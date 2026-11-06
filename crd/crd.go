package crd

import (
	"context"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/go-logr/logr"
	apiv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	v1 "k8s.io/apiextensions-apiserver/pkg/client/applyconfiguration/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"
)

//go:embed bases
var crdFS embed.FS

func mustReadCRD(path string) []byte {
	data, err := crdFS.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("reading embedded CRD %s: %v", path, err))
	}
	return data
}

// Tinkerbell is the struct that holds the raw custom resource definitions
// and a CRD client for operations.
type Tinkerbell struct {
	CRDs   map[string][]byte
	Client clientset.Interface
	// Dynamic is used to rewrite objects into the storage version. It is
	// created from the rest config when not set.
	Dynamic    dynamic.Interface
	Logger     logr.Logger
	restConfig *rest.Config
	webhook    *ConversionWebhook
	// removeDeprecated enables RemoveDeprecated; see WithDeprecatedCRDRemoval.
	removeDeprecated bool
	// kube reads and patches the objects RemoveDeprecated checks. It is created
	// from the rest config when deprecated CRD removal is enabled.
	kube client.Client
}

const (
	// GroupTinkerbell is the API group for core Tinkerbell resources.
	GroupTinkerbell = "tinkerbell.org"
	// GroupBMC is the API group for BMC resources.
	GroupBMC = "bmc.tinkerbell.org"

	fieldManager = "Tinkerbell CLI"

	// storageMigrationWait bounds how long MigrateStorage keeps retrying an
	// object type whose objects it cannot read or rewrite. Reading them needs the
	// conversion webhook, which the API server may not reach right away: a new
	// pod's endpoint takes a moment to be routed, for example.
	storageMigrationWait = 5 * time.Minute
)

// AvailableVersions lists all supported CRD API versions.
var AvailableVersions = []string{"v1alpha1", "v1alpha2"}

// TinkerbellDefaults contains every Tinkerbell CRD, as installed: kinds that
// exist in both API versions are served in both and stored as v1alpha2, with
// v1alpha1 converted by the conversion webhook. Kinds that only exist in
// v1alpha1 (Template, WorkflowRuleSet, and the bmc Machine and Task) are
// deprecated (DeprecatedCRDs); WithDeprecatedCRDRemoval removes them.
var TinkerbellDefaults = map[string][]byte{
	"hardware.tinkerbell.org":         mustReadCRD("bases/tinkerbell.org_hardware.yaml"),
	"workflows.tinkerbell.org":        mustReadCRD("bases/tinkerbell.org_workflows.yaml"),
	"tasks.tinkerbell.org":            mustReadCRD("bases/tinkerbell.org_tasks.yaml"),
	"policies.tinkerbell.org":         mustReadCRD("bases/tinkerbell.org_policies.yaml"),
	"templates.tinkerbell.org":        mustReadCRD("bases/tinkerbell.org_templates.yaml"),
	"workflowrulesets.tinkerbell.org": mustReadCRD("bases/tinkerbell.org_workflowrulesets.yaml"),
	"jobs.bmc.tinkerbell.org":         mustReadCRD("bases/bmc.tinkerbell.org_jobs.yaml"),
	"machines.bmc.tinkerbell.org":     mustReadCRD("bases/bmc.tinkerbell.org_machines.yaml"),
	"tasks.bmc.tinkerbell.org":        mustReadCRD("bases/bmc.tinkerbell.org_tasks.yaml"),
}

// CRDsByVersion maps an API version to single-version views of the CRDs that
// serve it, for documentation and the UI. They are not meant to be installed.
var CRDsByVersion = versionViews(TinkerbellDefaults)

// TinkerbellV1Alpha2 contains single-version views of the v1alpha2 CRDs.
var TinkerbellV1Alpha2 = CRDsByVersion["v1alpha2"]

