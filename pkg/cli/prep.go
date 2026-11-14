package cli

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/celttechie/uds-vm-operator/pkg/controller"
)

// QCOW2 Magic Number: 0x514649fb ('Q', 'F', 'I', 0xfb)
var qcow2Magic = []byte{0x51, 0x46, 0x49, 0xfb}

// DiskInfo contains extracted disk geometry, cryptographic digest, and sizing calculations.
type DiskInfo struct {
	Path                 string `json:"path"`
	Filename             string `json:"filename"`
	Format               string `json:"format"` // "raw" or "qcow2"
	VirtualSizeBytes     int64  `json:"virtualSizeBytes"`
	ActualSizeBytes      int64  `json:"actualSizeBytes"`
	CalculatedPVCSize    string `json:"calculatedPvcSize"`
	ChecksumSHA256       string `json:"checksumSha256"`
	HasBackingFile       bool   `json:"hasBackingFile"`
	BackingFilePath      string `json:"backingFilePath,omitempty"`
}

// InspectDiskImage reads RAW and QCOW2 image headers natively in Go, enforces air-gap
// flattened image requirements, calculates +6% PVC allocation math, and streams the SHA-256 digest.
func InspectDiskImage(path string) (*DiskInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open disk image %q: %w", path, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat disk image %q: %w", path, err)
	}
	actualSize := fi.Size()

	// Read first 512 bytes for header inspection
	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read disk image header: %w", err)
	}

	info := &DiskInfo{
		Path:            path,
		Filename:        filepath.Base(path),
		ActualSizeBytes: actualSize,
	}

	// Detect QCOW2 format
	if n >= 32 && string(header[0:4]) == string(qcow2Magic) {
		info.Format = "qcow2"

		// QCOW2 Header layout (big-endian):
		// bytes 4-7: uint32 version
		// bytes 8-15: uint64 backing_file_offset
		// bytes 16-19: uint32 backing_file_size
		// bytes 24-31: uint64 size (virtual disk size in bytes)
		backingOffset := binary.BigEndian.Uint64(header[8:16])
		backingSize := binary.BigEndian.Uint32(header[16:20])
		virtualSize := binary.BigEndian.Uint64(header[24:32])
		info.VirtualSizeBytes = int64(virtualSize)

		// Strict Air-Gap Verification: External backing chains break disconnected enclaves!
		if backingOffset > 0 && backingSize > 0 {
			info.HasBackingFile = true
			backingPathBuf := make([]byte, backingSize)
			if _, err := f.Seek(int64(backingOffset), io.SeekStart); err == nil {
				_, _ = f.Read(backingPathBuf)
				info.BackingFilePath = string(backingPathBuf)
			}
			return nil, fmt.Errorf("air-gap integrity violation: external backing file detected (%q). "+
				"Golden and edge VM images must be standalone and flattened ('qemu-img convert -O qcow2') "+
				"for air-gapped / disconnected deployment", info.BackingFilePath)
		}
	} else {
		// Default to RAW image
		info.Format = "raw"
		info.VirtualSizeBytes = actualSize
	}

	// Calculate deterministic +6% filesystem padding math (reconciler.CalculatePVCSizeMiB)
	info.CalculatedPVCSize = controller.CalculatePVCSizeMiB(info.VirtualSizeBytes)

	// Stream SHA-256 hash
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to rewind file for checksum: %w", err)
	}

	hasher := sha256.New()
	buf := make([]byte, 1024*1024) // 1MB buffer
	if _, err := io.CopyBuffer(hasher, f, buf); err != nil {
		return nil, fmt.Errorf("failed to compute SHA-256 checksum: %w", err)
	}

	info.ChecksumSHA256 = fmt.Sprintf("sha256:%x", hasher.Sum(nil))
	return info, nil
}

// GenerateUDSVirtualMachineYAML outputs a declarative Kubernetes manifest for the inspected image.
func GenerateUDSVirtualMachineYAML(info *DiskInfo, vmName, osType string, isGoldenImage bool) string {
	return GenerateUDSVirtualMachineYAMLWithDataSource(info, vmName, osType, isGoldenImage, "", "")
}

