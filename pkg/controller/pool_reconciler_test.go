package controller

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	udsv1alpha1 "github.com/celttechie/uds-vm-operator/pkg/apis/uds/v1alpha1"
)

func newPoolTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := udsv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme failed: %v", err)
	}
	return s
}

func int32Ptr(v int32) *int32 { return &v }

func makeTestPool(name string, replicas int32, dsName string) *udsv1alpha1.UDSVirtualMachinePool {
	return &udsv1alpha1.UDSVirtualMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "default",
			Generation: 1,
		},
		Spec: udsv1alpha1.UDSVirtualMachinePoolSpec{
			Replicas: int32Ptr(replicas),
			Strategy: udsv1alpha1.RollingUpdateStrategySpec{
				Type: "RollingUpdate",
				RollingUpdate: &udsv1alpha1.RollingUpdateConfig{
					MaxSurge:       int32Ptr(1),
					MaxUnavailable: int32Ptr(0),
				},
			},
			Template: udsv1alpha1.UDSVirtualMachineTemplateSpec{
				Spec: udsv1alpha1.UDSVirtualMachineSpec{
					OSType:      "linux",
					CPUs:        2,
					Memory:      "2Gi",
					RunStrategy: "Always",
					Disk: udsv1alpha1.DiskSpec{
						SourceType:          "dataSource",
						DataSourceName:      dsName,
						DataSourceNamespace: "uds-golden-images",
					},
				},
			},
		},
	}
}

func TestPoolReconciler_InitialScaleUp(t *testing.T) {
	ctx := context.Background()
	scheme := newPoolTestScheme(t)
	pool := makeTestPool("web-fleet", 2, "ubuntu-v1")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pool).
		WithStatusSubresource(&udsv1alpha1.UDSVirtualMachinePool{}, &udsv1alpha1.UDSVirtualMachine{}).
		Build()

	r := &PoolReconciler{Client: c, Scheme: scheme, Log: logr.Discard()}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "web-fleet", Namespace: "default"}}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	var vms udsv1alpha1.UDSVirtualMachineList
	if err := c.List(ctx, &vms, client.InNamespace("default")); err != nil {
		t.Fatalf("List VMs failed: %v", err)
	}
	if len(vms.Items) != 2 {
		t.Fatalf("expected 2 replicas created, got %d", len(vms.Items))
	}

	var updatedPool udsv1alpha1.UDSVirtualMachinePool
	if err := c.Get(ctx, req.NamespacedName, &updatedPool); err != nil {
		t.Fatalf("Get pool failed: %v", err)
	}
	if updatedPool.Status.Phase != "Provisioning" {
		t.Errorf("expected phase Provisioning, got %q", updatedPool.Status.Phase)
	}

	// Mark both VMs Ready and reconcile again.
	for i := range vms.Items {
		vms.Items[i].Status.Phase = "Ready"
		if err := c.Status().Update(ctx, &vms.Items[i]); err != nil {
			t.Fatalf("Update VM status failed: %v", err)
		}
	}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	if err := c.Get(ctx, req.NamespacedName, &updatedPool); err != nil {
		t.Fatalf("Get pool failed: %v", err)
	}
	if updatedPool.Status.Phase != "Ready" || updatedPool.Status.ReadyReplicas != 2 || updatedPool.Status.UpdatedReplicas != 2 {
		t.Errorf("expected Ready/2/2, got phase=%q ready=%d updated=%d",
			updatedPool.Status.Phase, updatedPool.Status.ReadyReplicas, updatedPool.Status.UpdatedReplicas)
	}
}