// versionViews splits multi-version CRDs into one CRD per version.
func versionViews(crds map[string][]byte) map[string]map[string][]byte {
	out := map[string]map[string][]byte{}
	for name, raw := range crds {
		var c apiv1.CustomResourceDefinition
		if err := sigsyaml.Unmarshal(raw, &c); err != nil {
			panic(fmt.Sprintf("decoding embedded CRD %s: %v", name, err))
		}
		for _, v := range c.Spec.Versions {
			view := c.DeepCopy()
			view.Spec.Versions = []apiv1.CustomResourceDefinitionVersion{v}
			view.Status = apiv1.CustomResourceDefinitionStatus{}
			b, err := sigsyaml.Marshal(view)
			if err != nil {
				panic(fmt.Sprintf("encoding CRD %s %s: %v", name, v.Name, err))
			}
			if out[v.Name] == nil {
				out[v.Name] = map[string][]byte{}
			}
			out[v.Name][name] = b
		}
	}
	return out
}

// ConversionWebhook tells the API server how to reach the conversion webhook
// for CRDs that serve more than one version.
type ConversionWebhook struct {
	// ServiceNamespace, ServiceName and ServicePort identify the Service in
	// front of the webhook. They are ignored when URL is set.
	ServiceNamespace string
	ServiceName      string
	ServicePort      int32
	// URL is the webhook's address, for API servers that cannot reach Services
	// (for example the embedded API server).
	URL string
	// Path is the HTTP path of the webhook, used with a Service.
	Path string
	// CABundle is the PEM encoded CA certificate that signed the webhook's
	// serving certificate.
	CABundle []byte
}

// ConfigOption is a function that sets a configuration option.
type ConfigOption func(*Tinkerbell)

func WithRestConfig(config *rest.Config) ConfigOption {
	return func(t *Tinkerbell) {
		t.restConfig = config
	}
}

// WithLogger sets a structured logger for Kubernetes API server warnings.
func WithLogger(logger logr.Logger) ConfigOption {
	return func(t *Tinkerbell) {
		t.Logger = logger
	}
}

// WithConversionWebhook configures the conversion webhook of multi-version CRDs.
func WithConversionWebhook(w ConversionWebhook) ConfigOption {
	return func(t *Tinkerbell) {
		t.webhook = &w
	}
}

// WithDeprecatedCRDRemoval makes MigrateAndReady remove the CRDs of the kinds
// that only exist in v1alpha1 once their objects have moved to v1alpha2 (see
// RemoveDeprecated), and stops it from installing them again. Clients that
// still create those kinds must have moved to v1alpha2 first: their objects
// are deleted with the CRD.
func WithDeprecatedCRDRemoval() ConfigOption {
	return func(t *Tinkerbell) {
		t.removeDeprecated = true
	}
}

// logrWarningHandler adapts a logr.Logger to the rest.WarningHandler interface.
type logrWarningHandler struct {
	logger logr.Logger
}

func (h logrWarningHandler) HandleWarningHeader(code int, agent string, text string) {
	h.logger.Info("Kubernetes API warning", "code", code, "agent", agent, "text", text)
}

// NewTinkerbell returns a struct with a CRD client and the CRDs.
// If no CRDs are provided, it will use the default (TinkerbellDefaults) CRDs.
func NewTinkerbell(opts ...ConfigOption) (Tinkerbell, error) {
	tbell := Tinkerbell{
		CRDs: TinkerbellDefaults,
	}
	for _, opt := range opts {
		opt(&tbell)
	}

	if tbell.restConfig != nil {
		cfg := rest.CopyConfig(tbell.restConfig)
		if tbell.Logger.GetSink() != nil {
			cfg.WarningHandler = logrWarningHandler{logger: tbell.Logger}
		}
		crdClient, err := clientset.NewForConfig(cfg)
		if err != nil {
			return Tinkerbell{}, fmt.Errorf("failed to create CRD client: %w", err)
		}
		tbell.Client = crdClient
		if err := tbell.objectClients(); err != nil {
			return Tinkerbell{}, err
		}
	}

	if tbell.Client == nil {
		return Tinkerbell{}, fmt.Errorf("no Kubernetes client configured: provide a rest.Config (e.g. WithRestConfig) or set Client directly via a ConfigOption")
	}

	return tbell, nil
}

