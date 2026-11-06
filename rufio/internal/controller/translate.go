package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/tinkerbell/tinkerbell/api/conversion"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// hardwareMachineIndexKey indexes v1alpha2 Hardware by the bmc Machine, as
// "namespace/name", their BMC connection was translated from: the value of the
// conversion.AnnotationMachine annotation.
const hardwareMachineIndexKey = ".metadata.annotations.machine"

// hardwareMachineIndexFunc is the IndexerFunc for hardwareMachineIndexKey.
func hardwareMachineIndexFunc(obj client.Object) []string {
	if m := obj.GetAnnotations()[conversion.AnnotationMachine]; m != "" {
		return []string{m}
	}
	return nil
}

// bmcRefMachine returns the bmc Machine the v1alpha1 bmcRef of hw names, as
// "namespace/name", or "" if it names none. A bmcRef is a local reference: the
// Machine is in the namespace of the Hardware.
func bmcRefMachine(hw *v1alpha2.Hardware) string {
	ref := conversion.HardwareBMCRef(hw)
	if ref == nil || ref.Kind != machineKind || ref.Name == "" {
		return ""
	}
	if ref.APIGroup != nil && *ref.APIGroup != "" && *ref.APIGroup != bmcv1.GroupVersion.Group {
		return ""
	}
	return hw.Namespace + "/" + ref.Name
}

// MachineTranslationReconciler translates bmc Machines into the Hardware that
// refers to them: a Hardware created through v1alpha1 whose bmcRef names a
// Machine gets that Machine's connection as spec.bmc.connection, and the
// annotation conversion.AnnotationMachine naming the Machine.
//
// The annotation marks the connection as translated: while it is there, the
// connection follows the Machine (and the bmcRef: a bmcRef naming another
// Machine translates that one, a removed bmcRef removes the connection). A
// connection without the annotation was set by a user and is never changed; to
// take over a translated connection, remove the annotation.
type MachineTranslationReconciler struct {
	client client.Client
}

// NewMachineTranslationReconciler returns a new MachineTranslationReconciler.
func NewMachineTranslationReconciler(c client.Client) *MachineTranslationReconciler {
	return &MachineTranslationReconciler{client: c}
}

//+kubebuilder:rbac:groups=tinkerbell.org,resources=hardware,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=bmc.tinkerbell.org,resources=machines,verbs=get;list;watch

// Reconcile translates the bmc Machine of a Hardware into its spec.bmc.connection.
func (r *MachineTranslationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx).WithName("controllers/MachineTranslation").WithValues("hardware", req.NamespacedName)

	hw := &v1alpha2.Hardware{}
	if err := r.client.Get(ctx, req.NamespacedName, hw); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !hw.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	machine := bmcRefMachine(hw)
	translated := hw.Annotations[conversion.AnnotationMachine]
	var current *bmc.Connection
	if hw.Spec.BMC != nil {
		current = hw.Spec.BMC.Connection
	}

	switch {
	case translated == "" && current != nil:
		// Set by a user.
		return ctrl.Result{}, nil
	case machine == "" && translated == "":
		return ctrl.Result{}, nil
	case machine == "":
		// The bmcRef was removed, so is the connection translated from it.
		logger.Info("removing the BMC connection translated from a Machine the Hardware no longer refers to", "machine", translated)
		return ctrl.Result{}, r.patch(ctx, hw, nil, "")
	}

	ns, name, _ := strings.Cut(machine, "/")
	m := &bmcv1.Machine{}
	if err := r.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, m); err != nil {
		if apierrors.IsNotFound(err) {
			// Translated when it is created. A connection translated before stays.
			logger.V(1).Info("bmc Machine not found", "machine", machine)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get Machine %s: %w", machine, err)
	}
	conn, err := conversion.MachineConnectionToV1Alpha2(m.Spec.Connection)
	if err != nil {
		return ctrl.Result{}, reconcile.TerminalError(fmt.Errorf("translating Machine %s: %w", machine, err))
	}
	if translated == machine && current != nil && equality.Semantic.DeepEqual(normalizedConnection(current), normalizedConnection(conn)) {
		return ctrl.Result{}, nil
	}

	logger.Info("translating the BMC connection of a bmc Machine into the Hardware", "machine", machine)
	return ctrl.Result{}, r.patch(ctx, hw, conn, machine)
}

