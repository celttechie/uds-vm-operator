package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	udsv1alpha1 "github.com/celttechie/uds-vm-operator/pkg/apis/uds/v1alpha1"
)

func getSampleUDSVM() *udsv1alpha1.UDSVirtualMachine {
	trueVal := true
	return &udsv1alpha1.UDSVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tactical-sensor-vm",
			Namespace: "default",
			UID:       "11111111-2222-3333-4444-555555555555",
		},
		Spec: udsv1alpha1.UDSVirtualMachineSpec{
			CPUs:        2,
			Memory:      "1Gi",
			RunStrategy: "Halted",
			Disk: udsv1alpha1.DiskSpec{
				SourceType:       "upload",
				Format:           "raw",
				VirtualSizeBytes: 10485760, // 10 MiB
				Checksum:         "sha256:e5b844cc57f57094ea4585e235f36c78c1cd222262bb89d53c94dcb4d6b3e55d",
			},
			SecurityProfile: udsv1alpha1.SecurityProfileSpec{
				ZeroTrustNetwork:    &trueVal,
				DisallowSSHPassword: &trueVal,
				FIPSMode:            &trueVal,
			},
		},
	}
}

func TestCalculatePVCSizeMiB(t *testing.T) {
	// 10 MiB * 1.06 = 10.6 MiB -> ceil is 11Mi
	if got := CalculatePVCSizeMiB(10 * 1024 * 1024); got != "11Mi" {
		t.Errorf("CalculatePVCSizeMiB(10MiB) = %s; want 11Mi", got)
	}
	// 1 GiB (1024 MiB) * 1.06 = 1085.44 MiB -> ceil is 1086Mi
	if got := CalculatePVCSizeMiB(1024 * 1024 * 1024); got != "1086Mi" {
		t.Errorf("CalculatePVCSizeMiB(1GiB) = %s; want 1086Mi", got)
	}
}

func TestBuildZeroTrustNetworkPolicy(t *testing.T) {
	vm := getSampleUDSVM()
	netpol := BuildZeroTrustNetworkPolicy(vm)

	if netpol.Name != "tactical-sensor-vm-netpol" {
		t.Errorf("expected name tactical-sensor-vm-netpol, got %s", netpol.Name)
	}
	if netpol.Labels["uds.dev/nist-control"] != "SC-7" {
		t.Errorf("expected NIST label SC-7, got %s", netpol.Labels["uds.dev/nist-control"])
	}
	if netpol.Spec.PodSelector.MatchLabels["vm.kubevirt.io/name"] != "tactical-sensor-vm" {
		t.Errorf("expected pod selector vm.kubevirt.io/name=tactical-sensor-vm, got %v", netpol.Spec.PodSelector.MatchLabels)
	}
	if len(netpol.Spec.Ingress) != 0 {
		t.Errorf("expected 0 ingress rules, got %d", len(netpol.Spec.Ingress))
	}
	if len(netpol.Spec.Egress) != 1 {
		t.Fatalf("expected 1 egress rule, got %d", len(netpol.Spec.Egress))
	}
	if len(netpol.Spec.Egress[0].Ports) != 2 {
		t.Errorf("expected 2 DNS egress ports (UDP & TCP 53), got %d", len(netpol.Spec.Egress[0].Ports))
	}
}

func TestBuildCloudInitSecret(t *testing.T) {
	vm := getSampleUDSVM()
	secret := BuildCloudInitSecret(vm)

	if secret.Name != "tactical-sensor-vm-cloudinit" {
		t.Errorf("expected name tactical-sensor-vm-cloudinit, got %s", secret.Name)
	}
	if secret.Labels["uds.dev/nist-control"] != "AC-3" {
		t.Errorf("expected NIST label AC-3, got %s", secret.Labels["uds.dev/nist-control"])
	}
	userdata := secret.StringData["userdata"]
	if !strings.Contains(userdata, "ssh_pwauth: false") {
		t.Errorf("expected ssh_pwauth: false in userdata, got:\n%s", userdata)
	}
	if !strings.Contains(userdata, "disable_root: true") {
		t.Errorf("expected disable_root: true in userdata, got:\n%s", userdata)
	}
	if !strings.Contains(userdata, "FIPS profile active") {
		t.Errorf("expected FIPS profile active in userdata, got:\n%s", userdata)
	}
}

func TestBuildDataVolumeUploadChecksum(t *testing.T) {
	vm := getSampleUDSVM()
	dv := BuildDataVolume(vm)

	if dv.GetName() != "tactical-sensor-vm-dv" {
		t.Errorf("expected dv name tactical-sensor-vm-dv, got %s", dv.GetName())
	}
	labels := dv.GetLabels()
	if labels["uds.dev/nist-control"] != "SI-7" {
		t.Errorf("expected NIST label SI-7, got %s", labels["uds.dev/nist-control"])
	}

	spec, ok := dv.Object["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("spec not found in DataVolume object")
	}
	source, ok := spec["source"].(map[string]interface{})
	if !ok {
		t.Fatalf("source not found in DataVolume spec")
	}
	upload, ok := source["upload"].(map[string]interface{})
	if !ok {
		t.Fatalf("upload not found in DataVolume source")
	}
	if upload["checksum"] != vm.Spec.Disk.Checksum {
		t.Errorf("expected checksum %s, got %v", vm.Spec.Disk.Checksum, upload["checksum"])
	}

	storage, ok := spec["storage"].(map[string]interface{})
	if !ok {
		t.Fatalf("storage not found in DataVolume spec")
	}
	res, ok := storage["resources"].(map[string]interface{})
	if !ok {
		t.Fatalf("resources not found in storage")
	}
	req, ok := res["requests"].(map[string]interface{})
	if !ok {
		t.Fatalf("requests not found in resources")
	}
	if req["storage"] != "11Mi" {
		t.Errorf("expected storage request 11Mi, got %v", req["storage"])
	}
}