// objectClients creates, from the rest config, the clients of the Tinkerbell
// objects that are not set: the dynamic client, and the client deprecated CRD
// removal reads and patches objects with.
func (t *Tinkerbell) objectClients() error {
	// Conversions of deprecated versions warn on every object; don't log those.
	cfg := rest.CopyConfig(t.restConfig)
	cfg.WarningHandler = rest.NoWarnings{}
	var err error
	if t.Dynamic == nil {
		if t.Dynamic, err = dynamic.NewForConfig(cfg); err != nil {
			return fmt.Errorf("failed to create dynamic client: %w", err)
		}
	}
	if !t.removeDeprecated || t.kube != nil {
		return nil
	}
	scheme, err := deprecatedScheme()
	if err != nil {
		return fmt.Errorf("failed to create scheme: %w", err)
	}
	if t.kube, err = client.New(cfg, client.Options{Scheme: scheme}); err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	return nil
}

// MigrateAndReady installs or upgrades the CRDs, waits for them to be
// established, and then rewrites existing objects into the storage version.
// With WithDeprecatedCRDRemoval, it then removes the CRDs of the kinds that only
// exist in v1alpha1 whose objects have all moved to v1alpha2.
func (t Tinkerbell) MigrateAndReady(ctx context.Context) error {
	if err := t.Migrate(ctx); err != nil {
		return err
	}
	if err := t.Ready(ctx); err != nil {
		return err
	}
	if err := t.MigrateStorage(ctx); err != nil {
		return err
	}
	if t.removeDeprecated {
		return t.RemoveDeprecated(ctx)
	}
	return nil
}

// Migrate applies the CRDs to the cluster. With WithDeprecatedCRDRemoval, a
// deprecated CRD that was removed, or is being removed, is not applied.
func (t Tinkerbell) Migrate(ctx context.Context) error {
	decoder := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	for name, raw := range t.CRDs {
		if ok, err := t.managed(ctx, name); err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		obj := &unstructured.Unstructured{}
		if _, _, err := decoder.Decode(raw, nil, obj); err != nil {
			return fmt.Errorf("failed to decode YAML: %w", err)
		}
		if err := t.setConversion(obj); err != nil {
			return err
		}

		// Try apply, if that fails, try create. Apply only works if the CRD already exists.
		if errApply := t.apply(ctx, obj); errApply != nil {
			if errUpdate := t.update(ctx, obj); errUpdate != nil {
				if errCreate := t.create(ctx, obj); errCreate != nil {
					return errors.Join(errApply, errUpdate, errCreate)
				}
			}
		}
	}

	return nil
}

// setConversion points a multi-version CRD at the conversion webhook. Their
// versions have different schemas, so serving them without conversion would
// hand clients objects of the wrong shape.
func (t Tinkerbell) setConversion(obj *unstructured.Unstructured) error {
	versions, _, _ := unstructured.NestedSlice(obj.Object, "spec", "versions")
	if len(versions) < 2 {
		return nil
	}
	if t.webhook == nil {
		return fmt.Errorf("CRD %s serves several versions and needs the conversion webhook, which is not configured", obj.GetName())
	}
	cc := map[string]any{}
	if t.webhook.URL != "" {
		cc["url"] = t.webhook.URL
	} else {
		cc["service"] = map[string]any{
			"namespace": t.webhook.ServiceNamespace,
			"name":      t.webhook.ServiceName,
			"port":      int64(t.webhook.ServicePort),
			"path":      t.webhook.Path,
		}
	}
	if len(t.webhook.CABundle) > 0 {
		cc["caBundle"] = base64.StdEncoding.EncodeToString(t.webhook.CABundle)
	}
	return unstructured.SetNestedMap(obj.Object, map[string]any{
		"strategy": "Webhook",
		"webhook": map[string]any{
			"clientConfig":             cc,
			"conversionReviewVersions": []any{"v1"},
		},
	}, "spec", "conversion")
}

