package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newInventoryTestVM(name, namespace, osType, phase, pvcSize, reconciler string, checksumVerified, zeroTrust bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "uds.dev/v1alpha1",
			"kind":       "UDSVirtualMachine",
			"metadata": map[string]interface{}{
				"name":              name,
				"namespace":         namespace,
				"creationTimestamp": metav1.Time{Time: time.Now().Add(-5 * time.Minute)}.Format(time.RFC3339),
			},
			"spec": map[string]interface{}{
				"osType":      osType,
				"runStrategy": "Always",
			},
			"status": map[string]interface{}{
				"phase":             phase,
				"calculatedPvcSize": pvcSize,
				"reconciledBy":      reconciler,
				"compliance": map[string]interface{}{
					"checksumVerified":  checksumVerified,
					"zeroTrustIsolated": zeroTrust,
				},
			},
		},
	}
}

func newFakeDynamicClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		udsvmGVR: "UDSVirtualMachineList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objects...)
}

func TestListInventoryWithClient_MultipleVMs(t *testing.T) {
	vm1 := newInventoryTestVM("tactical-sensor-vm", "default", "linux", "Ready", "11Mi", "controller-go", true, true)
	vm2 := newInventoryTestVM("tactical-windows-vm", "uds-system", "windows", "Provisioning", "5100Mi", "controller-go", false, true)

	client := newFakeDynamicClient(vm1, vm2)
	ctx := context.Background()

	var buf bytes.Buffer
	err := ListInventoryWithClient(ctx, client, "", &buf)
	if err != nil {
		t.Fatalf("unexpected error listing inventory: %v", err)
	}

	out := buf.String()
	// Assert table header
	if !strings.Contains(out, "NAMESPACE") || !strings.Contains(out, "SOURCE") || !strings.Contains(out, "PVC-SIZE") || !strings.Contains(out, "CHECKSUM-VERIFIED") {
		t.Errorf("output missing expected table headers: %s", out)
	}

	// Assert vm1 row
	if !strings.Contains(out, "tactical-sensor-vm") || !strings.Contains(out, "upload") || !strings.Contains(out, "11Mi") || !strings.Contains(out, "Ready") {
		t.Errorf("output missing tactical-sensor-vm details: %s", out)
	}

	// Assert vm2 row
	if !strings.Contains(out, "tactical-windows-vm") || !strings.Contains(out, "windows") || !strings.Contains(out, "Provisioning") {
		t.Errorf("output missing tactical-windows-vm details: %s", out)
	}
}

func TestListInventoryWithClient_FilteredNamespace(t *testing.T) {
	vm1 := newInventoryTestVM("vm-in-default", "default", "linux", "Ready", "11Mi", "controller-go", true, true)
	vm2 := newInventoryTestVM("vm-in-other", "other-ns", "linux", "Ready", "11Mi", "controller-go", true, true)

	client := newFakeDynamicClient(vm1, vm2)
	ctx := context.Background()

	var buf bytes.Buffer
	err := ListInventoryWithClient(ctx, client, "default", &buf)
	if err != nil {
		t.Fatalf("unexpected error listing inventory: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "vm-in-default") {
		t.Errorf("expected vm-in-default to be listed: %s", out)
	}
	if strings.Contains(out, "vm-in-other") {
		t.Errorf("did not expect vm-in-other to be listed when filtering namespace 'default': %s", out)
	}
}

func TestListInventoryWithClient_Empty(t *testing.T) {
	client := newFakeDynamicClient()
	ctx := context.Background()

	var buf bytes.Buffer
	err := ListInventoryWithClient(ctx, client, "", &buf)
	if err != nil {
		t.Fatalf("unexpected error on empty inventory: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "NAMESPACE") {
		t.Errorf("expected headers even on empty list, got: %s", out)
	}
}
