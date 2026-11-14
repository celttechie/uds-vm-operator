package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

func getSampleWindowsUDSVM() *udsv1alpha1.UDSVirtualMachine {
	trueVal := true
	return &udsv1alpha1.UDSVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tactical-windows-vm",
			Namespace: "default",
			UID:       "22222222-3333-4444-5555-666666666666",
		},
		Spec: udsv1alpha1.UDSVirtualMachineSpec{
			CPUs:        4,
			Memory:      "8Gi",
			RunStrategy: "Always",
			OSType:      "windows",
			WindowsProfile: &udsv1alpha1.WindowsProfileSpec{
				DiskBus:                    "virtio",
				NICModel:                   "virtio",
				InjectVirtioWinISO:         &trueVal,
				EnableHyperVEnlightenments: &trueVal,
				EnableSecureBootTPM:        &trueVal,
			},
			Disk: udsv1alpha1.DiskSpec{
				SourceType:       "upload",
				Format:           "raw",
				VirtualSizeBytes: 5347737600, // 5100 MiB
				Checksum:         "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			},
			SecurityProfile: udsv1alpha1.SecurityProfileSpec{
				ZeroTrustNetwork: &trueVal,
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

func TestBuildWindowsVirtualMachineAndSysprep(t *testing.T) {
	trueVal := true
	winVM := &udsv1alpha1.UDSVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tactical-windows-vm",
			Namespace: "default",
			UID:       "99999999-8888-7777-6666-555555555555",
		},
		Spec: udsv1alpha1.UDSVirtualMachineSpec{
			CPUs:        4,
			Memory:      "4Gi",
			RunStrategy: "Halted",
			OSType:      "windows",
			Disk: udsv1alpha1.DiskSpec{
				SourceType:       "upload",
				Format:           "raw",
				VirtualSizeBytes: 21474836480, // 20 GiB
				Checksum:         "sha256:111122223333444455556666777788889999aaaabbbbccccddddeeeeffff0000",
			},
			WindowsProfile: &udsv1alpha1.WindowsProfileSpec{
				DiskBus:                   "virtio",
				NICModel:                  "virtio",
				InjectVirtioWinISO:        &trueVal,
				EnableHyperVEnlightenments: &trueVal,
				EnableSecureBootTPM:       &trueVal,
				PersistentTPM:             &trueVal,
			},
		},
	}

	// 1. Verify Secret generation
	sec := BuildCloudInitSecret(winVM)
	if sec.Name != "tactical-windows-vm-cloudinit" {
		t.Errorf("expected secret name tactical-windows-vm-cloudinit, got %s", sec.Name)
	}
	autounattend, ok := sec.StringData["autounattend.xml"]
	if !ok || !strings.Contains(autounattend, "<ComputerName>TACTICAL-WINDOW</ComputerName>") {
		t.Errorf("expected autounattend.xml with ComputerName, got:\n%s", autounattend)
	}
	if !strings.Contains(autounattend, "<AdministratorPassword>") {
		t.Errorf("expected AdministratorPassword block in autounattend.xml")
	}

	// 2. Verify VirtualMachine object generation
	vmObj := BuildVirtualMachine(winVM)
	spec, ok := vmObj.Object["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("spec not found in VM")
	}
	tmpl, ok := spec["template"].(map[string]interface{})
	if !ok {
		t.Fatalf("template not found in spec")
	}
	tmplSpec, ok := tmpl["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("template spec not found")
	}
	domain, ok := tmplSpec["domain"].(map[string]interface{})
	if !ok {
		t.Fatalf("domain not found")
	}

	// Hyper-V features
	features, ok := domain["features"].(map[string]interface{})
	if !ok {
		t.Fatalf("features not found")
	}
	hyperv, ok := features["hyperv"].(map[string]interface{})
	if !ok {
		t.Fatalf("hyperv features not found")
	}
	if _, ok := hyperv["relaxed"]; !ok {
		t.Errorf("expected hyperv.relaxed feature")
	}
	if _, ok := hyperv["vapic"]; !ok {
		t.Errorf("expected hyperv.vapic feature")
	}
	if _, ok := hyperv["spinlocks"]; !ok {
		t.Errorf("expected hyperv.spinlocks feature")
	}

	// Clock timers
	clock, ok := domain["clock"].(map[string]interface{})
	if !ok {
		t.Fatalf("clock not found")
	}
	timers, ok := clock["timer"].(map[string]interface{})
	if !ok || timers["hyperv"] == nil {
		t.Errorf("expected clock.timer.hyperv")
	}

	// UEFI SecureBoot and vTPM
	firmware, ok := domain["firmware"].(map[string]interface{})
	if !ok {
		t.Fatalf("firmware not found")
	}
	bootloader, ok := firmware["bootloader"].(map[string]interface{})
	if !ok {
		t.Fatalf("bootloader not found")
	}
	efi, ok := bootloader["efi"].(map[string]interface{})
	if !ok || efi["secureBoot"] != true {
		t.Errorf("expected efi.secureBoot=true, got %v", efi)
	}

	devices, ok := domain["devices"].(map[string]interface{})
	if !ok {
		t.Fatalf("devices not found")
	}
	tpm, ok := devices["tpm"].(map[string]interface{})
	if !ok || tpm["persistent"] != true {
		t.Errorf("expected persistent tpm when PersistentTPM=true, got %v", tpm)
	}

	// Disks and Volumes: rootdisk, sysprepdisk, virtio-win-drivers
	disks, ok := devices["disks"].([]interface{})
	if !ok || len(disks) != 3 {
		t.Fatalf("expected 3 disks (root, sysprep, drivers), got %d", len(disks))
	}
	volumes, ok := tmplSpec["volumes"].([]interface{})
	if !ok || len(volumes) != 3 {
		t.Fatalf("expected 3 volumes (root, sysprep, drivers), got %d", len(volumes))
	}

	sysprepVol := volumes[1].(map[string]interface{})
	if sysprepVol["name"] != "sysprepdisk" {
		t.Errorf("expected volume 1 to be sysprepdisk, got %v", sysprepVol["name"])
	}
	driverVol := volumes[2].(map[string]interface{})
	if driverVol["name"] != "virtio-win-drivers" {
		t.Errorf("expected volume 2 to be virtio-win-drivers, got %v", driverVol["name"])
	}
}

func TestWindowsSysprepAndCredentials(t *testing.T) {
	// 1. Test GenerateSecurePassword meeting DISA STIG complexity
	pw, err := GenerateSecurePassword(24)
	if err != nil {
		t.Fatalf("GenerateSecurePassword failed: %v", err)
	}
	if len(pw) != 24 {
		t.Errorf("expected password length 24, got %d", len(pw))
	}
	var hasUpper, hasLower, hasDigit, hasSymbol bool
	for _, r := range pw {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		case strings.ContainsRune("!@#$%^&*-_=+", r):
			hasSymbol = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit || !hasSymbol {
		t.Errorf("password %q failed DISA STIG complexity (upper=%v, lower=%v, digit=%v, symbol=%v)",
			pw, hasUpper, hasLower, hasDigit, hasSymbol)
	}

	// 2. Test Custom SysprepSecretRef in BuildVirtualMachine
	customVM := &udsv1alpha1.UDSVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-win-vm",
			Namespace: "default",
		},
		Spec: udsv1alpha1.UDSVirtualMachineSpec{
			OSType: "windows",
			WindowsProfile: &udsv1alpha1.WindowsProfileSpec{
				SysprepSecretRef: "enterprise-custom-unattend-secret",
			},
			Disk: udsv1alpha1.DiskSpec{
				SourceType:       "upload",
				Checksum:         "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				VirtualSizeBytes: 10 * 1024 * 1024,
			},
		},
	}
	vmObj := BuildVirtualMachine(customVM)
	tmplSpec := vmObj.Object["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})
	volumes := tmplSpec["volumes"].([]interface{})
	sysprepVol := volumes[1].(map[string]interface{})
	sysprepSecret := sysprepVol["sysprep"].(map[string]interface{})["secret"].(map[string]interface{})["name"]
	if sysprepSecret != "enterprise-custom-unattend-secret" {
		t.Errorf("expected custom sysprep secret enterprise-custom-unattend-secret, got %v", sysprepSecret)
	}

	// 3. Test BuildCloudInitSecret with custom password
	sec := BuildCloudInitSecret(customVM, "SecretP@ssw0rd!2026")
	xmlContent := sec.StringData["autounattend.xml"]
	if !strings.Contains(xmlContent, "<Value>SecretP@ssw0rd!2026</Value>") {
		t.Errorf("expected injected password in autounattend.xml")
	}
	if !strings.Contains(xmlContent, "<Path>F:\\amd64\\2k22</Path>") {
		t.Errorf("expected VirtIO driver search path in autounattend.xml")
	}
	if !strings.Contains(xmlContent, "qemu-ga-x86_64.msi") {
		t.Errorf("expected QEMU guest agent install in FirstLogonCommands")
	}
}

func TestWindowsISOInstallationDiskAttachment(t *testing.T) {
	isoVM := &udsv1alpha1.UDSVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tactical-iso-win-vm",
			Namespace: "default",
		},
		Spec: udsv1alpha1.UDSVirtualMachineSpec{
			OSType: "windows",
			Disk: udsv1alpha1.DiskSpec{
				SourceType: "upload",
				Format:     "iso",
			},
			WindowsProfile: &udsv1alpha1.WindowsProfileSpec{
				DiskBus: "virtio",
			},
		},
	}
	vmObj := BuildVirtualMachine(isoVM)
	tmplSpec := vmObj.Object["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})
	domain := tmplSpec["domain"].(map[string]interface{})
	devices := domain["devices"].(map[string]interface{})
	disks := devices["disks"].([]interface{})
	volumes := tmplSpec["volumes"].([]interface{})

	var foundInstallDisk, foundInstallVol bool
	for _, d := range disks {
		dm := d.(map[string]interface{})
		if dm["name"] == "installdisk" {
			foundInstallDisk = true
			diskMap := dm["disk"].(map[string]interface{})
			if diskMap["bus"] != "virtio" {
				t.Errorf("expected installdisk bus virtio, got %v", diskMap["bus"])
			}
		}
	}
	for _, v := range volumes {
		vm := v.(map[string]interface{})
		if vm["name"] == "installdisk" {
			foundInstallVol = true
			emptyDisk := vm["emptyDisk"].(map[string]interface{})
			if emptyDisk["capacity"] != "40Gi" {
				t.Errorf("expected emptyDisk capacity 40Gi, got %v", emptyDisk["capacity"])
			}
		}
	}
	if !foundInstallDisk {
		t.Errorf("expected installdisk in disks")
	}
	if !foundInstallVol {
		t.Errorf("expected installdisk in volumes")
	}
}