// MigrateStorage rewrites the objects of every CRD that still has objects
// stored in an older version, so that they are stored in the current storage
// version, and then records that only the storage version is in use. For the
// Tinkerbell CRDs that serve v1alpha1 and v1alpha2, this moves every object
// to v1alpha2; the API server converts each one through the webhook.
func (t Tinkerbell) MigrateStorage(ctx context.Context) error {
	for name := range t.CRDs {
		if ok, err := t.managed(ctx, name); err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		c, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get CRD %s: %w", name, err)
		}
		storage := ""
		for _, v := range c.Spec.Versions {
			if v.Storage {
				storage = v.Name
			}
		}
		if storage == "" || len(c.Status.StoredVersions) == 0 || slices.Equal(c.Status.StoredVersions, []string{storage}) {
			continue
		}
		if t.Dynamic == nil {
			return errors.New("migrating stored objects needs a dynamic client")
		}
		n, err := t.rewriteWithRetry(ctx, c, storage)
		if err != nil {
			return fmt.Errorf("rewriting %s objects into %s: %w", c.Spec.Names.Plural, storage, err)
		}
		if err := t.setStoredVersions(ctx, name, storage); err != nil {
			return fmt.Errorf("updating stored versions of CRD %s: %w", name, err)
		}
		if t.Logger.GetSink() != nil {
			t.Logger.Info("migrated stored objects", "resource", gvr(c, storage).String(), "objects", n, "previousStoredVersions", c.Status.StoredVersions)
		}
	}
	return nil
}

// rewriteWithRetry rewrites c's objects into version, starting over until it
// succeeds or storageMigrationWait has passed. Rewriting an object that is
// already stored in version changes nothing.
func (t Tinkerbell) rewriteWithRetry(ctx context.Context, c *apiv1.CustomResourceDefinition, version string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, storageMigrationWait)
	defer cancel()
	var n int
	err := retry.Do(func() error {
		var err error
		n, err = t.rewrite(ctx, c, version)
		return err
	},
		retry.Context(ctx),
		retry.Attempts(0),
		retry.Delay(time.Second),
		retry.MaxDelay(15*time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.OnRetry(func(attempt uint, err error) {
			if t.Logger.GetSink() != nil {
				t.Logger.Info("cannot migrate stored objects yet, retrying; reading them needs the conversion webhook",
					"resource", c.Spec.Names.Plural, "attempt", attempt+1, "error", err.Error())
			}
		}),
	)
	return n, err
}

func (t Tinkerbell) rewrite(ctx context.Context, c *apiv1.CustomResourceDefinition, version string) (int, error) {
	res := t.Dynamic.Resource(gvr(c, version))
	count := 0
	opts := metav1.ListOptions{Limit: 250}
	for {
		list, err := res.List(ctx, opts)
		if err != nil {
			return count, err
		}
		for i := range list.Items {
			item := &list.Items[i]
			if err := retry.Do(func() error {
				ri := res.Namespace(item.GetNamespace())
				_, err := ri.Update(ctx, item, metav1.UpdateOptions{FieldManager: "tinkerbell-storage-migration"})
				if err == nil || isNotFound(err) {
					return nil
				}
				if isConflict(err) {
					// Changed meanwhile: that write already stored the new version, but
					// re-read and rewrite anyway rather than reason about it.
					fresh, gerr := ri.Get(ctx, item.GetName(), metav1.GetOptions{})
					if gerr != nil {
						if isNotFound(gerr) {
							return nil
						}
						return gerr
					}
					item = fresh
				}
				return err
			}, retry.Attempts(5), retry.Delay(200*time.Millisecond), retry.Context(ctx), retry.LastErrorOnly(true)); err != nil {
				return count, fmt.Errorf("%s/%s: %w", item.GetNamespace(), item.GetName(), err)
			}
			count++
		}
		if list.GetContinue() == "" {
			return count, nil
		}
		opts.Continue = list.GetContinue()
	}
}

