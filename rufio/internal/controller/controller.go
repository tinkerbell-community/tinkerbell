package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	bmcv2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"github.com/tinkerbell/tinkerbell/pkg/kubeapi"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
)

const (
	trueString = "true"

	// defaultBMCConnectTimeout is used when the caller leaves the BMC connect timeout unset.
	defaultBMCConnectTimeout = time.Minute
)

var schemeBuilder = runtime.NewSchemeBuilder(
	scheme.AddToScheme,
	// The deprecated bmc Machine and Task, which only exist in v1alpha1.
	bmc.AddToScheme,
	// Jobs.
	bmcv2.AddToScheme,
	// Hardware: its BMC connection, conditions and status.attributes.outOfBand.
	v1alpha2.AddToScheme,
)

// DefaultScheme returns a scheme with all the types necessary for the Rufio controller.
func DefaultScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = schemeBuilder.AddToScheme(s)
	return s
}

// Reconciler is a type for managing Workflows.
type Reconciler struct {
	client  client.Client
	nowFunc func() time.Time
	backoff *backoff.ExponentialBackOff
}

func NewManager(cfg *rest.Config, opts ctrl.Options, bmcConnectTimeout, powerCheckInterval, inventoryRefreshInterval time.Duration, inventoryCollectionEnabled bool, maxConcurrentReconciles int) (ctrl.Manager, error) {
	if opts.Scheme == nil {
		opts.Scheme = DefaultScheme()
	}

	if bmcConnectTimeout <= 0 {
		bmcConnectTimeout = defaultBMCConnectTimeout
	}

	mgr, err := ctrl.NewManager(cfg, opts)
	if err != nil {
		return nil, fmt.Errorf("controller manager: %w", err)
	}

	ctrlOpts := ctrlcontroller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}
	if err := NewReconciler(mgr.GetClient()).SetupWithManager(context.Background(), mgr, NewClientFunc(bmcConnectTimeout), powerCheckInterval, inventoryRefreshInterval, inventoryCollectionEnabled, ctrlOpts); err != nil {
		return nil, fmt.Errorf("unable to create reconciler: %w", err)
	}

	return mgr, nil
}

// TODO(jacobweinstock): add functional arguments to the signature.
// TODO(jacobweinstock): write functional argument for customizing the backoff.
func NewReconciler(c client.Client) *Reconciler {
	bo := backoff.NewExponentialBackOff()
	bo.MaxInterval = 5 * time.Second
	return &Reconciler{
		client:  c,
		nowFunc: time.Now,
		backoff: bo,
	}
}

// SetupWithManager sets up the Rufio controllers with the Manager:
//   - Jobs, which run BMC operations;
//   - the deprecated bmc Tasks, still run as before;
//   - the deprecated bmc Machines, polled for their power state and inventory;
//   - Hardware with a BMC connection of its own, polled the same way;
//   - the translation of bmc Machines into the Hardware that refers to them.
//
// The controllers of the deprecated kinds only run while their CRDs exist.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, bmcClient ClientFunc, powerCheckInterval, inventoryRefreshInterval time.Duration, inventoryCollectionEnabled bool, opts ctrlcontroller.Options) error {
	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(ctx, &v1alpha2.Hardware{}, hardwareBMCRefIndexKey, hardwareBMCRefIndexFunc); err != nil {
		return fmt.Errorf("unable to index Hardware by bmcRef: %w", err)
	}
	if err := indexer.IndexField(ctx, &v1alpha2.Hardware{}, hardwareMachineIndexKey, hardwareMachineIndexFunc); err != nil {
		return fmt.Errorf("unable to index Hardware by translated Machine: %w", err)
	}

	machines, err := kubeapi.Served(mgr.GetClient(), &bmc.Machine{})
	if err != nil {
		return fmt.Errorf("checking whether bmc Machines are served: %w", err)
	}
	tasks, err := kubeapi.Served(mgr.GetClient(), &bmc.Task{})
	if err != nil {
		return fmt.Errorf("checking whether bmc Tasks are served: %w", err)
	}

	if machines {
		if err := NewMachineReconciler(mgr.GetClient(), mgr.GetEventRecorder("machine-controller"), bmcClient, powerCheckInterval, inventoryRefreshInterval, inventoryCollectionEnabled).SetupWithManager(ctx, mgr, opts); err != nil {
			return fmt.Errorf("unable to create Machines controller: %w", err)
		}
	}

	hardware := NewHardwareReconciler(mgr.GetClient(), mgr.GetEventRecorder("hardware-controller"), bmcClient, powerCheckInterval, inventoryRefreshInterval, inventoryCollectionEnabled)
	hardware.machines = machines
	if err := hardware.SetupWithManager(mgr, opts); err != nil {
		return fmt.Errorf("unable to create Hardware controller: %w", err)
	}

	if machines {
		if err := NewMachineTranslationReconciler(mgr.GetClient()).SetupWithManager(mgr, opts); err != nil {
			return fmt.Errorf("unable to create Machine translation controller: %w", err)
		}
	}

	jobs := NewJobReconciler(mgr.GetClient(), mgr.GetAPIReader(), bmcClient)
	jobs.tasks = tasks
	if err := jobs.SetupWithManager(ctx, mgr, opts); err != nil {
		return fmt.Errorf("unable to create Jobs controller: %w", err)
	}

	if tasks {
		if err := NewTaskReconciler(mgr.GetClient(), bmcClient).SetupWithManager(mgr, opts); err != nil {
			return fmt.Errorf("unable to create Tasks controller: %w", err)
		}
	}

	return nil
}
