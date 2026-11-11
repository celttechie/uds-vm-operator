// Package v1alpha1 contains API Schema definitions for the uds.dev v1alpha1 API group.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "uds.dev", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&UDSVirtualMachine{},
		&UDSVirtualMachineList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

// DiskSpec defines the VM root disk image and cryptographic verification parameters.
type DiskSpec struct {
	SourceType       string `json:"sourceType"`
	HTTPURL          string `json:"httpUrl,omitempty"`
	Format           string `json:"format,omitempty"`
	VirtualSizeBytes int64  `json:"virtualSizeBytes"`
	Checksum         string `json:"checksum"`
	StorageClassName string `json:"storageClassName,omitempty"`
}

// SecurityProfileSpec defines NIST 800-53 / DISA STIG hardening controls.
type SecurityProfileSpec struct {
	ZeroTrustNetwork    *bool `json:"zeroTrustNetwork,omitempty"`
	DisallowSSHPassword *bool `json:"disallowSSHPassword,omitempty"`
	FIPSMode            *bool `json:"fipsMode,omitempty"`
}

// WindowsProfileSpec defines configuration parameters for Windows guest virtual machines.
type WindowsProfileSpec struct {
	DiskBus                    string `json:"diskBus,omitempty"`
	NICModel                   string `json:"nicModel,omitempty"`
	InjectVirtioWinISO         *bool  `json:"injectVirtioWinISO,omitempty"`
	VirtioWinImage             string `json:"virtioWinImage,omitempty"`
	EnableHyperVEnlightenments *bool  `json:"enableHyperVEnlightenments,omitempty"`
	EnableSecureBootTPM        *bool  `json:"enableSecureBootTPM,omitempty"`
	PersistentTPM              *bool  `json:"persistentTPM,omitempty"`
}

// DeepCopyInto copies all properties of WindowsProfileSpec.
func (in *WindowsProfileSpec) DeepCopyInto(out *WindowsProfileSpec) {
	*out = *in
	if in.InjectVirtioWinISO != nil {
		v := *in.InjectVirtioWinISO
		out.InjectVirtioWinISO = &v
	}
	if in.EnableHyperVEnlightenments != nil {
		v := *in.EnableHyperVEnlightenments
		out.EnableHyperVEnlightenments = &v
	}
	if in.EnableSecureBootTPM != nil {
		v := *in.EnableSecureBootTPM
		out.EnableSecureBootTPM = &v
	}
	if in.PersistentTPM != nil {
		v := *in.PersistentTPM
		out.PersistentTPM = &v
	}
}

// DeepCopy copies the receiver, creating a new WindowsProfileSpec.
func (in *WindowsProfileSpec) DeepCopy() *WindowsProfileSpec {
	if in == nil {
		return nil
	}
	out := new(WindowsProfileSpec)
	in.DeepCopyInto(out)
	return out
}

// UDSVirtualMachineSpec defines the desired state of UDSVirtualMachine.
type UDSVirtualMachineSpec struct {
	CPUs            int                 `json:"cpus,omitempty"`
	Memory          string              `json:"memory,omitempty"`
	RunStrategy     string              `json:"runStrategy,omitempty"`
	OSType          string              `json:"osType,omitempty"`
	WindowsProfile  *WindowsProfileSpec `json:"windowsProfile,omitempty"`
	Disk            DiskSpec            `json:"disk"`
	SecurityProfile SecurityProfileSpec `json:"securityProfile,omitempty"`
}

// ComplianceStatus reports real-time security posture of the VM and its artifacts.
type ComplianceStatus struct {
	ChecksumVerified  bool `json:"checksumVerified"`
	ZeroTrustIsolated bool `json:"zeroTrustIsolated"`
	CloudInitHardened bool `json:"cloudInitHardened"`
}

// UDSVirtualMachineStatus defines the observed state of UDSVirtualMachine.
type UDSVirtualMachineStatus struct {
	Phase             string             `json:"phase,omitempty"`
	CalculatedPVCSize string             `json:"calculatedPvcSize,omitempty"`
	DataVolumeName    string             `json:"dataVolumeName,omitempty"`
	DataVolumePhase   string             `json:"dataVolumePhase,omitempty"`
	ReconciledBy      string             `json:"reconciledBy,omitempty"`
	Compliance        ComplianceStatus   `json:"compliance,omitempty"`
	Conditions        []metav1.Condition `json:"conditions,omitempty"`
}

// UDSVirtualMachine is the Schema for the udsvirtualmachines API.
type UDSVirtualMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UDSVirtualMachineSpec   `json:"spec,omitempty"`
	Status UDSVirtualMachineStatus `json:"status,omitempty"`
}

// DeepCopyInto copies all properties of this object into another object of the same type.
func (in *UDSVirtualMachine) DeepCopyInto(out *UDSVirtualMachine) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	if in.Spec.WindowsProfile != nil {
		out.Spec.WindowsProfile = in.Spec.WindowsProfile.DeepCopy()
	}
	if in.Spec.SecurityProfile.ZeroTrustNetwork != nil {
		v := *in.Spec.SecurityProfile.ZeroTrustNetwork
		out.Spec.SecurityProfile.ZeroTrustNetwork = &v
	}
	if in.Spec.SecurityProfile.DisallowSSHPassword != nil {
		v := *in.Spec.SecurityProfile.DisallowSSHPassword
		out.Spec.SecurityProfile.DisallowSSHPassword = &v
	}
	if in.Spec.SecurityProfile.FIPSMode != nil {
		v := *in.Spec.SecurityProfile.FIPSMode
		out.Spec.SecurityProfile.FIPSMode = &v
	}
	out.Status = in.Status
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		for i := range in.Status.Conditions {
			in.Status.Conditions[i].DeepCopyInto(&out.Status.Conditions[i])
		}
	}
}

// DeepCopy copies the receiver, creating a new UDSVirtualMachine.
func (in *UDSVirtualMachine) DeepCopy() *UDSVirtualMachine {
	if in == nil {
		return nil
	}
	out := new(UDSVirtualMachine)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject copies the receiver as a runtime.Object.
func (in *UDSVirtualMachine) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// UDSVirtualMachineList contains a list of UDSVirtualMachine.
type UDSVirtualMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UDSVirtualMachine `json:"items"`
}

// DeepCopyInto copies all properties of this list into another list.
func (in *UDSVirtualMachineList) DeepCopyInto(out *UDSVirtualMachineList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]UDSVirtualMachine, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy copies the receiver, creating a new UDSVirtualMachineList.
func (in *UDSVirtualMachineList) DeepCopy() *UDSVirtualMachineList {
	if in == nil {
		return nil
	}
	out := new(UDSVirtualMachineList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject copies the receiver as a runtime.Object.
func (in *UDSVirtualMachineList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