func (t Tinkerbell) setStoredVersions(ctx context.Context, name, version string) error {
	return retry.Do(func() error {
		c, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		c.Status.StoredVersions = []string{version}
		_, err = t.Client.ApiextensionsV1().CustomResourceDefinitions().UpdateStatus(ctx, c, metav1.UpdateOptions{FieldManager: fieldManager})
		return err
	}, retry.Attempts(5), retry.Delay(200*time.Millisecond), retry.Context(ctx), retry.LastErrorOnly(true))
}

// Ready checks if the CRDs exist in the cluster and are established.
func (t Tinkerbell) Ready(ctx context.Context) error {
	// Get the CRDs from the cluster to verify they are available and usable.
	for name := range t.CRDs {
		if ok, err := t.managed(ctx, name); err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		if err := retry.Do(func() error {
			crd, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("failed to get CRD %s: %w", name, err)
			}

			establishedCond := getCondition(crd, apiv1.Established)
			namesAcceptedCond := getCondition(crd, apiv1.NamesAccepted)
			if establishedCond == nil || establishedCond.Status != apiv1.ConditionTrue {
				return fmt.Errorf("CRD %s is not established yet", name)
			}
			if namesAcceptedCond == nil || namesAcceptedCond.Status != apiv1.ConditionTrue {
				return fmt.Errorf("CRD %s names are not accepted yet", name)
			}
			return nil
		}, retry.Attempts(5), retry.Delay(2*time.Second), retry.Context(ctx)); err != nil {
			return fmt.Errorf("failed waiting for CRD %s to be ready: %w", name, err)
		}
	}

	return nil
}

func (t Tinkerbell) create(ctx context.Context, obj *unstructured.Unstructured) error {
	var crdef apiv1.CustomResourceDefinition
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &crdef); err != nil {
		return fmt.Errorf("failed to convert unstructured to CRD: %w", err)
	}
	if _, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, &crdef, metav1.CreateOptions{FieldManager: fieldManager}); err != nil {
		return fmt.Errorf("failed to create CRD: %w", err)
	}

	return nil
}

func (t Tinkerbell) apply(ctx context.Context, obj *unstructured.Unstructured) error {
	crdef := &v1.CustomResourceDefinitionApplyConfiguration{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, crdef); err != nil {
		return fmt.Errorf("failed to convert unstructured to CRD: %w", err)
	}

	if _, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Apply(ctx, crdef, metav1.ApplyOptions{FieldManager: fieldManager}); err != nil {
		return fmt.Errorf("failed to apply CRD: %w", err)
	}

	return nil
}

func (t Tinkerbell) update(ctx context.Context, obj *unstructured.Unstructured) error {
	var crdef apiv1.CustomResourceDefinition
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &crdef); err != nil {
		return fmt.Errorf("failed to convert unstructured to CRD: %w", err)
	}
	// Get the existing CRD to update it.
	existingCRD, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, crdef.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get existing CRD %s: %w", crdef.Name, err)
	}
	// Update the existing CRD with the new spec.
	crdef.ResourceVersion = existingCRD.ResourceVersion
	crdef.UID = existingCRD.UID
	crdef.CreationTimestamp = existingCRD.CreationTimestamp
	if _, err := t.Client.ApiextensionsV1().CustomResourceDefinitions().Update(ctx, &crdef, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
		return fmt.Errorf("failed to update CRD: %w", err)
	}

	return nil
}

// getCondition returns a condition from a list of conditions if it exists.
func getCondition(crd *apiv1.CustomResourceDefinition, conditionType apiv1.CustomResourceDefinitionConditionType) *apiv1.CustomResourceDefinitionCondition {
	for _, cond := range crd.Status.Conditions {
		if cond.Type == conditionType {
			return &cond
		}
	}
	return nil
}