// DefaultResourceName derives a Kubernetes-safe resource name from an image filename.
func DefaultResourceName(filename string) string {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	return strings.ToLower(strings.ReplaceAll(base, "_", "-"))
}

// GenerateUDSVirtualMachineYAMLWithDataSource outputs a declarative Kubernetes manifest targeting an optional DataSource.
func GenerateUDSVirtualMachineYAMLWithDataSource(info *DiskInfo, vmName, osType string, isGoldenImage bool, dsName, dsNamespace string) string {
	if vmName == "" {
		vmName = DefaultResourceName(info.Filename)
	}
	if osType == "" {
		osType = "linux"
	}

	sourceType := "upload"
	dataSourceLines := ""
	if isGoldenImage || dsName != "" {
		sourceType = "dataSource"
		if dsName == "" {
			dsName = vmName + "-golden"
		}
		dataSourceLines = fmt.Sprintf("    dataSourceName: %s\n", dsName)
		if dsNamespace != "" {
			dataSourceLines += fmt.Sprintf("    dataSourceNamespace: %s\n", dsNamespace)
		}
	}

	return fmt.Sprintf(`apiVersion: uds.dev/v1alpha1
kind: UDSVirtualMachine
metadata:
  name: %s
  namespace: default
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/managed-by: uds-vmctl
    uds.dev/security-tier: mission-critical
  annotations:
    uds.dev/air-gap-verified: "true"
spec:
  osType: %s
  cpus: 2
  memory: 4Gi
  runStrategy: Always
  disk:
    sourceType: %s
%s    format: %s
    virtualSizeBytes: %d
    checksum: %s
  securityProfile:
    zeroTrustNetwork: true
    disallowSSHPassword: true
    fipsMode: true
`, vmName, vmName, osType, sourceType, dataSourceLines, info.Format, info.VirtualSizeBytes, info.ChecksumSHA256)
}

// GenerateCDIDataSourceYAML outputs the central CDI DataVolume and DataSource manifests for the golden image.
func GenerateCDIDataSourceYAML(info *DiskInfo, sourceName, namespace string) string {
	if sourceName == "" {
		sourceName = DefaultResourceName(info.Filename)
	}
	if namespace == "" {
		namespace = "uds-golden-images"
	}

	return fmt.Sprintf(`apiVersion: cdi.kubevirt.io/v1beta1
kind: DataVolume
metadata:
  name: %s
  namespace: %s
  labels:
    uds.dev/golden-image: "true"
    uds.dev/nist-control: "SI-7"
  annotations:
    cdi.kubevirt.io/storage.bind.immediate.requested: "true"
spec:
  source:
    upload:
      checksum: %s
  storage:
    accessModes:
      - ReadWriteOnce
    resources:
      requests:
        storage: %s
---
apiVersion: cdi.kubevirt.io/v1beta1
kind: DataSource
metadata:
  name: %s
  namespace: %s
  labels:
    uds.dev/golden-image: "true"
spec:
  source:
    pvc:
      name: %s
      namespace: %s
`, sourceName, namespace, info.ChecksumSHA256, info.CalculatedPVCSize, sourceName, namespace, sourceName, namespace)
}

// GenerateZarfComponentYAML outputs the Zarf component snippet for offline package inclusion.
func GenerateZarfComponentYAML(info *DiskInfo, componentName, localPath string) string {
	if componentName == "" {
		componentName = "golden-image"
	}
	if localPath == "" {
		localPath = info.Path
	}

	return fmt.Sprintf(`  - name: %s
    description: "Pre-baked air-gap golden image (%s)"
    files:
      - source: %s
        target: /var/uds/images/%s
        shasum: %s
`, componentName, info.Filename, localPath, info.Filename, strings.TrimPrefix(info.ChecksumSHA256, "sha256:"))
}