func TestBuildVirtualMachine(t *testing.T) {
	vm := getSampleUDSVM()
	vmObj := BuildVirtualMachine(vm)

	if vmObj.GetName() != "tactical-sensor-vm" {
		t.Errorf("expected vm name tactical-sensor-vm, got %s", vmObj.GetName())
	}

	spec, ok := vmObj.Object["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("spec not found in VirtualMachine object")
	}
	if spec["runStrategy"] != "Halted" {
		t.Errorf("expected runStrategy Halted, got %v", spec["runStrategy"])
	}

	template, ok := spec["template"].(map[string]interface{})
	if !ok {
		t.Fatalf("template not found in spec")
	}
	tmplSpec, ok := template["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("spec not found in template")
	}
	volumes, ok := tmplSpec["volumes"].([]interface{})
	if !ok || len(volumes) < 2 {
		t.Fatalf("expected at least 2 volumes in template spec, got %v", volumes)
	}

	vol0 := volumes[0].(map[string]interface{})
	dvVol := vol0["dataVolume"].(map[string]interface{})
	if dvVol["name"] != "tactical-sensor-vm-dv" {
		t.Errorf("expected rootdisk to point to tactical-sensor-vm-dv, got %v", dvVol["name"])
	}

	vol1 := volumes[1].(map[string]interface{})
	ciVol := vol1["cloudInitNoCloud"].(map[string]interface{})
	secRef := ciVol["secretRef"].(map[string]interface{})
	if secRef["name"] != "tactical-sensor-vm-cloudinit" {
		t.Errorf("expected cloudinitdisk to point to tactical-sensor-vm-cloudinit, got %v", secRef["name"])
	}
}

func TestEvaluateStatusLifecycle(t *testing.T) {
	vm := getSampleUDSVM()

	// 1. UploadReady state
	dvUploadReady := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "tactical-sensor-vm-dv",
			},
			"status": map[string]interface{}{
				"phase":      "UploadReady",
				"conditions": []interface{}{},
			},
		},
	}
	st1 := EvaluateStatusFromDataVolume(vm, dvUploadReady, "controller-go")
	if st1.Phase != "WaitingForUpload" {
		t.Errorf("expected phase WaitingForUpload, got %s", st1.Phase)
	}
	if st1.Compliance.ChecksumVerified != false {
		t.Errorf("expected ChecksumVerified false, got %v", st1.Compliance.ChecksumVerified)
	}
	if st1.ReconciledBy != "controller-go" {
		t.Errorf("expected reconciledBy controller-go, got %s", st1.ReconciledBy)
	}

	// 2. ChecksumError state (leveraging CDI PR #4340)
	dvChecksumErr := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "tactical-sensor-vm-dv",
			},
			"status": map[string]interface{}{
				"phase": "UploadReady",
				"conditions": []interface{}{
					map[string]interface{}{
						"type":    "Running",
						"status":  "False",
						"reason":  "ChecksumError",
						"message": "checksum mismatch: expected e5b8..., got 9999...",
					},
				},
			},
		},
	}
	st2 := EvaluateStatusFromDataVolume(vm, dvChecksumErr, "controller-go")
	if st2.Phase != "IntegrityError" {
		t.Errorf("expected phase IntegrityError, got %s", st2.Phase)
	}
	if st2.Compliance.ChecksumVerified != false {
		t.Errorf("expected ChecksumVerified false, got %v", st2.Compliance.ChecksumVerified)
	}
	if len(st2.Conditions) == 0 || !strings.Contains(st2.Conditions[0].Message, "checksum mismatch") {
		t.Errorf("expected checksum mismatch condition message, got %v", st2.Conditions)
	}

	// 3. Succeeded state
	dvSucceeded := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "tactical-sensor-vm-dv",
			},
			"status": map[string]interface{}{
				"phase":      "Succeeded",
				"conditions": []interface{}{},
			},
		},
	}
	st3 := EvaluateStatusFromDataVolume(vm, dvSucceeded, "controller-go")
	if st3.Phase != "Ready" {
		t.Errorf("expected phase Ready, got %s", st3.Phase)
	}
	if st3.Compliance.ChecksumVerified != true {
		t.Errorf("expected ChecksumVerified true, got %v", st3.Compliance.ChecksumVerified)
	}
	if st3.Compliance.ZeroTrustIsolated != true {
		t.Errorf("expected ZeroTrustIsolated true, got %v", st3.Compliance.ZeroTrustIsolated)
	}
}