func TestPoolReconciler_RollingUpdate_MaxSurgeAndZeroUnavailable(t *testing.T) {
	ctx := context.Background()
	scheme := newPoolTestScheme(t)
	pool := makeTestPool("web-fleet", 2, "ubuntu-v1")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pool).
		WithStatusSubresource(&udsv1alpha1.UDSVirtualMachinePool{}, &udsv1alpha1.UDSVirtualMachine{}).
		Build()

	r := &PoolReconciler{Client: c, Scheme: scheme, Log: logr.Discard()}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "web-fleet", Namespace: "default"}}

	// Step 1: Initial creation & mark v1 replicas Ready.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("initial Reconcile failed: %v", err)
	}
	var vms udsv1alpha1.UDSVirtualMachineList
	if err := c.List(ctx, &vms); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	for i := range vms.Items {
		vms.Items[i].Status.Phase = "Ready"
		if err := c.Status().Update(ctx, &vms.Items[i]); err != nil {
			t.Fatalf("status update failed: %v", err)
		}
	}

	// Step 2: Update pool template to ubuntu-v2.
	if err := c.Get(ctx, req.NamespacedName, pool); err != nil {
		t.Fatalf("get pool failed: %v", err)
	}
	pool.Spec.Template.Spec.Disk.DataSourceName = "ubuntu-v2"
	if err := c.Update(ctx, pool); err != nil {
		t.Fatalf("update pool failed: %v", err)
	}

	// Reconcile: should surge 1 v2 VM (total 3) and keep both v1 VMs because v2 is not Ready yet.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("rollout Reconcile 1 failed: %v", err)
	}
	if err := c.List(ctx, &vms); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(vms.Items) != 3 {
		t.Fatalf("expected 3 VMs during maxSurge=1 rollout, got %d", len(vms.Items))
	}

	v2Rev, _ := ComputeTemplateHash(pool.Spec.Template.Spec)
	// Mark first v2 VM Ready.
	for i := range vms.Items {
		if vms.Items[i].Labels[revisionLabelKey] == v2Rev {
			vms.Items[i].Status.Phase = "Ready"
			if err := c.Status().Update(ctx, &vms.Items[i]); err != nil {
				t.Fatalf("update v2 status failed: %v", err)
			}
		}
	}

	// Reconcile: readyTotal=3 > minAvailable=2, so 1 v1 VM is drained and 2nd v2 VM is surged.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("rollout Reconcile 2 failed: %v", err)
	}
	if err := c.List(ctx, &vms); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	v2Count := 0
	for i := range vms.Items {
		if vms.Items[i].Labels[revisionLabelKey] == v2Rev {
			v2Count++
			vms.Items[i].Status.Phase = "Ready"
			if err := c.Status().Update(ctx, &vms.Items[i]); err != nil {
				t.Fatalf("update v2 status failed: %v", err)
			}
		}
	}
	if v2Count != 2 {
		t.Fatalf("expected 2 v2 VMs, got %d", v2Count)
	}

	// Final Reconcile: all v2 VMs Ready, last v1 VM drained -> Ready 2/2.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("rollout Reconcile 3 failed: %v", err)
	}
	if err := c.List(ctx, &vms); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(vms.Items) != 2 {
		t.Fatalf("expected 2 final VMs, got %d", len(vms.Items))
	}
	for _, vm := range vms.Items {
		if vm.Labels[revisionLabelKey] != v2Rev {
			t.Errorf("expected all remaining VMs on revision %s, found %s", v2Rev, vm.Labels[revisionLabelKey])
		}
	}

	var updatedPool udsv1alpha1.UDSVirtualMachinePool
	if err := c.Get(ctx, req.NamespacedName, &updatedPool); err != nil {
		t.Fatalf("get pool failed: %v", err)
	}
	if updatedPool.Status.Phase != "Ready" || updatedPool.Status.CurrentRevision != v2Rev {
		t.Errorf("expected Ready on %s, got phase=%q rev=%q", v2Rev, updatedPool.Status.Phase, updatedPool.Status.CurrentRevision)
	}
}

func TestPoolReconciler_ScaleDown(t *testing.T) {
	ctx := context.Background()
	scheme := newPoolTestScheme(t)
	pool := makeTestPool("web-fleet", 2, "ubuntu-v1")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pool).
		WithStatusSubresource(&udsv1alpha1.UDSVirtualMachinePool{}, &udsv1alpha1.UDSVirtualMachine{}).
		Build()

	r := &PoolReconciler{Client: c, Scheme: scheme, Log: logr.Discard()}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "web-fleet", Namespace: "default"}}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("initial Reconcile failed: %v", err)
	}

	if err := c.Get(ctx, req.NamespacedName, pool); err != nil {
		t.Fatalf("get pool failed: %v", err)
	}
	pool.Spec.Replicas = int32Ptr(1)
	if err := c.Update(ctx, pool); err != nil {
		t.Fatalf("update pool failed: %v", err)
	}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("scale-down Reconcile failed: %v", err)
	}

	var vms udsv1alpha1.UDSVirtualMachineList
	if err := c.List(ctx, &vms); err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(vms.Items) != 1 {
		t.Errorf("expected 1 VM after scale-down, got %d", len(vms.Items))
	}
}

func TestUDSVirtualMachinePool_DeepCopy(t *testing.T) {
	pool := makeTestPool("copy-test", 3, "ubuntu-v1")
	copied := pool.DeepCopy()
	*copied.Spec.Replicas = 99
	*copied.Spec.Strategy.RollingUpdate.MaxSurge = 5
	if *pool.Spec.Replicas != 3 || *pool.Spec.Strategy.RollingUpdate.MaxSurge != 1 {
		t.Errorf("DeepCopy mutated original pool spec")
	}
}