func TestPrometheusComplianceMetrics(t *testing.T) {
	RecordComplianceMetrics("test-tactical-vm", "default", true, true, true, false)
	RecordPhaseMetrics("test-tactical-vm", "default", "Ready")
	RecordStorageMetrics("test-tactical-vm", "default", 10*1024*1024, 11*1024*1024)

	metricFamilies, err := crmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics from registry: %v", err)
	}

	var foundCompliance, foundPhase, foundStorage bool
	for _, mf := range metricFamilies {
		switch mf.GetName() {
		case "uds_vm_compliance_verified":
			foundCompliance = true
			for _, m := range mf.GetMetric() {
				if m.GetGauge().GetValue() != 1.0 {
					t.Errorf("expected compliance gauge 1.0, got %f", m.GetGauge().GetValue())
				}
			}
		case "uds_vm_phase_status":
			foundPhase = true
		case "uds_vm_storage_overhead_ratio":
			foundStorage = true
		}
	}

	if !foundCompliance {
		t.Errorf("expected uds_vm_compliance_verified metric family in registry")
	}
	if !foundPhase {
		t.Errorf("expected uds_vm_phase_status metric family in registry")
	}
	if !foundStorage {
		t.Errorf("expected uds_vm_storage_overhead_ratio metric family in registry")
	}
}

