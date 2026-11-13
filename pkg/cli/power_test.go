package cli

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newSampleUnstructuredVM(name, namespace, runStrategy string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "uds.dev/v1alpha1",
			"kind":       "UDSVirtualMachine",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"runStrategy": runStrategy,
			},
		},
	}
}

func TestPowerActionWithClient_Start(t *testing.T) {
	scheme := runtime.NewScheme()
	initialVM := newSampleUnstructuredVM("test-vm", "default", "Halted")

	client := dynamicfake.NewSimpleDynamicClient(scheme, initialVM)
	ctx := context.Background()

	msg, err := PowerActionWithClient(ctx, client, "default", "test-vm", "start")
	if err != nil {
		t.Fatalf("unexpected error starting VM: %v", err)
	}
	if !strings.Contains(msg, "Always") {
		t.Errorf("expected message to mention 'Always', got: %s", msg)
	}

	updated, err := client.Resource(udsvmGVR).Namespace("default").Get(ctx, "test-vm", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to retrieve updated VM: %v", err)
	}

	spec := updated.Object["spec"].(map[string]interface{})
	if spec["runStrategy"] != "Always" {
		t.Errorf("expected runStrategy 'Always', got: %v", spec["runStrategy"])
	}
}

func TestPowerActionWithClient_Stop(t *testing.T) {
	scheme := runtime.NewScheme()
	initialVM := newSampleUnstructuredVM("test-vm", "default", "Always")

	client := dynamicfake.NewSimpleDynamicClient(scheme, initialVM)
	ctx := context.Background()

	msg, err := PowerActionWithClient(ctx, client, "default", "test-vm", "stop")
	if err != nil {
		t.Fatalf("unexpected error stopping VM: %v", err)
	}
	if !strings.Contains(msg, "Halted") {
		t.Errorf("expected message to mention 'Halted', got: %s", msg)
	}

	updated, err := client.Resource(udsvmGVR).Namespace("default").Get(ctx, "test-vm", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to retrieve updated VM: %v", err)
	}

	spec := updated.Object["spec"].(map[string]interface{})
	if spec["runStrategy"] != "Halted" {
		t.Errorf("expected runStrategy 'Halted', got: %v", spec["runStrategy"])
	}
}

func TestPowerActionWithClient_Restart(t *testing.T) {
	scheme := runtime.NewScheme()
	initialVM := newSampleUnstructuredVM("test-vm", "default", "Always")

	client := dynamicfake.NewSimpleDynamicClient(scheme, initialVM)
	ctx := context.Background()

	msg, err := PowerActionWithClient(ctx, client, "default", "test-vm", "restart")
	if err != nil {
		t.Fatalf("unexpected error restarting VM: %v", err)
	}
	if !strings.Contains(msg, "restart initiated") {
		t.Errorf("expected message to mention 'restart initiated', got: %s", msg)
	}
}

func TestPowerActionWithClient_UnknownAction(t *testing.T) {
	scheme := runtime.NewScheme()
	initialVM := newSampleUnstructuredVM("test-vm", "default", "Always")

	client := dynamicfake.NewSimpleDynamicClient(scheme, initialVM)
	ctx := context.Background()

	_, err := PowerActionWithClient(ctx, client, "default", "test-vm", "hibernate")
	if err == nil {
		t.Fatal("expected error for unknown action, got nil")
	}
	if !strings.Contains(err.Error(), "unknown power action") {
		t.Errorf("expected error message to mention unknown power action, got: %v", err)
	}
}

func TestPowerActionWithClient_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	client := dynamicfake.NewSimpleDynamicClient(scheme)
	ctx := context.Background()

	_, err := PowerActionWithClient(ctx, client, "default", "missing-vm", "start")
	if err == nil {
		t.Fatal("expected error for non-existent VM, got nil")
	}
}
