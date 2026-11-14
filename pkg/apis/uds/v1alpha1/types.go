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
		&UDSVirtualMachinePool{},
		&UDSVirtualMachinePoolList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

// DiskSpec defines the VM root disk image and cryptographic verification parameters.
type DiskSpec struct {
	SourceType          string `json:"sourceType"`
	HTTPURL             string `json:"httpUrl,omitempty"`
	DataSourceName      string `json:"dataSourceName,omitempty"`
	DataSourceNamespace string `json:"dataSourceNamespace,omitempty"`
	Format              string `json:"format,omitempty"`
	VirtualSizeBytes    int64  `json:"virtualSizeBytes,omitempty"`
	Checksum            string `json:"checksum,omitempty"`
	StorageClassName    string `json:"storageClassName,omitempty"`
}

// DataDiskSpec defines secondary persistent data disks attached to the VM.
type DataDiskSpec struct {
	Name             string `json:"name"`
	Size             string `json:"size"`
	Bus              string `json:"bus,omitempty"`
	StorageClassName string `json:"storageClassName,omitempty"`
	PVCName          string `json:"pvcName,omitempty"`
	RetentionPolicy  string `json:"retentionPolicy,omitempty"`
	MountPath        string `json:"mountPath,omitempty"`
}

// DeepCopyInto copies all properties of DataDiskSpec.
func (in *DataDiskSpec) DeepCopyInto(out *DataDiskSpec) {
	*out = *in
}

// DeepCopy copies the receiver, creating a new DataDiskSpec.
func (in *DataDiskSpec) DeepCopy() *DataDiskSpec {
	if in == nil {
		return nil
	}
	out := new(DataDiskSpec)
	in.DeepCopyInto(out)
	return out
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
	SysprepSecretRef           string `json:"sysprepSecretRef,omitempty"`
	AdminPasswordSecretRef     string `json:"adminPasswordSecretRef,omitempty"`
	ProvisioningMode           string `json:"provisioningMode,omitempty"`
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
	DataDisks       []DataDiskSpec      `json:"dataDisks,omitempty"`
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

// DeepCopyInto copies all properties of UDSVirtualMachineSpec into out.
func (in *UDSVirtualMachineSpec) DeepCopyInto(out *UDSVirtualMachineSpec) {
	*out = *in
	if in.WindowsProfile != nil {
		out.WindowsProfile = in.WindowsProfile.DeepCopy()
	}
	if in.DataDisks != nil {
		out.DataDisks = make([]DataDiskSpec, len(in.DataDisks))
		for i := range in.DataDisks {
			in.DataDisks[i].DeepCopyInto(&out.DataDisks[i])
		}
	}
	if in.SecurityProfile.ZeroTrustNetwork != nil {
		v := *in.SecurityProfile.ZeroTrustNetwork
		out.SecurityProfile.ZeroTrustNetwork = &v
	}
	if in.SecurityProfile.DisallowSSHPassword != nil {
		v := *in.SecurityProfile.DisallowSSHPassword
		out.SecurityProfile.DisallowSSHPassword = &v
	}
	if in.SecurityProfile.FIPSMode != nil {
		v := *in.SecurityProfile.FIPSMode
		out.SecurityProfile.FIPSMode = &v
	}
}

// DeepCopyInto copies all properties of this object into another object of the same type.
func (in *UDSVirtualMachine) DeepCopyInto(out *UDSVirtualMachine) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
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

// RollingUpdateConfig configures surge and unavailability limits during fleet updates.
type RollingUpdateConfig struct {
	MaxSurge       *int32 `json:"maxSurge,omitempty"`
	MaxUnavailable *int32 `json:"maxUnavailable,omitempty"`
}

// RollingUpdateStrategySpec defines the update strategy for a UDSVirtualMachinePool.
type RollingUpdateStrategySpec struct {
	Type          string               `json:"type,omitempty"`
	RollingUpdate *RollingUpdateConfig `json:"rollingUpdate,omitempty"`
}

// UDSVirtualMachineTemplateSpec describes the template used to stamp out pool replicas.
type UDSVirtualMachineTemplateSpec struct {
	Metadata metav1.ObjectMeta     `json:"metadata,omitempty"`
	Spec     UDSVirtualMachineSpec `json:"spec"`
}

// DeepCopyInto copies all properties of UDSVirtualMachineTemplateSpec into out.
func (in *UDSVirtualMachineTemplateSpec) DeepCopyInto(out *UDSVirtualMachineTemplateSpec) {
	*out = *in
	in.Metadata.DeepCopyInto(&out.Metadata)
	in.Spec.DeepCopyInto(&out.Spec)
}

// UDSVirtualMachinePoolSpec defines the desired state of a UDSVirtualMachinePool.
type UDSVirtualMachinePoolSpec struct {
	Replicas *int32                        `json:"replicas,omitempty"`
	Selector *metav1.LabelSelector         `json:"selector,omitempty"`
	Strategy RollingUpdateStrategySpec     `json:"strategy,omitempty"`
	Template UDSVirtualMachineTemplateSpec `json:"template"`
}

// UDSVirtualMachinePoolStatus defines the observed state of a UDSVirtualMachinePool.
type UDSVirtualMachinePoolStatus struct {
	Replicas        int32              `json:"replicas"`
	ReadyReplicas   int32              `json:"readyReplicas"`
	UpdatedReplicas int32              `json:"updatedReplicas"`
	CurrentRevision string             `json:"currentRevision,omitempty"`
	Phase           string             `json:"phase,omitempty"`
	Message         string             `json:"message,omitempty"`
	Conditions      []metav1.Condition `json:"conditions,omitempty"`
}

// UDSVirtualMachinePool is the Schema for the udsvirtualmachinepools API.
type UDSVirtualMachinePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UDSVirtualMachinePoolSpec   `json:"spec,omitempty"`
	Status UDSVirtualMachinePoolStatus `json:"status,omitempty"`
}