func TestCalculatePVCSizeMiB_EdgeCases(t *testing.T) {
	// Zero bytes should return 1Mi floor
	if got := CalculatePVCSizeMiB(0); got != "1Mi" {
		t.Errorf("CalculatePVCSizeMiB(0) = %s, want 1Mi", got)
	}

	// Negative bytes should return 1Mi floor
	if got := CalculatePVCSizeMiB(-100); got != "1Mi" {
		t.Errorf("CalculatePVCSizeMiB(-100) = %s, want 1Mi", got)
	}

	// 1 byte should return 1Mi
	if got := CalculatePVCSizeMiB(1); got != "1Mi" {
		t.Errorf("CalculatePVCSizeMiB(1) = %s, want 1Mi", got)
	}

	// 100 MiB: 100 * 1.06 = 106Mi
	if got := CalculatePVCSizeMiB(100 * 1024 * 1024); got != "106Mi" {
		t.Errorf("CalculatePVCSizeMiB(100MiB) = %s, want 106Mi", got)
	}
}

func newTestReconcilerScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = udsv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = networkingv1.AddToScheme(s)
	return s
}

func TestReconciler_Reconcile_NotFound(t *testing.T) {
	s := newTestReconcilerScheme()
	client := fake.NewClientBuilder().WithScheme(s).Build()

	r := &Reconciler{
		Client:       client,
		Scheme:       s,
		ReconcilerID: "controller-go-test",
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "nonexistent-vm",
			Namespace: "default",
		},
	}

	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error reconciling non-existent VM: %v", err)
	}
	if res.Requeue || res.RequeueAfter != 0 {
		t.Errorf("expected no requeue on not-found, got: %v", res)
	}
}

