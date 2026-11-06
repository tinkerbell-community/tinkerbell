package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/tink/controller/internal/translate"
	"github.com/tinkerbell/tinkerbell/tink/controller/internal/workflow"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var schemeBuilder = runtime.NewSchemeBuilder(
	clientgoscheme.AddToScheme,
	tinkerbell.AddToScheme,
	bmc.AddToScheme,
	v1alpha2.AddToScheme,
	bmcv1alpha2.AddToScheme,
)

type Config struct {
	// Namespaces are the namespaces to watch. Empty means all namespaces.
	Namespaces              []string
	Client                  *rest.Config
	EnableLeaderElection    bool
	LeaderElectionNamespace string
	ReferenceResolver       referenceResolver
	MaxConcurrentReconciles int
	MetadataURL             string
}

// referenceResolver renders Hardware and resolves the references of the objects the Workflow engine
// renders, as the kube backend does.
type referenceResolver interface {
	// RenderedHardware is for Workflows run in v1alpha1 compatibility mode, which render
	// Templates with the v1alpha1 Hardware.
	RenderedHardware(ctx context.Context, hw *tinkerbell.Hardware) (*tinkerbell.Hardware, error)
	RenderedHardwareV1Alpha2(ctx context.Context, hw *v1alpha2.Hardware) (*v1alpha2.Hardware, error)
	ResolveReferencesFor(ctx context.Context, src types.NamespacedName, refs map[string]v1alpha2.Reference) (map[string]any, error)
}

type Option func(*Config)

func WithNamespaces(namespaces ...string) Option {
	return func(c *Config) {
		c.Namespaces = namespaces
	}
}

func WithClient(client *rest.Config) Option {
	return func(c *Config) {
		c.Client = client
	}
}

func WithReferenceResolver(r referenceResolver) Option {
	return func(c *Config) {
		c.ReferenceResolver = r
	}
}

func WithEnableLeaderElection(enableLeaderElection bool) Option {
	return func(c *Config) {
		c.EnableLeaderElection = enableLeaderElection
	}
}

func WithLeaderElectionNamespace(namespace string) Option {
	return func(c *Config) {
		c.LeaderElectionNamespace = namespace
	}
}

func WithMetadataURL(u string) Option {
	return func(c *Config) {
		c.MetadataURL = u
	}
}

func NewConfig(opts ...Option) *Config {
	defatuls := &Config{
		EnableLeaderElection:    true,
		MaxConcurrentReconciles: 1,
	}

	for _, opt := range opts {
		opt(defatuls)
	}

	return defatuls
}

func (c *Config) Start(ctx context.Context, log logr.Logger) error {
	if c.ReferenceResolver == nil {
		return errors.New("reference resolver is required")
	}

	options := controllerruntime.Options{
		Logger:                  log,
		LeaderElection:          c.EnableLeaderElection,
		LeaderElectionID:        "tink-controller.tinkerbell.org",
		LeaderElectionNamespace: c.LeaderElectionNamespace,
		Metrics: server.Options{
			BindAddress: "0",
		},
		HealthProbeBindAddress: "0",
	}
	if len(c.Namespaces) > 0 {
		options.Cache = cache.Options{DefaultNamespaces: make(map[string]cache.Config, len(c.Namespaces))}
		for _, ns := range c.Namespaces {
			options.Cache.DefaultNamespaces[ns] = cache.Config{}
		}
	}

	var wfOpts []workflow.Option
	if c.MetadataURL != "" {
		wfOpts = append(wfOpts, workflow.WithMetadataURL(c.MetadataURL))
	}
	log.Info("template variable metadata_url", "metadataURL", c.MetadataURL)

	mgr, err := newManager(c.Client, c.ReferenceResolver, options, c.MaxConcurrentReconciles, wfOpts...)
	if err != nil {
		return err
	}

	return mgr.Start(ctx)
}

// NewManager creates a new controller manager with tink controller controllers pre-registered.
// If opts.Scheme is nil, DefaultScheme() is used.
func newManager(cfg *rest.Config, rr referenceResolver, opts controllerruntime.Options, maxConcurrentReconciles int, wfOpts ...workflow.Option) (controllerruntime.Manager, error) {
	if opts.Scheme == nil {
		s := runtime.NewScheme()
		_ = schemeBuilder.AddToScheme(s)
		opts.Scheme = s
	}

	mgr, err := controllerruntime.NewManager(cfg, opts)
	if err != nil {
		return nil, fmt.Errorf("controller manager: %w", err)
	}

	recorder := mgr.GetEventRecorder("tink-controller")
	wfOpts = append(wfOpts, workflow.WithEventRecorder(recorder))
	if err = workflow.NewReconciler(mgr.GetClient(), rr, wfOpts...).SetupWithManager(mgr, ctrlcontroller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}); err != nil {
		return nil, fmt.Errorf("setup workflow reconciler: %w", err)
	}

	// The v1alpha2 translations of the kinds only v1alpha1 has.
	if err = translate.NewTemplateReconciler(mgr.GetClient(), recorder).SetupWithManager(mgr, ctrlcontroller.Options{}); err != nil {
		return nil, fmt.Errorf("setup template translation reconciler: %w", err)
	}
	if err = translate.NewRuleSetReconciler(mgr.GetClient(), recorder).SetupWithManager(mgr, ctrlcontroller.Options{}); err != nil {
		return nil, fmt.Errorf("setup workflowruleset translation reconciler: %w", err)
	}

	return mgr, nil
}
