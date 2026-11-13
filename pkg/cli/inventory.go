package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

// ListInventory fetches all UDSVirtualMachine instances and formats them as a clean CLI table to stdout.
func ListInventory(ctx context.Context, kubeconfig, namespace string) error {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return ListInventoryWithClient(ctx, dynClient, namespace, os.Stdout)
}

// ListInventoryWithClient queries UDSVirtualMachines via dynamic.Interface and writes the formatted table to out.
func ListInventoryWithClient(ctx context.Context, dynClient dynamic.Interface, namespace string, out io.Writer) error {
	var unstrList *unstructured.UnstructuredList
	var err error
	if namespace != "" {
		unstrList, err = dynClient.Resource(udsvmGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	} else {
		unstrList, err = dynClient.Resource(udsvmGVR).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return fmt.Errorf("failed to query UDSVirtualMachine resources: %w", err)
	}

	w := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAMESPACE\tNAME\tPHASE\tOS\tPVC-SIZE\tCHECKSUM-VERIFIED\tZERO-TRUST\tRECONCILER\tAGE")

	for _, item := range unstrList.Items {
		ns := item.GetNamespace()
		name := item.GetName()
		creationTime := item.GetCreationTimestamp()
		age := time.Since(creationTime.Time).Round(time.Second)

		spec, _ := item.Object["spec"].(map[string]interface{})
		osType, _ := spec["osType"].(string)
		if osType == "" {
			osType = "linux"
		}

		status, _ := item.Object["status"].(map[string]interface{})
		phase, _ := status["phase"].(string)
		if phase == "" {
			phase = "Pending"
		}
		pvcSize, _ := status["calculatedPvcSize"].(string)
		if pvcSize == "" {
			pvcSize = "-"
		}
		reconciledBy, _ := status["reconciledBy"].(string)
		if reconciledBy == "" {
			reconciledBy = "-"
		}

		compliance, _ := status["compliance"].(map[string]interface{})
		checksumVerified := "false"
		if cv, ok := compliance["checksumVerified"].(bool); ok && cv {
			checksumVerified = "true"
		}
		zeroTrust := "false"
		if zt, ok := compliance["zeroTrustIsolated"].(bool); ok && zt {
			zeroTrust = "true"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ns, name, phase, osType, pvcSize, checksumVerified, zeroTrust, reconciledBy, age)
	}

	return w.Flush()
}
