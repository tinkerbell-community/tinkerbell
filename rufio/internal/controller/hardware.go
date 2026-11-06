package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	bmclib "github.com/bmc-toolbox/bmclib/v2"
	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/api/conversion"
	bmcv1 "github.com/tinkerbell/tinkerbell/api/v1alpha1/bmc"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell/bmc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HardwareReconciler reports on the BMC of v1alpha2 Hardware, in the Hardware's
// status: whether the BMC can be reached (condition Contactable), the power
// state (condition PowerState) and the out-of-band inventory
// (status.attributes.outOfBand). It does not change the state of the machine.
//
// It polls the BMC of Hardware that has a BMC connection of its own, like the
// Machine controller polls a bmc Machine. A connection translated from a bmc
// Machine (annotation conversion.AnnotationMachine) is not polled a second
// time: the Machine controller keeps polling the Machine, and this controller
// copies the Machine's state into the Hardware conditions.
type HardwareReconciler struct {
	client                     client.Client
	recorder                   events.EventRecorder
	bmcClient                  ClientFunc
	powerCheckInterval         time.Duration
	inventoryRefreshInterval   time.Duration
	inventoryCollectionEnabled bool
	nowFunc                    func() time.Time
}

// NewHardwareReconciler returns a new HardwareReconciler. Its intervals and the
// inventory kill switch work as for NewMachineReconciler.
func NewHardwareReconciler(c client.Client, recorder events.EventRecorder, bmcClient ClientFunc, powerCheckInterval, inventoryRefreshInterval time.Duration, inventoryCollectionEnabled bool) *HardwareReconciler {
	return &HardwareReconciler{
		client:                     c,
		recorder:                   recorder,
		bmcClient:                  bmcClient,
		powerCheckInterval:         ternary(powerCheckInterval > 0, powerCheckInterval, machineRequeueInterval),
		inventoryRefreshInterval:   ternary(inventoryRefreshInterval > 0, inventoryRefreshInterval, defaultInventoryRefreshInterval),
		inventoryCollectionEnabled: inventoryCollectionEnabled,
		nowFunc:                    time.Now,
	}
}

//+kubebuilder:rbac:groups=tinkerbell.org,resources=hardware,verbs=get;list;watch;patch
//+kubebuilder:rbac:groups=tinkerbell.org,resources=hardware/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=bmc.tinkerbell.org,resources=machines,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets;,verbs=get;list;watch
//+kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch;update

// Reconcile reports on the BMC of a Hardware.
func (r *HardwareReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx).WithName("controllers/Hardware").WithValues("hardware", req.NamespacedName)

	hw := &v1alpha2.Hardware{}
	if err := r.client.Get(ctx, req.NamespacedName, hw); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !hw.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	before := hw.DeepCopy()
	var (
		conditions = hw.Status.Conditions
		result     ctrl.Result
		err        error
	)
	switch {
	case hw.Annotations[conversion.AnnotationMachine] != "":
		conditions, err = r.machineConditions(ctx, hw)
	case hw.Spec.BMC != nil && hw.Spec.BMC.Connection != nil:
		conditions, result, err = r.poll(ctx, logger, hw)
	default:
		conditions = withoutBMCConditions(conditions)
	}

	if patchErr := r.patchConditions(ctx, before, conditions); patchErr != nil {
		if err != nil {
			logger.Error(err, "reconciling the Hardware BMC failed and its conditions could not be recorded")
		}
		return ctrl.Result{}, patchErr
	}

	return result, err
}

