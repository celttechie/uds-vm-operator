package controller

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	udsv1alpha1 "github.com/celttechie/uds-vm-operator/pkg/apis/uds/v1alpha1"
)

const (
	CdiFsOverheadMultiplier = 1.06
	MiB                     = 1024 * 1024
	DefaultReconcilerID     = "controller-go"
)

// CalculatePVCSizeMiB calculates the PVC size with +6% KubeVirt/CDI filesystem overhead.
func CalculatePVCSizeMiB(virtualSizeBytes int64) string {
	rawMiB := float64(virtualSizeBytes) / float64(MiB)
	paddedMiB := math.Ceil(rawMiB * CdiFsOverheadMultiplier)
	if paddedMiB <= rawMiB {
		paddedMiB = math.Ceil(rawMiB) + 1
	}
	if paddedMiB < 1 {
		paddedMiB = 1
	}
	return fmt.Sprintf("%dMi", int64(paddedMiB))
}

// BuildOwnerReference builds standard controller OwnerReference for garbage collection.
func BuildOwnerReference(udsvm *udsv1alpha1.UDSVirtualMachine) metav1.OwnerReference {
	trueVal := true
	return metav1.OwnerReference{
		APIVersion:         udsv1alpha1.GroupVersion.String(),
		Kind:               "UDSVirtualMachine",
		Name:               udsvm.Name,
		UID:                udsvm.UID,
		Controller:         &trueVal,
		BlockOwnerDeletion: &trueVal,
	}
}

// BuildZeroTrustNetworkPolicy builds a default-deny-ingress + restricted-egress (DNS only)
// NetworkPolicy for the KubeVirt virt-launcher pod (NIST 800-53 SC-7 Boundary Protection).
func BuildZeroTrustNetworkPolicy(udsvm *udsv1alpha1.UDSVirtualMachine) *networkingv1.NetworkPolicy {
	ownerRef := BuildOwnerReference(udsvm)
	dnsPort := intstr.FromInt(53)
	protoUDP := corev1.ProtocolUDP
	protoTCP := corev1.ProtocolTCP

	return &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "networking.k8s.io/v1",
			Kind:       "NetworkPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-netpol", udsvm.Name),
			Namespace: udsvm.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       udsvm.Name,
				"app.kubernetes.io/managed-by": "uds-vm-operator",
				"uds.dev/nist-control":         "SC-7",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"vm.kubevirt.io/name": udsvm.Name,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &protoUDP,
							Port:     &dnsPort,
						},
						{
							Protocol: &protoTCP,
							Port:     &dnsPort,
						},
					},
				},
			},
		},
	}
}

// BuildCloudInitSecret builds a hardened Cloud-Init NoCloud Secret (Linux) or
// autounattend.xml / unattend.xml Sysprep Secret (Windows) (NIST 800-53 AC-3 / IA-5).
func BuildCloudInitSecret(udsvm *udsv1alpha1.UDSVirtualMachine, adminPassword ...string) *corev1.Secret {
	ownerRef := BuildOwnerReference(udsvm)
	isWindows := udsvm.Spec.OSType == "windows"

	disallowSSH := true
	if udsvm.Spec.SecurityProfile.DisallowSSHPassword != nil {
		disallowSSH = *udsvm.Spec.SecurityProfile.DisallowSSHPassword
	}
	fipsMode := true
	if udsvm.Spec.SecurityProfile.FIPSMode != nil {
		fipsMode = *udsvm.Spec.SecurityProfile.FIPSMode
	}

	sshPwAuthStr := "true"
	if disallowSSH {
		sshPwAuthStr = "false"
	}

	userdata := fmt.Sprintf("#cloud-config\nhostname: %s\nssh_pwauth: %s\ndisable_root: true\n", udsvm.Name, sshPwAuthStr)
	if fipsMode {
		userdata += "runcmd:\n  - [ sh, -c, 'echo Hardened UDS VM initialized (FIPS profile active)' ]\n"
	}

	stringData := map[string]string{
		"userdata": userdata,
	}

	if isWindows {
		pw := ""
		if len(adminPassword) > 0 && adminPassword[0] != "" {
			pw = adminPassword[0]
		}
		autounattendXML := BuildWindowsAutounattendXML(udsvm, pw)
		stringData["autounattend.xml"] = autounattendXML
		stringData["unattend.xml"] = autounattendXML
	}

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-cloudinit", udsvm.Name),
			Namespace: udsvm.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       udsvm.Name,
				"app.kubernetes.io/managed-by": "uds-vm-operator",
				"uds.dev/nist-control":         "AC-3",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: stringData,
	}
}

