package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	udsv1alpha1 "github.com/celttechie/uds-vm-operator/pkg/apis/uds/v1alpha1"
)

const (
	poolLabelKey     = "uds.dev/pool"
	revisionLabelKey = "uds.dev/revision"
)

// PoolReconciler reconciles UDSVirtualMachinePool resources for zero-downtime rolling fleet updates.
type PoolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

// ComputeTemplateHash returns a deterministic 10-character SHA-256 hash of a UDSVirtualMachineSpec.
func ComputeTemplateHash(spec udsv1alpha1.UDSVirtualMachineSpec) (string, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal template spec: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:10], nil
}

// BuildPoolReplicaVM constructs a child UDSVirtualMachine stamped from the pool template.
func BuildPoolReplicaVM(pool *udsv1alpha1.UDSVirtualMachinePool, name, revision string) *udsv1alpha1.UDSVirtualMachine {
	labels := make(map[string]string, len(pool.Spec.Template.Metadata.Labels)+2)
	for k, v := range pool.Spec.Template.Metadata.Labels {
		labels[k] = v
	}
	labels[poolLabelKey] = pool.Name
	labels[revisionLabelKey] = revision

	annotations := make(map[string]string, len(pool.Spec.Template.Metadata.Annotations))
	for k, v := range pool.Spec.Template.Metadata.Annotations {
		annotations[k] = v
	}

	var specCopy udsv1alpha1.UDSVirtualMachineSpec
	pool.Spec.Template.Spec.DeepCopyInto(&specCopy)

	return &udsv1alpha1.UDSVirtualMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: udsv1alpha1.GroupVersion.String(),
			Kind:       "UDSVirtualMachine",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   pool.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: specCopy,
	}
}

func resolvePoolStrategy(pool *udsv1alpha1.UDSVirtualMachinePool) (desiredReplicas, maxSurge, maxUnavailable int32, strategyType string) {
	desiredReplicas = 1
	if pool.Spec.Replicas != nil {
		desiredReplicas = *pool.Spec.Replicas
	}

	strategyType = pool.Spec.Strategy.Type
	if strategyType == "" {
		strategyType = "RollingUpdate"
	}

	if strategyType == "Recreate" {
		return desiredReplicas, 0, desiredReplicas, strategyType
	}

	maxSurge = 1
	maxUnavailable = 0
	if pool.Spec.Strategy.RollingUpdate != nil {
		if pool.Spec.Strategy.RollingUpdate.MaxSurge != nil {
			maxSurge = *pool.Spec.Strategy.RollingUpdate.MaxSurge
		}
		if pool.Spec.Strategy.RollingUpdate.MaxUnavailable != nil {
			maxUnavailable = *pool.Spec.Strategy.RollingUpdate.MaxUnavailable
		}
	}
	return desiredReplicas, maxSurge, maxUnavailable, strategyType
}