// poll connects to the BMC of hw, which has a connection of its own, reads its
// power state and, when due, its inventory. It returns the resulting conditions.
func (r *HardwareReconciler) poll(ctx context.Context, logger logr.Logger, hw *v1alpha2.Hardware) ([]bmc.Condition, ctrl.Result, error) {
	conditions := append([]bmc.Condition(nil), hw.Status.Conditions...)
	conn := hw.Spec.BMC.Connection
	logger = logger.WithValues("host", conn.Host)

	username, password, opts, err := bmcCredentials(ctx, r.client, conn)
	if err != nil {
		// Requeue: the Secret may not exist yet.
		return conditions, ctrl.Result{}, fmt.Errorf("resolving Hardware %s/%s BMC credentials: %w", hw.Namespace, hw.Name, err)
	}

	bmcClient, err := r.bmcClient(ctx, logger, conn.Host, username, password, opts)
	if err != nil {
		logger.Error(err, "BMC connection failed")
		conditions = r.setCondition(conditions, hw, bmc.ConditionTypeBMCContactable, bmc.ConditionStatusFalse, err.Error())
		conditions = r.setCondition(conditions, hw, bmc.ConditionTypeBMCPowerState, bmc.ConditionStatusUnknown, "")
		// requeue as bmc connections can be transient.
		return conditions, ctrl.Result{RequeueAfter: r.powerCheckInterval}, nil
	}
	defer closeBMC(ctx, logger, bmcClient)

	rawState, err := bmcClient.GetPowerState(ctx)
	if err != nil {
		logger.Error(err, "failed to get Hardware power state")
		r.recorder.Eventf(hw, nil, corev1.EventTypeWarning, "GetPowerStateFailed", "GetPowerState", "get power state: %v", err)
		conditions = r.setCondition(conditions, hw, bmc.ConditionTypeBMCContactable, bmc.ConditionStatusFalse, fmt.Sprintf("get power state: %v", err))
		conditions = r.setCondition(conditions, hw, bmc.ConditionTypeBMCPowerState, bmc.ConditionStatusUnknown, "")
	} else {
		conditions = r.setCondition(conditions, hw, bmc.ConditionTypeBMCContactable, bmc.ConditionStatusTrue, "")
		conditions = r.setCondition(conditions, hw, bmc.ConditionTypeBMCPowerState, powerConditionStatus(toPowerState(rawState)), "")
	}

	// Collect and store BMC inventory, if due, over the same BMC connection.
	// Failures never affect the conditions or the result.
	r.reconcileInventoryIfDue(ctx, logger, bmcClient, hw)

	return conditions, ctrl.Result{RequeueAfter: r.powerCheckInterval}, nil
}

// reconcileInventoryIfDue collects the inventory of the BMC of hw when it is due
// (see dueForInventoryRefresh) and writes it to its status.
func (r *HardwareReconciler) reconcileInventoryIfDue(ctx context.Context, logger logr.Logger, bmcClient *bmclib.Client, hw *v1alpha2.Hardware) {
	if !r.inventoryCollectionEnabled || hw.Annotations[disableOutOfBandInventoryAnnotation] == trueString || !dueForInventoryRefresh(hw, hw, r.inventoryRefreshInterval) {
		return
	}
	if err := reconcileInventory(ctx, r.client, logger, bmcClient, hw); err != nil {
		logger.Error(err, "BMC inventory collection failed")
		r.recorder.Eventf(hw, nil, corev1.EventTypeWarning, "InventoryUnreachable", "GetInventory", "get BMC inventory: %v", err)
		return
	}
	clearRefreshInventoryAnnotation(ctx, r.client, r.recorder, logger, hw)
}

// machineConditions returns the conditions of hw, whose connection was
// translated from a bmc Machine, with the state the Machine controller recorded
// for that Machine.
func (r *HardwareReconciler) machineConditions(ctx context.Context, hw *v1alpha2.Hardware) ([]bmc.Condition, error) {
	conditions := append([]bmc.Condition(nil), hw.Status.Conditions...)
	machine := hw.Annotations[conversion.AnnotationMachine]
	ns, name, _ := strings.Cut(machine, "/")
	m := &bmcv1.Machine{}
	if err := r.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, m); err != nil {
		if apierrors.IsNotFound(err) {
			conditions = r.setCondition(conditions, hw, bmc.ConditionTypeBMCContactable, bmc.ConditionStatusUnknown, fmt.Sprintf("bmc Machine %s not found", machine))
			conditions = r.setCondition(conditions, hw, bmc.ConditionTypeBMCPowerState, bmc.ConditionStatusUnknown, "")
			return conditions, nil
		}
		return conditions, fmt.Errorf("failed to get Machine %s: %w", machine, err)
	}

	for _, c := range m.Status.Conditions {
		if c.Type == bmcv1.Contactable {
			conditions = bmc.SetCondition(conditions, bmc.Condition{
				Type:               bmc.ConditionTypeBMCContactable,
				Status:             bmc.ConditionStatus(c.Status),
				Message:            c.Message,
				LastUpdateTime:     c.LastUpdateTime,
				ObservedGeneration: hw.Generation,
			})
		}
	}
	if m.Status.Power != "" {
		conditions = r.setCondition(conditions, hw, bmc.ConditionTypeBMCPowerState, powerConditionStatus(m.Status.Power), "")
	}

	return conditions, nil
}