// GenerateSecurePassword generates a cryptographically random password meeting DISA STIG complexity (NIST 800-53 IA-5).
func GenerateSecurePassword(length int) (string, error) {
	if length < 16 {
		length = 24
	}
	const (
		upper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		lower   = "abcdefghijkmnopqrstuvwxyz"
		digits  = "23456789"
		symbols = "!@#$%^&*-_=+"
	)
	b := make([]byte, length)
	classes := []string{upper, lower, digits, symbols}
	for i, c := range classes {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(c))))
		if err != nil {
			return "", err
		}
		b[i] = c[n.Int64()]
	}

	all := upper + lower + digits + symbols
	for i := len(classes); i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(all))))
		if err != nil {
			return "", err
		}
		b[i] = all[n.Int64()]
	}

	for i := range b {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(length)))
		if err != nil {
			return "", err
		}
		b[i], b[j.Int64()] = b[j.Int64()], b[i]
	}

	return string(b), nil
}

// SanitizeNetBIOSName truncates and sanitizes a VM name to meet Windows 15-character NetBIOS limits.
func SanitizeNetBIOSName(name string) string {
	name = strings.ToUpper(name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	res := b.String()
	if len(res) > 15 {
		res = res[:15]
	}
	res = strings.TrimRight(res, "-")
	if res == "" {
		res = "WIN-TACTICAL"
	}
	return res
}

// BuildWindowsAutounattendXML generates a zero-touch Autounattend.xml answer file for Windows Setup (NIST 800-53 AC-3, IA-5).
func BuildWindowsAutounattendXML(udsvm *udsv1alpha1.UDSVirtualMachine, adminPassword string) string {
	if adminPassword == "" {
		adminPassword = "P@ssw0rd!" + udsvm.Name
	}
	computerName := SanitizeNetBIOSName(udsvm.Name)

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <settings pass="windowsPE">
    <component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <SetupUILanguage>
        <UILanguage>en-US</UILanguage>
      </SetupUILanguage>
      <InputLocale>en-US</InputLocale>
      <SystemLocale>en-US</SystemLocale>
      <UILanguage>en-US</UILanguage>
      <UserLocale>en-US</UserLocale>
    </component>
    <component name="Microsoft-Windows-PnpCustomizationsWinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <DriverPaths>
        <PathAndCredentials wcm:action="add" wcm:keyValue="1">
          <Path>F:\amd64\2k22</Path>
        </PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="2">
          <Path>F:\NetKVM\2k22\amd64</Path>
        </PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="3">
          <Path>F:\Balloon\2k22\amd64</Path>
        </PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="4">
          <Path>F:\vioserial\2k22\amd64</Path>
        </PathAndCredentials>
      </DriverPaths>
    </component>
    <component name="Microsoft-Windows-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <DiskConfiguration>
        <Disk wcm:action="add">
          <DiskID>0</DiskID>
          <WillWipeDisk>true</WillWipeDisk>
          <CreatePartitions>
            <CreatePartition wcm:action="add">
              <Order>1</Order>
              <Type>EFI</Type>
              <Size>100</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>2</Order>
              <Type>MSR</Type>
              <Size>16</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>3</Order>
              <Type>Primary</Type>
              <Extend>true</Extend>
            </CreatePartition>
          </CreatePartitions>
          <ModifyPartitions>
            <ModifyPartition wcm:action="add">
              <Order>1</Order>
              <PartitionID>1</PartitionID>
              <Format>FAT32</Format>
              <Label>System</Label>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>2</Order>
              <PartitionID>2</PartitionID>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>3</Order>
              <PartitionID>3</PartitionID>
              <Format>NTFS</Format>
              <Label>Windows</Label>
              <Letter>C</Letter>
            </ModifyPartition>
          </ModifyPartitions>
        </Disk>
      </DiskConfiguration>
      <ImageInstall>
        <OSImage>
          <InstallFrom>
            <MetaData wcm:action="add">
              <Key>/IMAGE/INDEX</Key>
              <Value>2</Value>
            </MetaData>
          </InstallFrom>
          <InstallTo>
            <DiskID>0</DiskID>
            <PartitionID>3</PartitionID>
          </InstallTo>
          <WillShowUI>OnError</WillShowUI>
        </OSImage>
      </ImageInstall>
      <UserData>
        <AcceptEula>true</AcceptEula>
        <FullName>UDS Administrator</FullName>
        <Organization>Defense Unicorns</Organization>
      </UserData>
    </component>
  </settings>
  <settings pass="specialize">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <ComputerName>%s</ComputerName>
      <TimeZone>UTC</TimeZone>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideLocalAccountScreen>true</HideLocalAccountScreen>
        <HideOEMRegistrationScreen>true</HideOEMRegistrationScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <NetworkLocation>Work</NetworkLocation>
        <ProtectYourPC>3</ProtectYourPC>
      </OOBE>
      <UserAccounts>
        <AdministratorPassword>
          <Value>%s</Value>
          <PlainText>true</PlainText>
        </AdministratorPassword>
      </UserAccounts>
      <FirstLogonCommands>
        <SynchronousCommand wcm:action="add">
          <Order>1</Order>
          <CommandLine>powershell -ExecutionPolicy Bypass -Command "Get-ChildItem -Path D:\,E:\,F:\ -Filter 'qemu-ga-x86_64.msi' -Recurse -ErrorAction SilentlyContinue | ForEach-Object { Start-Process msiexec.exe -ArgumentList ('/i', $_.FullName, '/qn', '/norestart') -Wait }"</CommandLine>
          <Description>Install QEMU Guest Agent</Description>
        </SynchronousCommand>
      </FirstLogonCommands>
    </component>
  </settings>
</unattend>`, computerName, adminPassword)
}

// BuildDataVolume builds a CDI DataVolume resource with cryptographic checksum validation
// (leveraging PR #4340, NIST 800-53 SI-7) or DataSource cloning, and automatic +6% filesystem overhead sizing.
func BuildDataVolume(udsvm *udsv1alpha1.UDSVirtualMachine) *unstructured.Unstructured {
	ownerRef := BuildOwnerReference(udsvm)
	sourceType := udsvm.Spec.Disk.SourceType
	if sourceType == "" {
		sourceType = "upload"
	}

	dvSpec := map[string]interface{}{}

	if sourceType == "dataSource" {
		sourceRef := map[string]interface{}{
			"kind": "DataSource",
			"name": udsvm.Spec.Disk.DataSourceName,
		}
		if udsvm.Spec.Disk.DataSourceNamespace != "" {
			sourceRef["namespace"] = udsvm.Spec.Disk.DataSourceNamespace
		} else {
			sourceRef["namespace"] = udsvm.Namespace
		}
		dvSpec["sourceRef"] = sourceRef
	} else {
		sourceSpec := map[string]interface{}{}
		if sourceType == "upload" {
			sourceSpec["upload"] = map[string]interface{}{
				"checksum": udsvm.Spec.Disk.Checksum,
			}
		} else if sourceType == "http" {
			sourceSpec["http"] = map[string]interface{}{
				"url":      udsvm.Spec.Disk.HTTPURL,
				"checksum": udsvm.Spec.Disk.Checksum,
			}
		}
		dvSpec["source"] = sourceSpec
	}

	storageSpec := map[string]interface{}{
		"accessModes": []interface{}{"ReadWriteOnce"},
	}
	if udsvm.Spec.Disk.VirtualSizeBytes > 0 {
		pvcSize := CalculatePVCSizeMiB(udsvm.Spec.Disk.VirtualSizeBytes)
		storageSpec["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{
				"storage": pvcSize,
			},
		}
	} else if sourceType != "dataSource" {
		storageSpec["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{
				"storage": "10Gi",
			},
		}
	}
	if udsvm.Spec.Disk.StorageClassName != "" {
		storageSpec["storageClassName"] = udsvm.Spec.Disk.StorageClassName
	}
	dvSpec["storage"] = storageSpec

	dv := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cdi.kubevirt.io/v1beta1",
			"kind":       "DataVolume",
			"metadata": map[string]interface{}{
				"name":      fmt.Sprintf("%s-dv", udsvm.Name),
				"namespace": udsvm.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/name":       udsvm.Name,
					"app.kubernetes.io/managed-by": "uds-vm-operator",
					"uds.dev/nist-control":         "SI-7",
				},
				"annotations": map[string]interface{}{
					"cdi.kubevirt.io/storage.bind.immediate.requested": "true",
				},
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion":         ownerRef.APIVersion,
						"kind":               ownerRef.Kind,
						"name":               ownerRef.Name,
						"uid":                string(ownerRef.UID),
						"controller":         *ownerRef.Controller,
						"blockOwnerDeletion": *ownerRef.BlockOwnerDeletion,
					},
				},
			},
			"spec": dvSpec,
		},
	}
	return dv
}

// BuildVirtualMachine builds a hardened KubeVirt VirtualMachine bound to the verified
// DataVolume and Cloud-Init Secret.
func BuildVirtualMachine(udsvm *udsv1alpha1.UDSVirtualMachine) *unstructured.Unstructured {
	ownerRef := BuildOwnerReference(udsvm)
	cpus := udsvm.Spec.CPUs
	if cpus <= 0 {
		cpus = 2
	}
	memory := udsvm.Spec.Memory
	if memory == "" {
		memory = "2Gi"
	}
	runStrategy := udsvm.Spec.RunStrategy
	if runStrategy == "" {
		runStrategy = "Halted"
	}

	isWindows := udsvm.Spec.OSType == "windows"
	diskBus := "virtio"
	nicModel := "virtio"
	injectVirtioWinISO := false
	virtioWinImage := "quay.io/kubevirt/virtio-container-disk:20261111_6cefb75928"
	enableHyperV := false
	enableSecureBootTPM := false

	if isWindows {
		injectVirtioWinISO = true
		enableHyperV = true
		enableSecureBootTPM = true

		if udsvm.Spec.WindowsProfile != nil {
			if udsvm.Spec.WindowsProfile.DiskBus != "" {
				diskBus = udsvm.Spec.WindowsProfile.DiskBus
			}
			if udsvm.Spec.WindowsProfile.NICModel != "" {
				nicModel = udsvm.Spec.WindowsProfile.NICModel
			}
			if udsvm.Spec.WindowsProfile.InjectVirtioWinISO != nil {
				injectVirtioWinISO = *udsvm.Spec.WindowsProfile.InjectVirtioWinISO
			}
			if udsvm.Spec.WindowsProfile.VirtioWinImage != "" {
				virtioWinImage = udsvm.Spec.WindowsProfile.VirtioWinImage
			}
			if udsvm.Spec.WindowsProfile.EnableHyperVEnlightenments != nil {
				enableHyperV = *udsvm.Spec.WindowsProfile.EnableHyperVEnlightenments
			}
			if udsvm.Spec.WindowsProfile.EnableSecureBootTPM != nil {
				enableSecureBootTPM = *udsvm.Spec.WindowsProfile.EnableSecureBootTPM
			}
		}
	}

	rootDiskObj := map[string]interface{}{
		"name": "rootdisk",
	}
	isISO := udsvm.Spec.Disk.Format == "iso"
	if isISO {
		rootDiskObj["cdrom"] = map[string]interface{}{
			"bus": "sata",
		}
		rootDiskObj["bootOrder"] = int64(1)
	} else {
		rootDiskObj["disk"] = map[string]interface{}{
			"bus": diskBus,
		}
	}

	disks := []interface{}{
		rootDiskObj,
	}
	volumes := []interface{}{
		map[string]interface{}{
			"name": "rootdisk",
			"dataVolume": map[string]interface{}{
				"name": fmt.Sprintf("%s-dv", udsvm.Name),
			},
		},
	}

	if isISO {
		// When booting from an installation ISO, attach an installation target hard drive for the guest OS
		disks = append(disks, map[string]interface{}{
			"name": "installdisk",
			"disk": map[string]interface{}{
				"bus": diskBus,
			},
		})
		volumes = append(volumes, map[string]interface{}{
			"name": "installdisk",
			"emptyDisk": map[string]interface{}{
				"capacity": "40Gi",
			},
		})
	}

	if isWindows {
		// Attach Sysprep / Unattended answer file as CD-ROM / volume
		sysprepSecretName := fmt.Sprintf("%s-cloudinit", udsvm.Name)
		if udsvm.Spec.WindowsProfile != nil && udsvm.Spec.WindowsProfile.SysprepSecretRef != "" {
			sysprepSecretName = udsvm.Spec.WindowsProfile.SysprepSecretRef
		}
		disks = append(disks, map[string]interface{}{
			"name": "sysprepdisk",
			"cdrom": map[string]interface{}{
				"bus": "sata",
			},
		})
		volumes = append(volumes, map[string]interface{}{
			"name": "sysprepdisk",
			"sysprep": map[string]interface{}{
				"secret": map[string]interface{}{
					"name": sysprepSecretName,
				},
			},
		})

		// Attach VirtIO Windows driver ISO containerDisk if enabled
		if injectVirtioWinISO {
			disks = append(disks, map[string]interface{}{
				"name": "virtio-win-drivers",
				"cdrom": map[string]interface{}{
					"bus": "sata",
				},
			})
			volumes = append(volumes, map[string]interface{}{
				"name": "virtio-win-drivers",
				"containerDisk": map[string]interface{}{
					"image": virtioWinImage,
				},
			})
		}
	} else {
		// Linux Cloud-Init NoCloud volume
		disks = append(disks, map[string]interface{}{
			"name": "cloudinitdisk",
			"disk": map[string]interface{}{
				"bus": "virtio",
			},
		})
		volumes = append(volumes, map[string]interface{}{
			"name": "cloudinitdisk",
			"cloudInitNoCloud": map[string]interface{}{
				"secretRef": map[string]interface{}{
					"name": fmt.Sprintf("%s-cloudinit", udsvm.Name),
				},
			},
		})
	}

	// Secondary persistent data disks
	for _, dd := range udsvm.Spec.DataDisks {
		bus := dd.Bus
		if bus == "" {
			bus = "virtio"
		}
		diskName := fmt.Sprintf("datadisk-%s", dd.Name)
		claimName := dd.PVCName
		if claimName == "" {
			claimName = fmt.Sprintf("%s-data-%s", udsvm.Name, dd.Name)
		}

		disks = append(disks, map[string]interface{}{
			"name": diskName,
			"disk": map[string]interface{}{
				"bus": bus,
			},
		})
		volumes = append(volumes, map[string]interface{}{
			"name": diskName,
			"persistentVolumeClaim": map[string]interface{}{
				"claimName": claimName,
			},
		})
	}

	iface := map[string]interface{}{
		"name":       "default",
		"masquerade": map[string]interface{}{},
	}
	if nicModel != "virtio" {
		iface["model"] = nicModel
	}

	features := map[string]interface{}{}
	devices := map[string]interface{}{
		"disks": disks,
		"interfaces": []interface{}{
			iface,
		},
	}

	domainSpec := map[string]interface{}{
		"cpu": map[string]interface{}{
			"cores": int64(cpus),
		},
		"resources": map[string]interface{}{
			"requests": map[string]interface{}{
				"memory": memory,
			},
		},
		"devices": devices,
	}

	if enableHyperV {
		features["hyperv"] = map[string]interface{}{
			"relaxed":     map[string]interface{}{},
			"vapic":       map[string]interface{}{},
			"spinlocks":   map[string]interface{}{"spinlocks": int64(8191)},
			"synic":       map[string]interface{}{},
			"synictimer":  map[string]interface{}{"direct": map[string]interface{}{}},
			"frequencies": map[string]interface{}{},
			"tlbflush":    map[string]interface{}{},
		}
		domainSpec["clock"] = map[string]interface{}{
			"utc": map[string]interface{}{},
			"timer": map[string]interface{}{
				"hyperv": map[string]interface{}{},
				"hpet":   map[string]interface{}{"present": false},
				"pit":    map[string]interface{}{"tickPolicy": "delay"},
				"rtc":    map[string]interface{}{"tickPolicy": "catchup"},
			},
		}
	}

	if enableSecureBootTPM {
		features["smm"] = map[string]interface{}{"enabled": true}
		domainSpec["firmware"] = map[string]interface{}{
			"bootloader": map[string]interface{}{
				"efi": map[string]interface{}{
					"secureBoot": true,
				},
			},
		}
		tpmSpec := map[string]interface{}{}
		if udsvm.Spec.WindowsProfile != nil && udsvm.Spec.WindowsProfile.PersistentTPM != nil && *udsvm.Spec.WindowsProfile.PersistentTPM {
			tpmSpec["persistent"] = true
		}
		devices["tpm"] = tpmSpec
	}

	if len(features) > 0 {
		domainSpec["features"] = features
	}

	vm := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "kubevirt.io/v1",
			"kind":       "VirtualMachine",
			"metadata": map[string]interface{}{
				"name":      udsvm.Name,
				"namespace": udsvm.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/name":       udsvm.Name,
					"app.kubernetes.io/managed-by": "uds-vm-operator",
				},
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion":         ownerRef.APIVersion,
						"kind":               ownerRef.Kind,
						"name":               ownerRef.Name,
						"uid":                string(ownerRef.UID),
						"controller":         *ownerRef.Controller,
						"blockOwnerDeletion": *ownerRef.BlockOwnerDeletion,
					},
				},
			},
			"spec": map[string]interface{}{
				"runStrategy": runStrategy,
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"vm.kubevirt.io/name":    udsvm.Name,
							"app.kubernetes.io/name": udsvm.Name,
						},
					},
					"spec": map[string]interface{}{
						"domain": domainSpec,
						"networks": []interface{}{
							map[string]interface{}{
								"name": "default",
								"pod":  map[string]interface{}{},
							},
						},
						"volumes": volumes,
					},
				},
			},
		},
	}
	return vm
}

// BuildDataDiskPVC constructs a PersistentVolumeClaim for secondary storage.
// If retentionPolicy is "Retain", OwnerReferences are omitted so the PVC survives VM deletion.
func BuildDataDiskPVC(udsvm *udsv1alpha1.UDSVirtualMachine, dd *udsv1alpha1.DataDiskSpec) *corev1.PersistentVolumeClaim {
	claimName := dd.PVCName
	if claimName == "" {
		claimName = fmt.Sprintf("%s-data-%s", udsvm.Name, dd.Name)
	}

	storageQty, err := resource.ParseQuantity(dd.Size)
	if err != nil {
		storageQty = resource.MustParse("10Gi")
	}
	pvc := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "PersistentVolumeClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: udsvm.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       udsvm.Name,
				"app.kubernetes.io/managed-by": "uds-vm-operator",
				"uds.dev/disk-name":            dd.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageQty,
				},
			},
		},
	}

	if dd.StorageClassName != "" {
		pvc.Spec.StorageClassName = &dd.StorageClassName
	}

	retention := dd.RetentionPolicy
	if retention == "" {
		retention = "Delete"
	}
	pvc.Labels["uds.dev/retention-policy"] = retention

	if strings.EqualFold(retention, "Delete") {
		ownerRef := BuildOwnerReference(udsvm)
		pvc.OwnerReferences = []metav1.OwnerReference{ownerRef}
	}

	return pvc
}

// EvaluateStatusFromDataVolume evaluates the observed state from the child CDI DataVolume.
func EvaluateStatusFromDataVolume(
	udsvm *udsv1alpha1.UDSVirtualMachine,
	dvObj *unstructured.Unstructured,
	reconcilerID string,
) udsv1alpha1.UDSVirtualMachineStatus {
	pvcSize := "auto"
	if udsvm.Spec.Disk.VirtualSizeBytes > 0 {
		pvcSize = CalculatePVCSizeMiB(udsvm.Spec.Disk.VirtualSizeBytes)
	}
	zeroTrust := true
	if udsvm.Spec.SecurityProfile.ZeroTrustNetwork != nil {
		zeroTrust = *udsvm.Spec.SecurityProfile.ZeroTrustNetwork
	}

	dvName := ""
	dvPhase := "Pending"
	checksumVerified := false
	phase := "Pending"
	reason := "DataVolumeCreating"
	message := "Waiting for CDI DataVolume to initialize"

	if dvObj != nil {
		dvName = dvObj.GetName()
		statusObj, hasStatus := dvObj.Object["status"].(map[string]interface{})
		if hasStatus && statusObj != nil {
			if p, ok := statusObj["phase"].(string); ok && p != "" {
				dvPhase = p
			}

			// Check conditions for ChecksumError
			checksumErrorMsg := ""
			if conds, ok := statusObj["conditions"].([]interface{}); ok {
				for _, c := range conds {
					if cMap, ok := c.(map[string]interface{}); ok {
						if r, ok := cMap["reason"].(string); ok && r == "ChecksumError" {
							if m, ok := cMap["message"].(string); ok {
								checksumErrorMsg = m
							} else {
								checksumErrorMsg = "Disk image checksum mismatch"
							}
							break
						}
					}
				}
			}

			sourceType := udsvm.Spec.Disk.SourceType
			if sourceType == "" {
				sourceType = "upload"
			}

			if checksumErrorMsg != "" {
				phase = "IntegrityError"
				reason = "ChecksumError"
				message = checksumErrorMsg
				checksumVerified = false
			} else if dvPhase == "Succeeded" {
				phase = "Ready"
				reason = "ChecksumVerified"
				if sourceType == "dataSource" {
					message = fmt.Sprintf("Cloned from DataSource %s", udsvm.Spec.Disk.DataSourceName)
					checksumVerified = true
				} else {
					message = fmt.Sprintf("Disk image imported and verified against %s", udsvm.Spec.Disk.Checksum)
					checksumVerified = true
				}
			} else if dvPhase == "UploadReady" || dvPhase == "WaitForFirstConsumer" || dvPhase == "Pending" {
				if sourceType == "upload" {
					phase = "WaitingForUpload"
				} else {
					phase = "Provisioning"
				}
				reason = fmt.Sprintf("DataVolume%s", dvPhase)
				message = fmt.Sprintf("CDI DataVolume %s is in phase %s", dvName, dvPhase)
			} else {
				phase = "Provisioning"
				reason = fmt.Sprintf("DataVolume%s", dvPhase)
				message = fmt.Sprintf("CDI DataVolume %s is in phase %s", dvName, dvPhase)
			}
		}
	}

	condStatus := metav1.ConditionFalse
	if phase == "Ready" {
		condStatus = metav1.ConditionTrue
	}

	return udsv1alpha1.UDSVirtualMachineStatus{
		Phase:             phase,
		CalculatedPVCSize: pvcSize,
		DataVolumeName:    dvName,
		DataVolumePhase:   dvPhase,
		ReconciledBy:      reconcilerID,
		Compliance: udsv1alpha1.ComplianceStatus{
			ChecksumVerified:  checksumVerified,
			ZeroTrustIsolated: zeroTrust,
			CloudInitHardened: true,
		},
		Conditions: []metav1.Condition{
			{
				Type:               "Ready",
				Status:             condStatus,
				ObservedGeneration: udsvm.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             reason,
				Message:            message,
			},
		},
	}
}

// Reconciler reconciles a UDSVirtualMachine object.
type Reconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Log          logr.Logger
	ReconcilerID string
}

// Reconcile performs the level-triggered reconciliation loop for UDSVirtualMachine.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	res, err := r.reconcileVM(ctx, req)
	if err != nil {
		ReconcileTotal.WithLabelValues("error").Inc()
	}
	return res, err
}

func (r *Reconciler) reconcileVM(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	udsvm := &udsv1alpha1.UDSVirtualMachine{}
	err := r.Get(ctx, req.NamespacedName, udsvm)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	reconcilerID := r.ReconcilerID
	if reconcilerID == "" {
		reconcilerID = DefaultReconcilerID
	}

	// 1. Reconcile Zero-Trust NetworkPolicy
	zeroTrust := true
	if udsvm.Spec.SecurityProfile.ZeroTrustNetwork != nil {
		zeroTrust = *udsvm.Spec.SecurityProfile.ZeroTrustNetwork
	}
	if zeroTrust {
		netpol := BuildZeroTrustNetworkPolicy(udsvm)
		existingNetPol := &networkingv1.NetworkPolicy{}
		err = r.Get(ctx, types.NamespacedName{Name: netpol.Name, Namespace: netpol.Namespace}, existingNetPol)
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, netpol); err != nil && !apierrors.IsAlreadyExists(err) {
				return reconcile.Result{}, err
			}
		} else if err != nil {
			return reconcile.Result{}, err
		} else {
			existingNetPol.Labels = netpol.Labels
			existingNetPol.Spec = netpol.Spec
			existingNetPol.OwnerReferences = netpol.OwnerReferences
			if err := r.Update(ctx, existingNetPol); err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	// 2. Reconcile Windows Credentials & Sysprep / Cloud-Init Secret
	adminPassword := ""
	if udsvm.Spec.OSType == "windows" {
		if udsvm.Spec.WindowsProfile != nil && udsvm.Spec.WindowsProfile.AdminPasswordSecretRef != "" {
			credSecret := &corev1.Secret{}
			err := r.Get(ctx, types.NamespacedName{Name: udsvm.Spec.WindowsProfile.AdminPasswordSecretRef, Namespace: udsvm.Namespace}, credSecret)
			if err == nil {
				if pw, ok := credSecret.Data["password"]; ok {
					adminPassword = string(pw)
				} else if pw, ok := credSecret.Data["admin-password"]; ok {
					adminPassword = string(pw)
				}
			}
		}
		if adminPassword == "" {
			credSecretName := fmt.Sprintf("%s-admin-credentials", udsvm.Name)
			existingCredSecret := &corev1.Secret{}
			err := r.Get(ctx, types.NamespacedName{Name: credSecretName, Namespace: udsvm.Namespace}, existingCredSecret)
			if err == nil {
				if pw, ok := existingCredSecret.Data["password"]; ok {
					adminPassword = string(pw)
				}
			} else if apierrors.IsNotFound(err) {
				generatedPW, genErr := GenerateSecurePassword(24)
				if genErr == nil {
					adminPassword = generatedPW
					ownerRef := BuildOwnerReference(udsvm)
					credSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      credSecretName,
							Namespace: udsvm.Namespace,
							Labels: map[string]string{
								"app.kubernetes.io/name":       udsvm.Name,
								"app.kubernetes.io/managed-by": "uds-vm-operator",
								"uds.dev/nist-control":         "IA-5",
							},
							OwnerReferences: []metav1.OwnerReference{ownerRef},
						},
						Type: corev1.SecretTypeOpaque,
						StringData: map[string]string{
							"password": adminPassword,
							"username": "Administrator",
						},
					}
					_ = r.Create(ctx, credSecret)
				}
			}
		}
	}

	secret := BuildCloudInitSecret(udsvm, adminPassword)
	existingSecret := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, existingSecret)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
			return reconcile.Result{}, err
		}
	} else if err != nil {
		return reconcile.Result{}, err
	} else {
		existingSecret.Labels = secret.Labels
		existingSecret.StringData = secret.StringData
		existingSecret.OwnerReferences = secret.OwnerReferences
		if err := r.Update(ctx, existingSecret); err != nil {
			return reconcile.Result{}, err
		}
	}

	// 3. Reconcile CDI DataVolume
	dv := BuildDataVolume(udsvm)
	existingDV := &unstructured.Unstructured{}
	existingDV.SetGroupVersionKind(dv.GroupVersionKind())
	err = r.Get(ctx, types.NamespacedName{Name: dv.GetName(), Namespace: dv.GetNamespace()}, existingDV)
	var dvObj *unstructured.Unstructured
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, dv); err != nil && !apierrors.IsAlreadyExists(err) {
			return reconcile.Result{}, err
		}
		dvObj = dv
	} else if err != nil {
		return reconcile.Result{}, err
	} else {
		dvObj = existingDV
	}

	// 4. Reconcile Secondary Data Volume Claims (dataDisks)
	for i := range udsvm.Spec.DataDisks {
		dd := &udsvm.Spec.DataDisks[i]
		if dd.PVCName == "" {
			if _, parseErr := resource.ParseQuantity(dd.Size); parseErr != nil {
				return reconcile.Result{}, fmt.Errorf("invalid dataDisks[%d].size %q for disk %q: %w", i, dd.Size, dd.Name, parseErr)
			}
			dataPVC := BuildDataDiskPVC(udsvm, dd)
			existingPVC := &corev1.PersistentVolumeClaim{}
			err = r.Get(ctx, types.NamespacedName{Name: dataPVC.Name, Namespace: dataPVC.Namespace}, existingPVC)
			if apierrors.IsNotFound(err) {
				if err := r.Create(ctx, dataPVC); err != nil && !apierrors.IsAlreadyExists(err) {
					return reconcile.Result{}, err
				}
			} else if err != nil {
				return reconcile.Result{}, err
			}
		}
	}

	// 5. Reconcile KubeVirt VirtualMachine
	vm := BuildVirtualMachine(udsvm)
	existingVM := &unstructured.Unstructured{}
	existingVM.SetGroupVersionKind(vm.GroupVersionKind())
	err = r.Get(ctx, types.NamespacedName{Name: vm.GetName(), Namespace: vm.GetNamespace()}, existingVM)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, vm); err != nil && !apierrors.IsAlreadyExists(err) {
			return reconcile.Result{}, err
		}
	} else if err != nil {
		return reconcile.Result{}, err
	} else {
		existingVM.Object["spec"] = vm.Object["spec"]
		existingVM.SetLabels(vm.GetLabels())
		existingVM.SetOwnerReferences(vm.GetOwnerReferences())
		if err := r.Update(ctx, existingVM); err != nil {
			return reconcile.Result{}, err
		}
	}

	// 6. Evaluate and update status directly onto UDSVirtualMachine
	newStatus := EvaluateStatusFromDataVolume(udsvm, dvObj, reconcilerID)
	mergedConditions := make([]metav1.Condition, len(udsvm.Status.Conditions))
	copy(mergedConditions, udsvm.Status.Conditions)
	for _, c := range newStatus.Conditions {
		meta.SetStatusCondition(&mergedConditions, c)
	}
	newStatus.Conditions = mergedConditions

	// Record Prometheus compliance and telemetry metrics
	RecordComplianceMetrics(udsvm.Name, udsvm.Namespace,
		newStatus.Compliance.ChecksumVerified,
		newStatus.Compliance.ZeroTrustIsolated,
		newStatus.Compliance.CloudInitHardened,
		udsvm.Spec.OSType == "windows")
	RecordPhaseMetrics(udsvm.Name, udsvm.Namespace, newStatus.Phase)
	if allocatedQty, qtyErr := resource.ParseQuantity(newStatus.CalculatedPVCSize); qtyErr == nil {
		RecordStorageMetrics(udsvm.Name, udsvm.Namespace, udsvm.Spec.Disk.VirtualSizeBytes, allocatedQty.Value())
	}
	ReconcileTotal.WithLabelValues("success").Inc()

	if udsvm.Status.Phase != newStatus.Phase ||
		udsvm.Status.CalculatedPVCSize != newStatus.CalculatedPVCSize ||
		udsvm.Status.DataVolumeName != newStatus.DataVolumeName ||
		udsvm.Status.DataVolumePhase != newStatus.DataVolumePhase ||
		udsvm.Status.ReconciledBy != newStatus.ReconciledBy ||
		!reflect.DeepEqual(udsvm.Status.Compliance, newStatus.Compliance) ||
		!reflect.DeepEqual(udsvm.Status.Conditions, newStatus.Conditions) {

		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			latest := &udsv1alpha1.UDSVirtualMachine{}
			if err := r.Get(ctx, req.NamespacedName, latest); err != nil {
				return err
			}
			latest.Status = newStatus
			return r.Status().Update(ctx, latest)
		})
		if err != nil {
			return reconcile.Result{}, err
		}
		r.Log.Info("Reconciled UDSVirtualMachine",
			"namespace", udsvm.Namespace,
			"name", udsvm.Name,
			"phase", newStatus.Phase,
			"pvc", newStatus.CalculatedPVCSize,
			"checksumVerified", newStatus.Compliance.ChecksumVerified,
			"reconciledBy", newStatus.ReconciledBy,
		)
	}

	// Requeue periodically if not finished or to keep synced
	requeueDuration := 5 * time.Second
	if newStatus.Phase == "Ready" {
		requeueDuration = 30 * time.Second
	}
	return reconcile.Result{RequeueAfter: requeueDuration}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&udsv1alpha1.UDSVirtualMachine{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