func TestReconciler_Reconcile_LinuxVM(t *testing.T) {
	s := newTestReconcilerScheme()
	udsvm := getSampleUDSVM()

	client := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(udsvm).
		WithStatusSubresource(udsvm).
		Build()

	r := &Reconciler{
		Client:       client,
		Scheme:       s,
		ReconcilerID: "controller-go-test",
	}

	ctx := context.Background()
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      udsvm.Name,
			Namespace: udsvm.Namespace,
		},
	}

	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error reconciling Linux VM: %v", err)
	}
	if !res.Requeue && res.RequeueAfter == 0 {
		// Because DataVolume is in WaitingForUpload, reconciler queues periodic check
	}

	// 1. Verify NetworkPolicy was created
	netpol := &networkingv1.NetworkPolicy{}
	err = client.Get(ctx, types.NamespacedName{Name: udsvm.Name + "-netpol", Namespace: udsvm.Namespace}, netpol)
	if err != nil {
		t.Fatalf("expected NetworkPolicy to be created: %v", err)
	}
	if netpol.Labels["uds.dev/nist-control"] != "SC-7" {
		t.Errorf("expected SC-7 label, got %s", netpol.Labels["uds.dev/nist-control"])
	}

	// 2. Verify Cloud-Init Secret was created
	secret := &corev1.Secret{}
	err = client.Get(ctx, types.NamespacedName{Name: udsvm.Name + "-cloudinit", Namespace: udsvm.Namespace}, secret)
	if err != nil {
		t.Fatalf("expected Cloud-Init secret to be created: %v", err)
	}
	if len(secret.Data["userdata"]) == 0 && len(secret.StringData["userdata"]) == 0 {
		t.Errorf("expected userdata in secret Data or StringData")
	}

	// 3. Verify DataVolume was created
	dv := &unstructured.Unstructured{}
	dv.SetAPIVersion("cdi.kubevirt.io/v1beta1")
	dv.SetKind("DataVolume")
	err = client.Get(ctx, types.NamespacedName{Name: udsvm.Name + "-dv", Namespace: udsvm.Namespace}, dv)
	if err != nil {
		t.Fatalf("expected DataVolume to be created: %v", err)
	}

	// 4. Verify VirtualMachine was created
	vm := &unstructured.Unstructured{}
	vm.SetAPIVersion("kubevirt.io/v1")
	vm.SetKind("VirtualMachine")
	err = client.Get(ctx, types.NamespacedName{Name: udsvm.Name, Namespace: udsvm.Namespace}, vm)
	if err != nil {
		t.Fatalf("expected VirtualMachine to be created: %v", err)
	}

	// 5. Test idempotence: second reconcile should update cleanly without error
	_, err = r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error during second idempotent reconcile: %v", err)
	}
}

