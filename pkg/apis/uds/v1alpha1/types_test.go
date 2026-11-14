package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUDSVirtualMachine_DeepCopy(t *testing.T) {
	trueVal := true
	orig := &UDSVirtualMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "UDSVirtualMachine",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-vm",
			Namespace: "default",
			Labels: map[string]string{
				"tier": "mission-critical",
			},
		},
		Spec: UDSVirtualMachineSpec{
			CPUs:        2,
			Memory:      "4Gi",
			RunStrategy: "Always",
			OSType:      "windows",
			WindowsProfile: &WindowsProfileSpec{
				DiskBus:                    "virtio",
				NICModel:                   "virtio",
				InjectVirtioWinISO:         &trueVal,
				EnableHyperVEnlightenments: &trueVal,
				EnableSecureBootTPM:        &trueVal,
				PersistentTPM:              &trueVal,
				AdminPasswordSecretRef:     "custom-secret",
			},
			Disk: DiskSpec{
				SourceType:       "upload",
				VirtualSizeBytes: 10485760,
				Checksum:         "sha256:e5b844cc57f57094ea4585e235f36c78c1cd222262bb89d53c94dcb4d6b3e55d",
			},
			SecurityProfile: SecurityProfileSpec{
				ZeroTrustNetwork:    &trueVal,
				DisallowSSHPassword: &trueVal,
				FIPSMode:            &trueVal,
			},
		},
		Status: UDSVirtualMachineStatus{
			Phase:             "Ready",
			CalculatedPVCSize: "11Mi",
			ReconciledBy:      "controller-go",
			Compliance: ComplianceStatus{
				ChecksumVerified:  true,
				ZeroTrustIsolated: true,
				CloudInitHardened: true,
			},
			Conditions: []metav1.Condition{
				{
					Type:   "Ready",
					Status: metav1.ConditionTrue,
					Reason: "WorkloadReady",
				},
			},
		},
	}

	copied := orig.DeepCopy()
	if copied == nil {
		t.Fatal("expected non-nil copy")
	}
	if copied.Name != orig.Name {
		t.Errorf("copied name = %s, want %s", copied.Name, orig.Name)
	}
	if *copied.Spec.WindowsProfile.InjectVirtioWinISO != *orig.Spec.WindowsProfile.InjectVirtioWinISO {
		t.Errorf("copied WindowsProfile not matching")
	}

	// Verify deep copy mutability isolation
	copied.Name = "mutated-vm"
	copied.Spec.WindowsProfile.AdminPasswordSecretRef = "mutated-secret"
	if orig.Name == "mutated-vm" {
		t.Errorf("modifying copy affected original Name")
	}
	if orig.Spec.WindowsProfile.AdminPasswordSecretRef == "mutated-secret" {
		t.Errorf("modifying copy affected original WindowsProfile")
	}

	// Test DeepCopyObject
	objCopy := orig.DeepCopyObject()
	if objCopy == nil {
		t.Fatal("expected non-nil DeepCopyObject")
	}

	// Nil receiver tests
	var nilVM *UDSVirtualMachine
	if nilVM.DeepCopy() != nil {
		t.Errorf("expected nil for nilVM.DeepCopy()")
	}
	var nilProfile *WindowsProfileSpec
	if nilProfile.DeepCopy() != nil {
		t.Errorf("expected nil for nilProfile.DeepCopy()")
	}
}

func TestUDSVirtualMachineList_DeepCopy(t *testing.T) {
	origList := &UDSVirtualMachineList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "UDSVirtualMachineList",
		},
		Items: []UDSVirtualMachine{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "item-1",
				},
			},
		},
	}

	copiedList := origList.DeepCopy()
	if copiedList == nil {
		t.Fatal("expected non-nil list copy")
	}
	if len(copiedList.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(copiedList.Items))
	}

	copiedList.Items[0].Name = "mutated-item"
	if origList.Items[0].Name == "mutated-item" {
		t.Errorf("modifying item in copied list affected original")
	}

	objListCopy := origList.DeepCopyObject()
	if objListCopy == nil {
		t.Fatal("expected non-nil DeepCopyObject for list")
	}

	var nilList *UDSVirtualMachineList
	if nilList.DeepCopy() != nil {
		t.Errorf("expected nil for nilList.DeepCopy()")
	}
}

func TestUDSVirtualMachinePool_DeepCopy(t *testing.T) {
	rep := int32(2)
	surge := int32(1)
	unavail := int32(0)
	orig := &UDSVirtualMachinePool{
		ObjectMeta: metav1.ObjectMeta{Name: "pool-1", Namespace: "default"},
		Spec: UDSVirtualMachinePoolSpec{
			Replicas: &rep,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Strategy: RollingUpdateStrategySpec{
				Type: "RollingUpdate",
				RollingUpdate: &RollingUpdateConfig{
					MaxSurge:       &surge,
					MaxUnavailable: &unavail,
				},
			},
			Template: UDSVirtualMachineTemplateSpec{
				Metadata: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
				Spec: UDSVirtualMachineSpec{
					CPUs:   2,
					Memory: "2Gi",
					Disk:   DiskSpec{SourceType: "dataSource", DataSourceName: "golden"},
				},
			},
		},
		Status: UDSVirtualMachinePoolStatus{
			Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}},
		},
	}

	copied := orig.DeepCopy()
	if copied == nil || copied.DeepCopyObject() == nil {
		t.Fatal("expected non-nil DeepCopy and DeepCopyObject")
	}
	*copied.Spec.Replicas = 5
	*copied.Spec.Strategy.RollingUpdate.MaxSurge = 3
	*copied.Spec.Strategy.RollingUpdate.MaxUnavailable = 2
	copied.Spec.Selector.MatchLabels["app"] = "db"
	copied.Status.Conditions[0].Status = metav1.ConditionFalse

	if *orig.Spec.Replicas != 2 || *orig.Spec.Strategy.RollingUpdate.MaxSurge != 1 || *orig.Spec.Strategy.RollingUpdate.MaxUnavailable != 0 {
		t.Errorf("pool DeepCopy mutated original pointer fields")
	}
	if orig.Spec.Selector.MatchLabels["app"] != "web" || orig.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("pool DeepCopy mutated original selector or conditions")
	}

	var nilPool *UDSVirtualMachinePool
	if nilPool.DeepCopy() != nil || nilPool.DeepCopyObject() != nil {
		t.Errorf("expected nil for nil pool DeepCopy")
	}
}

func TestUDSVirtualMachinePoolList_DeepCopy(t *testing.T) {
	orig := &UDSVirtualMachinePoolList{
		Items: []UDSVirtualMachinePool{{ObjectMeta: metav1.ObjectMeta{Name: "p1"}}},
	}
	copied := orig.DeepCopy()
	if copied == nil || copied.DeepCopyObject() == nil {
		t.Fatal("expected non-nil pool list DeepCopy")
	}
	copied.Items[0].Name = "mutated"
	if orig.Items[0].Name == "mutated" {
		t.Errorf("pool list DeepCopy mutated original")
	}
	var nilList *UDSVirtualMachinePoolList
	if nilList.DeepCopy() != nil || nilList.DeepCopyObject() != nil {
		t.Errorf("expected nil for nil pool list DeepCopy")
	}
}