// patch sets the BMC connection of hw and the annotation naming the Machine it
// was translated from, or removes both when machine is "". The patch fails if hw
// changed since it was read, so a connection a user sets at the same time is not
// overwritten.
func (r *MachineTranslationReconciler) patch(ctx context.Context, hw *v1alpha2.Hardware, conn *bmc.Connection, machine string) error {
	patch := client.MergeFromWithOptions(hw.DeepCopy(), client.MergeFromWithOptimisticLock{})
	if hw.Spec.BMC == nil {
		hw.Spec.BMC = &v1alpha2.BMC{}
	}
	hw.Spec.BMC.Connection = conn
	annotations := hw.GetAnnotations()
	if machine == "" {
		delete(annotations, conversion.AnnotationMachine)
	} else {
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[conversion.AnnotationMachine] = machine
	}
	hw.SetAnnotations(annotations)
	if err := r.client.Patch(ctx, hw, patch); err != nil {
		return fmt.Errorf("failed to patch Hardware %s/%s BMC connection: %w", hw.Namespace, hw.Name, err)
	}
	return nil
}

// normalizedConnection fills in what the API server defaults, so a connection
// read back compares equal to the one written.
func normalizedConnection(c *bmc.Connection) *bmc.Connection {
	c = c.DeepCopy()
	if po := c.ProviderOptions; po != nil && po.IntelAMT != nil && po.IntelAMT.HostScheme == "" {
		po.IntelAMT.HostScheme = "http"
	}
	return c
}

// SetupWithManager sets up the controller with the Manager. It needs the
// hardwareBMCRefIndexKey and hardwareMachineIndexKey indexes.
func (r *MachineTranslationReconciler) SetupWithManager(mgr ctrl.Manager, opts ctrlcontroller.Options) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("machinetranslation").
		WithOptions(opts).
		For(&v1alpha2.Hardware{}, builder.WithPredicates(predicate.Or[client.Object](predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Watches(&bmcv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(r.machineHardware),
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		Complete(r)
}

// machineHardware maps a Machine to the Hardware whose bmcRef names it or whose
// connection was translated from it.
func (r *MachineTranslationReconciler) machineHardware(ctx context.Context, m client.Object) []reconcile.Request {
	return hardwareOfMachine(ctx, r.client, m, true)
}

// hardwareOfMachine returns requests for the Hardware whose connection was
// translated from the Machine m and, with byBMCRef, the Hardware whose bmcRef
// names it.
func hardwareOfMachine(ctx context.Context, c client.Client, m client.Object, byBMCRef bool) []reconcile.Request {
	seen := map[types.NamespacedName]bool{}
	var reqs []reconcile.Request
	add := func(list *v1alpha2.HardwareList) {
		for _, hw := range list.Items {
			key := types.NamespacedName{Namespace: hw.Namespace, Name: hw.Name}
			if !seen[key] {
				seen[key] = true
				reqs = append(reqs, reconcile.Request{NamespacedName: key})
			}
		}
	}
	logger := ctrl.LoggerFrom(ctx)
	list := &v1alpha2.HardwareList{}
	if err := c.List(ctx, list, client.InNamespace(m.GetNamespace()), client.MatchingFields{hardwareMachineIndexKey: m.GetNamespace() + "/" + m.GetName()}); err != nil {
		logger.Error(err, "listing the Hardware translated from a Machine", "machine", client.ObjectKeyFromObject(m))
	}
	add(list)
	if byBMCRef {
		list = &v1alpha2.HardwareList{}
		if err := c.List(ctx, list, client.InNamespace(m.GetNamespace()), client.MatchingFields{hardwareBMCRefIndexKey: m.GetName()}); err != nil {
			logger.Error(err, "listing the Hardware that refer to a Machine", "machine", client.ObjectKeyFromObject(m))
		}
		add(list)
	}
	return reqs
}