func TestReconciler_Reconcile_WindowsVM(t *testing.T) {
	s := newTestReconcilerScheme()
	udsvm := getSampleWindowsUDSVM()

	client := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(udsvm).
		WithStatusSubresource(udsvm).
		Build()

	r := &Reconciler{
		Client:       client,
		Scheme:       s,
		ReconcilerID: "controller-go-test",
	}

	ctx := context.Background()
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      udsvm.Name,
			Namespace: udsvm.Namespace,
		},
	}

	_, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error reconciling Windows VM: %v", err)
	}

	// Verify Admin Credentials Secret was automatically generated (NIST IA-5)
	credSecret := &corev1.Secret{}
	credSecretName := udsvm.Name + "-admin-credentials"
	err = client.Get(ctx, types.NamespacedName{Name: credSecretName, Namespace: udsvm.Namespace}, credSecret)
	if err != nil {
		t.Fatalf("expected generated admin credentials secret %q: %v", credSecretName, err)
	}
	pw := string(credSecret.Data["password"])
	if pw == "" {
		pw = credSecret.StringData["password"]
	}
	if len(pw) < 24 {
		t.Errorf("expected high-entropy password of >= 24 chars, got %d", len(pw))
	}

	// Verify Autounattend.xml Secret was created
	sysprepSecret := &corev1.Secret{}
	err = client.Get(ctx, types.NamespacedName{Name: udsvm.Name + "-cloudinit", Namespace: udsvm.Namespace}, sysprepSecret)
	if err != nil {
		t.Fatalf("expected Sysprep secret to be created: %v", err)
	}
	if len(sysprepSecret.Data["autounattend.xml"]) == 0 && len(sysprepSecret.StringData["autounattend.xml"]) == 0 {
		t.Errorf("expected autounattend.xml in Sysprep secret")
	}

	// Verify Windows VM created with Hyper-V and firmware
	vm := &unstructured.Unstructured{}
	vm.SetAPIVersion("kubevirt.io/v1")
	vm.SetKind("VirtualMachine")
	err = client.Get(ctx, types.NamespacedName{Name: udsvm.Name, Namespace: udsvm.Namespace}, vm)
	if err != nil {
		t.Fatalf("expected Windows VirtualMachine to be created: %v", err)
	}
}

func TestBuildDataVolume_DataSource(t *testing.T) {
	udsvm := getSampleUDSVM()
	udsvm.Spec.Disk.SourceType = "dataSource"
	udsvm.Spec.Disk.DataSourceName = "ubuntu-2204-golden"
	udsvm.Spec.Disk.DataSourceNamespace = "uds-golden-images"
	udsvm.Spec.Disk.VirtualSizeBytes = 20 * 1024 * 1024 * 1024 // 20 GiB

	dv := BuildDataVolume(udsvm)
	spec, ok := dv.Object["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected spec in DataVolume")
	}

	// Verify sourceRef is populated instead of source
	if _, hasSource := spec["source"]; hasSource {
		t.Errorf("expected no source block when sourceType is dataSource")
	}

	sourceRef, ok := spec["sourceRef"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected sourceRef block in DataVolume spec")
	}

	if sourceRef["kind"] != "DataSource" {
		t.Errorf("expected sourceRef kind DataSource, got %v", sourceRef["kind"])
	}
	if sourceRef["name"] != "ubuntu-2204-golden" {
		t.Errorf("expected sourceRef name ubuntu-2204-golden, got %v", sourceRef["name"])
	}
	if sourceRef["namespace"] != "uds-golden-images" {
		t.Errorf("expected sourceRef namespace uds-golden-images, got %v", sourceRef["namespace"])
	}

	// Verify PVC size request
	storage, ok := spec["storage"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected storage block in DataVolume spec")
	}
	res, ok := storage["resources"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected resources block in storage spec")
	}
	req, ok := res["requests"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected requests block in resources")
	}
	// 20Gi * 1.06 = 21709Mi
	if req["storage"] != "21709Mi" {
		t.Errorf("expected storage request 21709Mi, got %v", req["storage"])
	}
}

