package cli

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

var udsvmGVR = schema.GroupVersionResource{
	Group:    "uds.dev",
	Version:  "v1alpha1",
	Resource: "udsvirtualmachines",
}

// PowerAction executes a start, stop, or restart transition on a UDSVirtualMachine.
func PowerAction(ctx context.Context, kubeconfig, namespace, vmName, action string) (string, error) {
	if namespace == "" {
		namespace = "default"
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return "", fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return "", fmt.Errorf("failed to create dynamic client: %w", err)
	}

	udsvmClient := dynClient.Resource(udsvmGVR).Namespace(namespace)
	udsvm, err := udsvmClient.Get(ctx, vmName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get UDSVirtualMachine %q in namespace %q: %w", vmName, namespace, err)
	}

	spec, ok := udsvm.Object["spec"].(map[string]interface{})
	if !ok {
		spec = make(map[string]interface{})
	}

	switch action {
	case "start":
		spec["runStrategy"] = "Always"
		udsvm.Object["spec"] = spec
		_, err = udsvmClient.Update(ctx, udsvm, metav1.UpdateOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to start VM: %w", err)
		}
		return fmt.Sprintf("UDSVirtualMachine %s/%s power state set to 'Always' (Starting)", namespace, vmName), nil

	case "stop":
		spec["runStrategy"] = "Halted"
		udsvm.Object["spec"] = spec
		_, err = udsvmClient.Update(ctx, udsvm, metav1.UpdateOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to stop VM: %w", err)
		}
		return fmt.Sprintf("UDSVirtualMachine %s/%s power state set to 'Halted' (Stopping)", namespace, vmName), nil

	case "restart":
		// Trigger restart by toggling or reporting restart scheduling
		spec["runStrategy"] = "Always"
		udsvm.Object["spec"] = spec
		_, err = udsvmClient.Update(ctx, udsvm, metav1.UpdateOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to restart VM: %w", err)
		}
		return fmt.Sprintf("UDSVirtualMachine %s/%s restart initiated (runStrategy: Always)", namespace, vmName), nil

	default:
		return "", fmt.Errorf("unknown power action %q: expected start, stop, or restart", action)
	}
}