// DeepCopyInto copies all properties of UDSVirtualMachinePool into out.
func (in *UDSVirtualMachinePool) DeepCopyInto(out *UDSVirtualMachinePool) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.Replicas != nil {
		v := *in.Spec.Replicas
		out.Spec.Replicas = &v
	}
	if in.Spec.Selector != nil {
		out.Spec.Selector = in.Spec.Selector.DeepCopy()
	}
	if in.Spec.Strategy.RollingUpdate != nil {
		ru := &RollingUpdateConfig{}
		if in.Spec.Strategy.RollingUpdate.MaxSurge != nil {
			v := *in.Spec.Strategy.RollingUpdate.MaxSurge
			ru.MaxSurge = &v
		}
		if in.Spec.Strategy.RollingUpdate.MaxUnavailable != nil {
			v := *in.Spec.Strategy.RollingUpdate.MaxUnavailable
			ru.MaxUnavailable = &v
		}
		out.Spec.Strategy.RollingUpdate = ru
	}
	in.Spec.Template.DeepCopyInto(&out.Spec.Template)
	out.Status = in.Status
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		for i := range in.Status.Conditions {
			in.Status.Conditions[i].DeepCopyInto(&out.Status.Conditions[i])
		}
	}
}

// DeepCopy copies the receiver, creating a new UDSVirtualMachinePool.
func (in *UDSVirtualMachinePool) DeepCopy() *UDSVirtualMachinePool {
	if in == nil {
		return nil
	}
	out := new(UDSVirtualMachinePool)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject copies the receiver as a runtime.Object.
func (in *UDSVirtualMachinePool) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// UDSVirtualMachinePoolList contains a list of UDSVirtualMachinePool.
type UDSVirtualMachinePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UDSVirtualMachinePool `json:"items"`
}

// DeepCopyInto copies all properties of UDSVirtualMachinePoolList into out.
func (in *UDSVirtualMachinePoolList) DeepCopyInto(out *UDSVirtualMachinePoolList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]UDSVirtualMachinePool, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy copies the receiver, creating a new UDSVirtualMachinePoolList.
func (in *UDSVirtualMachinePoolList) DeepCopy() *UDSVirtualMachinePoolList {
	if in == nil {
		return nil
	}
	out := new(UDSVirtualMachinePoolList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject copies the receiver as a runtime.Object.
func (in *UDSVirtualMachinePoolList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