func TestBuildVirtualMachine_WithDataDisks(t *testing.T) {
	udsvm := getSampleUDSVM()
	udsvm.Spec.DataDisks = []udsv1alpha1.DataDiskSpec{
		{
			Name:            "db-data",
			Size:            "50Gi",
			Bus:             "virtio",
			RetentionPolicy: "Retain",
		},
		{
			Name:            "log-data",
			Size:            "10Gi",
			Bus:             "sata",
			PVCName:         "custom-log-pvc",
			RetentionPolicy: "Delete",
		},
	}

	vm := BuildVirtualMachine(udsvm)
	spec := vm.Object["spec"].(map[string]interface{})
	template := spec["template"].(map[string]interface{})
	vmspec := template["spec"].(map[string]interface{})
	domain := vmspec["domain"].(map[string]interface{})
	devices := domain["devices"].(map[string]interface{})
	disks := devices["disks"].([]interface{})
	volumes := vmspec["volumes"].([]interface{})

	// Check disks array
	foundDBDisk := false
	foundLogDisk := false
	for _, d := range disks {
		dMap := d.(map[string]interface{})
		if dMap["name"] == "datadisk-db-data" {
			foundDBDisk = true
			diskObj := dMap["disk"].(map[string]interface{})
			if diskObj["bus"] != "virtio" {
				t.Errorf("expected bus virtio for db-data disk, got %v", diskObj["bus"])
			}
		}
		if dMap["name"] == "datadisk-log-data" {
			foundLogDisk = true
			diskObj := dMap["disk"].(map[string]interface{})
			if diskObj["bus"] != "sata" {
				t.Errorf("expected bus sata for log-data disk, got %v", diskObj["bus"])
			}
		}
	}
	if !foundDBDisk {
		t.Errorf("datadisk-db-data not found in VM disks")
	}
	if !foundLogDisk {
		t.Errorf("datadisk-log-data not found in VM disks")
	}

	// Check volumes array
	foundDBVol := false
	foundLogVol := false
	for _, v := range volumes {
		vMap := v.(map[string]interface{})
		if vMap["name"] == "datadisk-db-data" {
			foundDBVol = true
			pvcObj := vMap["persistentVolumeClaim"].(map[string]interface{})
			// Should default to <vm-name>-data-<disk-name>
			expectedClaim := udsvm.Name + "-data-db-data"
			if pvcObj["claimName"] != expectedClaim {
				t.Errorf("expected claimName %s, got %v", expectedClaim, pvcObj["claimName"])
			}
		}
		if vMap["name"] == "datadisk-log-data" {
			foundLogVol = true
			pvcObj := vMap["persistentVolumeClaim"].(map[string]interface{})
			// Should use explicit PVCName
			if pvcObj["claimName"] != "custom-log-pvc" {
				t.Errorf("expected claimName custom-log-pvc, got %v", pvcObj["claimName"])
			}
		}
	}
	if !foundDBVol {
		t.Errorf("datadisk-db-data volume not found in VM volumes")
	}
	if !foundLogVol {
		t.Errorf("datadisk-log-data volume not found in VM volumes")
	}
}

func TestBuildDataDiskPVC_RetentionPolicy(t *testing.T) {
	udsvm := getSampleUDSVM()

	// Retain disk: NO OwnerReference
	retainDisk := &udsv1alpha1.DataDiskSpec{
		Name:            "stateful-db",
		Size:            "100Gi",
		RetentionPolicy: "Retain",
	}
	retainPVC := BuildDataDiskPVC(udsvm, retainDisk)
	if len(retainPVC.OwnerReferences) != 0 {
		t.Errorf("expected 0 OwnerReferences for Retain disk, got %d", len(retainPVC.OwnerReferences))
	}
	if retainPVC.Labels["uds.dev/retention-policy"] != "Retain" {
		t.Errorf("expected retention label 'Retain', got %q", retainPVC.Labels["uds.dev/retention-policy"])
	}

	// Delete disk: OwnerReference present
	deleteDisk := &udsv1alpha1.DataDiskSpec{
		Name:            "ephemeral-scratch",
		Size:            "20Gi",
		RetentionPolicy: "Delete",
	}
	deletePVC := BuildDataDiskPVC(udsvm, deleteDisk)
	if len(deletePVC.OwnerReferences) != 1 {
		t.Errorf("expected 1 OwnerReference for Delete disk, got %d", len(deletePVC.OwnerReferences))
	}
	if deletePVC.OwnerReferences[0].Name != udsvm.Name {
		t.Errorf("expected OwnerReference name %s, got %s", udsvm.Name, deletePVC.OwnerReferences[0].Name)
	}
	if deletePVC.Labels["uds.dev/retention-policy"] != "Delete" {
		t.Errorf("expected retention label 'Delete', got %q", deletePVC.Labels["uds.dev/retention-policy"])
	}
}

