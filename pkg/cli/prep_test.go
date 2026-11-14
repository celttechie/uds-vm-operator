package cli

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectDiskImage_RAW(t *testing.T) {
	tmpDir := t.TempDir()
	rawPath := filepath.Join(tmpDir, "test-golden.raw")

	// Create 10MB test file with known content
	data := make([]byte, 10*1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(rawPath, data, 0644); err != nil {
		t.Fatalf("failed to create test raw image: %v", err)
	}

	expectedHasher := sha256.New()
	expectedHasher.Write(data)
	expectedHash := fmt.Sprintf("sha256:%x", expectedHasher.Sum(nil))

	info, err := InspectDiskImage(rawPath)
	if err != nil {
		t.Fatalf("InspectDiskImage failed on raw image: %v", err)
	}

	if info.Format != "raw" {
		t.Errorf("expected format 'raw', got %q", info.Format)
	}
	if info.VirtualSizeBytes != int64(len(data)) {
		t.Errorf("expected virtual size %d, got %d", len(data), info.VirtualSizeBytes)
	}
	if info.ActualSizeBytes != int64(len(data)) {
		t.Errorf("expected actual size %d, got %d", len(data), info.ActualSizeBytes)
	}
	if info.ChecksumSHA256 != expectedHash {
		t.Errorf("expected hash %q, got %q", expectedHash, info.ChecksumSHA256)
	}
	// 10MB = 10 MiB; 10 * 1.06 = 10.6 -> ceil is 11Mi
	if info.CalculatedPVCSize != "11Mi" {
		t.Errorf("expected calculated PVC size '11Mi', got %q", info.CalculatedPVCSize)
	}
}

func TestInspectDiskImage_QCOW2_Standalone(t *testing.T) {
	tmpDir := t.TempDir()
	qcow2Path := filepath.Join(tmpDir, "standalone.qcow2")

	// Build a valid synthetic QCOW2 v3 header (72 bytes minimum)
	// Layout:
	// 0..3: magic (0x51, 0x46, 0x49, 0xfb)
	// 4..7: version (uint32: 3)
	// 8..15: backing_file_offset (uint64: 0)
	// 16..19: backing_file_size (uint32: 0)
	// 20..23: cluster_bits (uint32: 16 -> 64KB)
	// 24..31: size (uint64: 20 GiB = 21474836480 bytes)
	header := make([]byte, 512)
	copy(header[0:4], qcow2Magic)
	binary.BigEndian.PutUint32(header[4:8], 3)
	binary.BigEndian.PutUint64(header[8:16], 0)
	binary.BigEndian.PutUint32(header[16:20], 0)
	binary.BigEndian.PutUint32(header[20:24], 16)
	virtualSize := uint64(20 * 1024 * 1024 * 1024) // 20 GiB
	binary.BigEndian.PutUint64(header[24:32], virtualSize)

	if err := os.WriteFile(qcow2Path, header, 0644); err != nil {
		t.Fatalf("failed to write synthetic qcow2: %v", err)
	}

	info, err := InspectDiskImage(qcow2Path)
	if err != nil {
		t.Fatalf("InspectDiskImage failed on standalone qcow2: %v", err)
	}

	if info.Format != "qcow2" {
		t.Errorf("expected format 'qcow2', got %q", info.Format)
	}
	if info.VirtualSizeBytes != int64(virtualSize) {
		t.Errorf("expected virtual size %d, got %d", virtualSize, info.VirtualSizeBytes)
	}
	if info.HasBackingFile {
		t.Errorf("expected no backing file for standalone image")
	}
	// 20 GiB = 20480 MiB; 20480 * 1.06 = 21708.8 -> 21709Mi
	expectedPVC := "21709Mi"
	if info.CalculatedPVCSize != expectedPVC {
		t.Errorf("expected calculated PVC size %q, got %q", expectedPVC, info.CalculatedPVCSize)
	}
}

func TestInspectDiskImage_QCOW2_ExternalBackingFileRejection(t *testing.T) {
	tmpDir := t.TempDir()
	qcow2Path := filepath.Join(tmpDir, "chained.qcow2")

	backingFile := "/var/images/base-ubuntu-22.04.qcow2"
	backingOffset := uint64(128)
	backingSize := uint32(len(backingFile))

	header := make([]byte, 512)
	copy(header[0:4], qcow2Magic)
	binary.BigEndian.PutUint32(header[4:8], 3)
	binary.BigEndian.PutUint64(header[8:16], backingOffset)
	binary.BigEndian.PutUint32(header[16:20], backingSize)
	binary.BigEndian.PutUint32(header[20:24], 16)
	binary.BigEndian.PutUint64(header[24:32], uint64(10*1024*1024*1024))

	// Embed backing file path at offset 128
	copy(header[backingOffset:backingOffset+uint64(backingSize)], []byte(backingFile))

	if err := os.WriteFile(qcow2Path, header, 0644); err != nil {
		t.Fatalf("failed to write chained qcow2: %v", err)
	}

	_, err := InspectDiskImage(qcow2Path)
	if err == nil {
		t.Fatalf("expected error due to air-gap external backing file violation, got nil")
	}

	expectedSubstr := "air-gap integrity violation: external backing file detected"
	if !strings.Contains(err.Error(), expectedSubstr) {
		t.Errorf("expected error message containing %q, got %q", expectedSubstr, err.Error())
	}
}

func TestGenerateUDSVirtualMachineYAML(t *testing.T) {
	info := &DiskInfo{
		Filename:             "tactical-sensor-vm.qcow2",
		Format:               "qcow2",
		VirtualSizeBytes:     21474836480,
		CalculatedPVCSize:    "21709Mi",
		ChecksumSHA256:       "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}

	// Test upload-based Linux VM
	yamlUpload := GenerateUDSVirtualMachineYAML(info, "tactical-sensor-vm", "linux", false)
	if !strings.Contains(yamlUpload, "sourceType: upload") {
		t.Errorf("expected sourceType upload, got:\n%s", yamlUpload)
	}
	if !strings.Contains(yamlUpload, "zeroTrustNetwork: true") {
		t.Errorf("expected zeroTrustNetwork true in manifest")
	}
	if !strings.Contains(yamlUpload, "checksum: sha256:e3b0c44298") {
		t.Errorf("expected sha256 checksum in manifest")
	}

	// Test Golden Image cloned Windows VM
	yamlGolden := GenerateUDSVirtualMachineYAML(info, "tactical-win-vm", "windows", true)
	if !strings.Contains(yamlGolden, "sourceType: dataSource") {
		t.Errorf("expected sourceType dataSource for golden image, got:\n%s", yamlGolden)
	}
	if !strings.Contains(yamlGolden, "dataSourceName: tactical-win-vm-golden") {
		t.Errorf("expected dataSourceName in manifest, got:\n%s", yamlGolden)
	}
	if !strings.Contains(yamlGolden, "osType: windows") {
		t.Errorf("expected osType windows, got:\n%s", yamlGolden)
	}
	if !strings.Contains(yamlGolden, "provisioningMode: clone") {
		t.Errorf("expected provisioningMode clone for Windows golden image, got:\n%s", yamlGolden)
	}
	if !strings.Contains(yamlGolden, "injectVirtioWinISO: false") {
		t.Errorf("expected injectVirtioWinISO false for Windows golden image, got:\n%s", yamlGolden)
	}
}

func TestGenerateCDIDataSourceYAML(t *testing.T) {
	info := &DiskInfo{
		Filename:          "ubuntu-2204.raw",
		CalculatedPVCSize: "11Mi",
		ChecksumSHA256:    "sha256:abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
	}

	dsYAML := GenerateCDIDataSourceYAML(info, "ubuntu-2204-golden", "uds-golden-images")
	if !strings.Contains(dsYAML, "kind: DataVolume") || !strings.Contains(dsYAML, "kind: DataSource") {
		t.Errorf("expected DataVolume and DataSource in generated YAML, got:\n%s", dsYAML)
	}
	if !strings.Contains(dsYAML, "name: ubuntu-2204-golden") {
		t.Errorf("expected name ubuntu-2204-golden in manifest")
	}
	if !strings.Contains(dsYAML, "checksum: sha256:abcd1234") {
		t.Errorf("expected checksum in manifest")
	}
}

func TestGenerateZarfComponentYAML(t *testing.T) {
	info := &DiskInfo{
		Filename:       "tactical-linux.raw",
		Path:           "/opt/images/tactical-linux.raw",
		ChecksumSHA256: "sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}

	zarfSnippet := GenerateZarfComponentYAML(info, "golden-image", "")
	if !strings.Contains(zarfSnippet, "- name: golden-image") {
		t.Errorf("expected component name 'golden-image'")
	}
	if !strings.Contains(zarfSnippet, "source: /opt/images/tactical-linux.raw") {
		t.Errorf("expected source path in zarf snippet")
	}
	if !strings.Contains(zarfSnippet, "shasum: 1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef") {
		t.Errorf("expected raw hex shasum without sha256: prefix, got:\n%s", zarfSnippet)
	}
}

func TestDefaultResourceName(t *testing.T) {
	if got := DefaultResourceName("Win_2022_Golden.qcow2"); got != "win-2022-golden" {
		t.Errorf("expected 'win-2022-golden', got %q", got)
	}
}