// setCondition sets a condition of hw in conditions. Its lastUpdateTime only
// changes with its status, so polling a BMC whose state stays the same does not
// write the Hardware.
func (r *HardwareReconciler) setCondition(conditions []bmc.Condition, hw *v1alpha2.Hardware, t bmc.ConditionType, s bmc.ConditionStatus, message string) []bmc.Condition {
	c := bmc.Condition{Type: t, Status: s, Message: message, ObservedGeneration: hw.Generation}
	for _, old := range conditions {
		if old.Type == t && old.Status == s {
			c.LastUpdateTime = old.LastUpdateTime
		}
	}
	if c.LastUpdateTime.IsZero() {
		c.LastUpdateTime = metav1.NewTime(r.nowFunc())
	}
	return bmc.SetCondition(conditions, c)
}

// patchConditions writes conditions to the status of the Hardware read as
// before, if they changed. The patch holds the conditions only: the inventory
// in the same status has its own writer and field manager.
func (r *HardwareReconciler) patchConditions(ctx context.Context, before *v1alpha2.Hardware, conditions []bmc.Condition) error {
	if equality.Semantic.DeepEqual(before.Status.Conditions, conditions) {
		return nil
	}
	hw := before.DeepCopy()
	hw.Status.Conditions = conditions
	if err := r.client.Status().Patch(ctx, hw, client.MergeFrom(before)); err != nil {
		return fmt.Errorf("failed to patch Hardware %s/%s status conditions: %w", hw.Namespace, hw.Name, err)
	}
	return nil
}

// withoutBMCConditions drops the conditions this controller sets, for Hardware
// that no longer has a BMC.
func withoutBMCConditions(conditions []bmc.Condition) []bmc.Condition {
	var out []bmc.Condition
	for _, c := range conditions {
		if c.Type != bmc.ConditionTypeBMCContactable && c.Type != bmc.ConditionTypeBMCPowerState {
			out = append(out, c)
		}
	}
	return out
}

// powerConditionStatus is the status of the PowerState condition for a power state.
func powerConditionStatus(p bmcv1.PowerState) bmc.ConditionStatus {
	switch p {
	case bmcv1.On:
		return bmc.ConditionStatusOn
	case bmcv1.Off:
		return bmc.ConditionStatusOff
	default:
		return bmc.ConditionStatusUnknown
	}
}

// hardwareBMCChanged lets through the Hardware events that change what this
// controller reports: creation, a changed spec.bmc, a connection translated from
// another Machine or no longer translated, and a requested inventory refresh.
// Other changes, to the spec or to the status this and other controllers write,
// must not start another round of BMC calls; polling is paced by the requeue
// interval.
var hardwareBMCChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		older, ok := e.ObjectOld.(*v1alpha2.Hardware)
		if !ok {
			return false
		}
		newer, ok := e.ObjectNew.(*v1alpha2.Hardware)
		if !ok {
			return false
		}
		return !equality.Semantic.DeepEqual(older.Spec.BMC, newer.Spec.BMC) ||
			older.Annotations[conversion.AnnotationMachine] != newer.Annotations[conversion.AnnotationMachine] ||
			(newer.Annotations[refreshInventoryAnnotation] == trueString && older.Annotations[refreshInventoryAnnotation] != trueString)
	},
	DeleteFunc: func(event.DeleteEvent) bool { return false },
}

// SetupWithManager sets up the controller with the Manager. It needs the
// hardwareMachineIndexKey index.
func (r *HardwareReconciler) SetupWithManager(mgr ctrl.Manager, opts ctrlcontroller.Options) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("hardware").
		WithOptions(opts).
		For(&v1alpha2.Hardware{}, builder.WithPredicates(hardwareBMCChanged)).
		Watches(&bmcv1.Machine{}, handler.EnqueueRequestsFromMapFunc(r.translatedHardware)).
		Complete(r)
}

// translatedHardware maps a Machine to the Hardware whose connection was
// translated from it, which show its state.
func (r *HardwareReconciler) translatedHardware(ctx context.Context, m client.Object) []reconcile.Request {
	return hardwareOfMachine(ctx, r.client, m, false)
}