func TestReconcile_DataSource_And_DataDisks(t *testing.T) {
	s := newTestReconcilerScheme()

	udsvm := getSampleUDSVM()
	udsvm.Name = "tactical-cluster-db"
	udsvm.Spec.Disk.SourceType = "dataSource"
	udsvm.Spec.Disk.DataSourceName = "rocky9-golden"
	udsvm.Spec.Disk.DataSourceNamespace = "uds-golden-images"
	udsvm.Spec.DataDisks = []udsv1alpha1.DataDiskSpec{
		{
			Name:            "pgdata",
			Size:            "30Gi",
			RetentionPolicy: "Retain",
		},
		{
			Name:            "wal",
			Size:            "10Gi",
			RetentionPolicy: "Delete",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(udsvm).
		WithStatusSubresource(udsvm).
		Build()

	r := &Reconciler{
		Client:       client,
		Scheme:       s,
		ReconcilerID: "controller-go-test",
	}

	ctx := context.Background()
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      udsvm.Name,
			Namespace: udsvm.Namespace,
		},
	}

	_, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error reconciling VM with DataSource and DataDisks: %v", err)
	}

	// 1. Verify DataVolume created with sourceRef
	dv := &unstructured.Unstructured{}
	dv.SetAPIVersion("cdi.kubevirt.io/v1beta1")
	dv.SetKind("DataVolume")
	err = client.Get(ctx, types.NamespacedName{Name: udsvm.Name + "-dv", Namespace: udsvm.Namespace}, dv)
	if err != nil {
		t.Fatalf("expected DataVolume to be created: %v", err)
	}
	spec := dv.Object["spec"].(map[string]interface{})
	sourceRef := spec["sourceRef"].(map[string]interface{})
	if sourceRef["name"] != "rocky9-golden" {
		t.Errorf("expected sourceRef rocky9-golden, got %v", sourceRef["name"])
	}

	// 2. Verify secondary PVCs created
	pgPVC := &corev1.PersistentVolumeClaim{}
	err = client.Get(ctx, types.NamespacedName{Name: udsvm.Name + "-data-pgdata", Namespace: udsvm.Namespace}, pgPVC)
	if err != nil {
		t.Fatalf("expected pgdata PVC to be created: %v", err)
	}
	if len(pgPVC.OwnerReferences) != 0 {
		t.Errorf("expected Retain pgdata PVC to have 0 OwnerReferences, got %d", len(pgPVC.OwnerReferences))
	}

	walPVC := &corev1.PersistentVolumeClaim{}
	err = client.Get(ctx, types.NamespacedName{Name: udsvm.Name + "-data-wal", Namespace: udsvm.Namespace}, walPVC)
	if err != nil {
		t.Fatalf("expected wal PVC to be created: %v", err)
	}
	if len(walPVC.OwnerReferences) != 1 {
		t.Errorf("expected Delete wal PVC to have 1 OwnerReference, got %d", len(walPVC.OwnerReferences))
	}

	// 3. Verify VirtualMachine created with all disks
	vm := &unstructured.Unstructured{}
	vm.SetAPIVersion("kubevirt.io/v1")
	vm.SetKind("VirtualMachine")
	err = client.Get(ctx, types.NamespacedName{Name: udsvm.Name, Namespace: udsvm.Namespace}, vm)
	if err != nil {
		t.Fatalf("expected VirtualMachine to be created: %v", err)
	}
}

func TestReconciler_InvalidDataDiskSize(t *testing.T) {
	s := newTestReconcilerScheme()
	udsvm := getSampleUDSVM()
	udsvm.Name = "invalid-disk-vm"
	udsvm.Spec.DataDisks = []udsv1alpha1.DataDiskSpec{
		{
			Name: "broken",
			Size: "not-a-quantity",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(udsvm).
		WithStatusSubresource(udsvm).
		Build()

	r := &Reconciler{
		Client:       c,
		Scheme:       s,
		ReconcilerID: "controller-go-test",
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: udsvm.Name, Namespace: udsvm.Namespace},
	})
	if err == nil {
		t.Fatalf("expected error for invalid dataDisks[].size, got nil")
	}
	if !strings.Contains(err.Error(), "invalid dataDisks[0].size") {
		t.Errorf("expected descriptive invalid size error, got: %v", err)
	}
}