// Reconcile executes the level-triggered fleet rolling update loop for UDSVirtualMachinePool.
func (r *PoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("pool", req.NamespacedName)

	var pool udsv1alpha1.UDSVirtualMachinePool
	if err := r.Get(ctx, req.NamespacedName, &pool); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	currentRev, err := ComputeTemplateHash(pool.Spec.Template.Spec)
	if err != nil {
		return ctrl.Result{}, err
	}

	var vmList udsv1alpha1.UDSVirtualMachineList
	if err := r.List(ctx, &vmList, client.InNamespace(pool.Namespace), client.MatchingLabels{poolLabelKey: pool.Name}); err != nil {
		return ctrl.Result{}, err
	}

	// Collect active (non-deleting) child VMs sorted deterministically by CreationTimestamp then Name.
	var activeVMs []*udsv1alpha1.UDSVirtualMachine
	existingNames := make(map[string]struct{}, len(vmList.Items))
	for i := range vmList.Items {
		vm := &vmList.Items[i]
		existingNames[vm.Name] = struct{}{}
		if !vm.DeletionTimestamp.IsZero() {
			continue
		}
		activeVMs = append(activeVMs, vm)
	}
	sort.Slice(activeVMs, func(i, j int) bool {
		ti := activeVMs[i].CreationTimestamp.Time
		tj := activeVMs[j].CreationTimestamp.Time
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return activeVMs[i].Name < activeVMs[j].Name
	})

	var updatedVMs, outdatedVMs []*udsv1alpha1.UDSVirtualMachine
	var readyTotal, readyUpdated int32
	for _, vm := range activeVMs {
		isReady := vm.Status.Phase == "Ready"
		if isReady {
			readyTotal++
		}
		if vm.Labels[revisionLabelKey] == currentRev {
			updatedVMs = append(updatedVMs, vm)
			if isReady {
				readyUpdated++
			}
		} else {
			outdatedVMs = append(outdatedVMs, vm)
		}
	}

	desiredReplicas, maxSurge, maxUnavailable, strategyType := resolvePoolStrategy(&pool)
	maxTotal := int(desiredReplicas + maxSurge)
	minAvailable := int(desiredReplicas) - int(maxUnavailable)
	if minAvailable < 0 {
		minAvailable = 0
	}

	// 1. Drain outdated replicas first when readiness gate allows (or Recreate / scale-to-zero).
	deleted := 0
	if len(outdatedVMs) > 0 {
		for _, oldVM := range outdatedVMs {
			canDelete := desiredReplicas == 0 || strategyType == "Recreate" || int(readyTotal) > minAvailable
			if !canDelete {
				break
			}
			if err := r.Delete(ctx, oldVM); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			log.Info("Drained outdated pool replica", "vm", oldVM.Name, "oldRevision", oldVM.Labels[revisionLabelKey])
			if oldVM.Status.Phase == "Ready" {
				readyTotal--
			}
			deleted++
		}
	}

	// 2. Scale up / surge new revision replicas up to maxTotal.
	created := 0
	activeAfterDrain := len(activeVMs) - deleted
	for len(updatedVMs)+created < int(desiredReplicas) && activeAfterDrain+created < maxTotal {
		name := nextReplicaName(pool.Name, currentRev, existingNames)
		existingNames[name] = struct{}{}
		childVM := BuildPoolReplicaVM(&pool, name, currentRev)
		if r.Scheme != nil {
			if err := controllerutil.SetControllerReference(&pool, childVM, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}
		}
		if err := r.Create(ctx, childVM); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Created pool replica", "vm", name, "revision", currentRev)
		created++
	}

	// 3. Scale down excess current-revision replicas if desiredReplicas was reduced.
	if len(outdatedVMs) == 0 && len(updatedVMs) > int(desiredReplicas) {
		for i := len(updatedVMs) - 1; i >= int(desiredReplicas); i-- {
			excessVM := updatedVMs[i]
			if err := r.Delete(ctx, excessVM); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			log.Info("Scaled down excess pool replica", "vm", excessVM.Name)
			deleted++
		}
	}

	// 3. Evaluate and persist pool status.
	totalActive := int32(len(activeVMs) + created - deleted)
	outdatedRemaining := len(outdatedVMs) - deleted
	if outdatedRemaining < 0 {
		outdatedRemaining = 0
	}

	phase := "Provisioning"
	msg := fmt.Sprintf("Waiting for %d/%d updated replicas to become Ready", readyUpdated, desiredReplicas)
	readyCondStatus := metav1.ConditionFalse
	readyReason := "PoolProgressing"

	if desiredReplicas == 0 && totalActive == 0 {
		phase = "Ready"
		msg = "Pool scaled to 0 replicas"
		readyCondStatus = metav1.ConditionTrue
		readyReason = "PoolReady"
	} else if outdatedRemaining > 0 {
		phase = "Updating"
		msg = fmt.Sprintf("Rolling update to revision %s in progress (%d outdated remaining)", currentRev, outdatedRemaining)
	} else if readyUpdated >= desiredReplicas && totalActive == desiredReplicas {
		phase = "Ready"
		msg = fmt.Sprintf("All %d replicas Ready on revision %s", desiredReplicas, currentRev)
		readyCondStatus = metav1.ConditionTrue
		readyReason = "PoolReady"
	}

	pool.Status.Replicas = totalActive
	pool.Status.ReadyReplicas = readyTotal
	pool.Status.UpdatedReplicas = int32(len(updatedVMs) + created)
	if len(outdatedVMs) == 0 && deleted > 0 {
		pool.Status.UpdatedReplicas -= int32(deleted)
	}
	pool.Status.CurrentRevision = currentRev
	pool.Status.Phase = phase
	pool.Status.Message = msg
	meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             readyCondStatus,
		Reason:             readyReason,
		Message:            msg,
		ObservedGeneration: pool.Generation,
	})

	if err := r.Status().Update(ctx, &pool); err != nil {
		return ctrl.Result{}, err
	}

	if phase != "Ready" {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

func nextReplicaName(poolName, rev string, existing map[string]struct{}) string {
	for i := 0; ; i++ {
		candidate := fmt.Sprintf("%s-%s-%d", poolName, rev, i)
		if _, exists := existing[candidate]; !exists {
			return candidate
		}
	}
}

// SetupWithManager registers the PoolReconciler with the controller-runtime Manager.
func (r *PoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&udsv1alpha1.UDSVirtualMachinePool{}).
		Owns(&udsv1alpha1.UDSVirtualMachine{}).
		Complete(r)
}