func TestWindowsProvisioningModesAndCloneXML(t *testing.T) {
	falseVal := false

	// 1. Explicit clone mode with slipstreamed VirtIO/qemu-ga (injectVirtioWinISO: false)
	cloneVM := &udsv1alpha1.UDSVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "win2022-clone-01",
			Namespace: "default",
		},
		Spec: udsv1alpha1.UDSVirtualMachineSpec{
			OSType: "windows",
			WindowsProfile: &udsv1alpha1.WindowsProfileSpec{
				ProvisioningMode:   "clone",
				InjectVirtioWinISO: &falseVal,
			},
			Disk: udsv1alpha1.DiskSpec{
				SourceType:     "upload",
				Format:         "qcow2",
				DataSourceName: "windows-server-2022-v1",
			},
		},
	}

	if got := ResolveWindowsProvisioningMode(cloneVM); got != "clone" {
		t.Fatalf("expected resolved mode 'clone', got %q", got)
	}
	cloneXML := BuildWindowsAutounattendXML(cloneVM, "CloneP@ssw0rd!2026")
	if strings.Contains(cloneXML, `pass="windowsPE"`) || strings.Contains(cloneXML, "<WillWipeDisk>") {
		t.Errorf("clone mode XML must not contain windowsPE or WillWipeDisk:\n%s", cloneXML)
	}
	if strings.Contains(cloneXML, "qemu-ga-x86_64.msi") {
		t.Errorf("clone mode with injectVirtioWinISO=false must skip runtime qemu-ga msiexec:\n%s", cloneXML)
	}
	if !strings.Contains(cloneXML, `pass="specialize"`) || !strings.Contains(cloneXML, `pass="oobeSystem"`) {
		t.Errorf("clone mode XML missing specialize or oobeSystem pass:\n%s", cloneXML)
	}

	// 2. Auto mode with dataSource resolves to clone
	autoDS := &udsv1alpha1.UDSVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "win2022-ds", Namespace: "default"},
		Spec: udsv1alpha1.UDSVirtualMachineSpec{
			OSType:         "windows",
			WindowsProfile: &udsv1alpha1.WindowsProfileSpec{ProvisioningMode: "auto"},
			Disk:           udsv1alpha1.DiskSpec{SourceType: "dataSource", DataSourceName: "win2022"},
		},
	}
	if got := ResolveWindowsProvisioningMode(autoDS); got != "clone" {
		t.Errorf("expected auto+dataSource to resolve to 'clone', got %q", got)
	}

	// 3. Explicit iso mode emits windowsPE
	isoVM := &udsv1alpha1.UDSVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "win2022-iso", Namespace: "default"},
		Spec: udsv1alpha1.UDSVirtualMachineSpec{
			OSType:         "windows",
			WindowsProfile: &udsv1alpha1.WindowsProfileSpec{ProvisioningMode: "iso"},
			Disk:           udsv1alpha1.DiskSpec{SourceType: "upload", Format: "iso"},
		},
	}
	if got := ResolveWindowsProvisioningMode(isoVM); got != "iso" {
		t.Errorf("expected explicit 'iso' mode, got %q", got)
	}
	isoXML := BuildWindowsAutounattendXML(isoVM, "IsoP@ssw0rd!2026")
	if !strings.Contains(isoXML, `pass="windowsPE"`) || !strings.Contains(isoXML, "<WillWipeDisk>true</WillWipeDisk>") {
		t.Errorf("iso mode XML missing windowsPE or WillWipeDisk:\n%s", isoXML)
	}
}

func TestSanitizeNetBIOSName_PoolUniqueness(t *testing.T) {
	r0 := SanitizePoolNetBIOSName("win2022-app-pool-a1b2c3d4-0")
	r1 := SanitizePoolNetBIOSName("win2022-app-pool-a1b2c3d4-1")
	if len(r0) > 15 || len(r1) > 15 {
		t.Fatalf("NetBIOS names exceeded 15 chars: %q (%d), %q (%d)", r0, len(r0), r1, len(r1))
	}
	if r0 == r1 {
		t.Errorf("expected distinct NetBIOS names for pool replicas, both got %q", r0)
	}
}

func TestWindowsPersistentISOInstallDisk(t *testing.T) {
	builderVM := &udsv1alpha1.UDSVirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "win2022-golden-builder",
			Namespace: "uds-golden-images",
		},
		Spec: udsv1alpha1.UDSVirtualMachineSpec{
			OSType: "windows",
			Disk: udsv1alpha1.DiskSpec{
				SourceType: "upload",
				Format:     "iso",
			},
			DataDisks: []udsv1alpha1.DataDiskSpec{
				{Name: "installdisk", Size: "40Gi", Bus: "virtio", RetentionPolicy: "Retain"},
			},
		},
	}

	vmObj := BuildVirtualMachine(builderVM)
	tmplSpec := vmObj.Object["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})
	volumes := tmplSpec["volumes"].([]interface{})

	var foundPVCInstall bool
	for _, v := range volumes {
		vm := v.(map[string]interface{})
		if _, hasEmpty := vm["emptyDisk"]; hasEmpty {
			t.Fatalf("expected no ephemeral emptyDisk when persistent installdisk dataDisk is specified")
		}
		if vm["name"] == "datadisk-installdisk" {
			if pvc, ok := vm["persistentVolumeClaim"].(map[string]interface{}); ok {
				if pvc["claimName"] == "win2022-golden-builder-data-installdisk" {
					foundPVCInstall = true
				}
			}
		}
	}
	if !foundPVCInstall {
		t.Errorf("expected persistentVolumeClaim volume datadisk-installdisk for Stage 1 ISO builder")
	}
}

