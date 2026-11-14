# Architecture Proposal: Golden Images, Rolling Fleet Updates & Storage Persistence

**Author:** Brian Jarrett  
**Status:** Implemented (v0.5.0 / v0.5.1)  
**Target Release:** v0.5.0  
**Repository:** [uds-vm-operator](..)

---

## 1. Executive Summary & Goals

This document outlines the architectural enhancements required to transition `uds-vm-operator` from single-instance validation to automated, production-grade fleet management.

The primary objective is to allow mission operators to deploy and update fleets of virtual machines seamlessly, ensuring continuous service availability while maintaining strict data integrity and DoD compliance.

### Key Objectives:
1. **Production Golden Images:** Build, inspect, and verify standardized, air-gapped golden images for both Linux (Ubuntu 22.04 LTS / Rocky 9) and Windows Server 2022 using the native `uds-vmctl prep` utility.
2. **Centralized Image Caching & Fast Cloning:** Import and cryptographically verify (NIST SP 800-53 SI-7) golden images once into cluster storage via Containerized Data Importer (CDI) `DataSource` objects. Subsequent virtual machines instantiate in seconds using storage-layer Copy-on-Write (CoW) clones without re-uploading large disk images.
3. **Dual Storage Architecture:**
   * **Stateless Workloads:** Disposable, immutable virtual machines where the operating system and runtimes can be replaced or destroyed on demand.
   * **Stateful Workloads:** Mission-critical services (such as databases and directory services) where application data is strictly preserved across operating system updates via decoupled secondary storage volumes.
4. **Zero-Downtime Rolling Updates:** Orchestrate coordinated, automated rolling updates across VM pools (`maxSurge: 1`, `maxUnavailable: 0`) governed by guest readiness probes, preventing service outages during OS upgrades and kernel patching.

---

## 2. Target Architecture

```
                                  ┌─────────────────────────────────────────┐
                                  │           CDI DataSource                │
                                  │  (Golden OS Image: Verified SHA-256)    │
                                  └────────────────────┬────────────────────┘
                                                       │
                           ┌───────────────────────────┴───────────────────────────┐
                           │ Fast Copy-on-Write Clones                             │
                           ▼                                                       ▼
            ┌─────────────────────────────┐                         ┌─────────────────────────────┐
            │       STATELESS FLEET       │                         │       STATEFUL FLEET        │
            │      (Disposable OS)        │                         │  (Decoupled Persistent Data)│
            └──────────────┬──────────────┘                         └──────────────┬──────────────┘
                           │                                                       │
                           ▼                                                       ▼
            ┌─────────────────────────────┐                         ┌─────────────────────────────┐
            │        VM Instance          │                         │        VM Instance          │
            │  ┌───────────────────────┐  │                         │  ┌───────────────────────┐  │
            │  │ C: / / Root (CoW)     │  │                         │  │ C: / / Root (CoW)     │  │
            │  │ (Ephemeral across     │  │                         │  │ (Swappable on upgrade)│  │
            │  │  OS upgrades)         │  │                         │  └───────────────────────┘  │
            │  └───────────────────────┘  │                         │  ┌───────────────────────┐  │
            └─────────────────────────────┘                         │  │ D: / /data (PVC)      │  │
                                                                    │  │ (PERSISTENT DATA)     │  │
                                                                    │  └───────────────────────┘  │
                                                                    └─────────────────────────────┘
```

---

## 3. Storage Persistence Model

### 3.1. Profile A: Stateless / Immutable VMs
* **Use Case:** Tactical sensor data collectors, ingress reverse proxies, ephemeral build workers, and zero-trust edge compute.
* **Storage Topology:** Single root disk created from a CDI `DataSource` clone.
* **Upgrade Behavior:** Completely disposable. When the golden image updates, old VM instances are terminated and new instances are created. Configuration is re-applied cleanly via Cloud-Init or Sysprep.

### 3.2. Profile B: Stateful VMs with Decoupled Data Drives
* **Use Case:** Domain Controllers, SQL/PostgreSQL databases, local telemetry archives, and stateful enterprise Windows/Linux services.
* **Storage Topology:**
  1. **Root OS Drive (`rootdisk` / `C:` or `/`):** Cloned from the golden image `DataSource`. This drive contains only the operating system and base runtimes.
  2. **Data Drive (`datadisk` / `D:` or `/var/data`):** Independent, long-lived `PersistentVolumeClaim` (PVC) bound to high-performance block or filesystem storage.
* **Upgrade Behavior:** During a rolling OS upgrade:
  1. Traffic is drained / services are stopped cleanly.
  2. The persistent data PVC is detached from the old VM instance.
  3. A new VM instance with the updated golden OS image boots up.
  4. The persistent data PVC is attached to the new VM instance.
  5. The service resumes with the updated OS and intact state.

---

## 4. Required Code & CRD Schema Enhancements

### 4.1. CRD Schema Updates (`pkg/apis/uds/v1alpha1/types.go`)

Extend `UDSVirtualMachineSpec` to support `dataSource` roots and secondary `dataDisks`:

```go
// DataDiskSpec defines secondary persistent data disks attached to the VM.
type DataDiskSpec struct {
    Name             string `json:"name"`
    Size             string `json:"size"`                       // e.g. "50Gi"
    StorageClassName string `json:"storageClassName,omitempty"`
    Bus              string `json:"bus,omitempty"`              // "virtio", "scsi", "sata"
    PVCName          string `json:"pvcName,omitempty"`          // Bind to existing persistent PVC
    RetentionPolicy  string `json:"retentionPolicy,omitempty"`  // "Retain" or "Delete"
    MountPath        string `json:"mountPath,omitempty"`        // Used for Linux guest formatting
}

type DiskSpec struct {
    SourceType          string `json:"sourceType"`                 // "upload", "http", "dataSource"
    DataSourceName      string `json:"dataSourceName,omitempty"`   // Name of CDI DataSource to clone
    DataSourceNamespace string `json:"dataSourceNamespace,omitempty"`
    Format              string `json:"format,omitempty"`
    VirtualSizeBytes    int64  `json:"virtualSizeBytes,omitempty"`
    Checksum            string `json:"checksum,omitempty"`
    StorageClassName    string `json:"storageClassName,omitempty"`
}

type UDSVirtualMachineSpec struct {
    CPUs            int                 `json:"cpus,omitempty"`
    Memory          string              `json:"memory,omitempty"`
    RunStrategy     string              `json:"runStrategy,omitempty"`
    OSType          string              `json:"osType,omitempty"`
    WindowsProfile  *WindowsProfileSpec `json:"windowsProfile,omitempty"`
    Disk            DiskSpec            `json:"disk"`
    DataDisks       []DataDiskSpec      `json:"dataDisks,omitempty"` // NEW: Secondary persistent drives
    SecurityProfile SecurityProfileSpec `json:"securityProfile,omitempty"`
}
```

### 4.2. Controller Reconciler Updates (`pkg/controller/reconciler.go`)

1. **CDI DataSource Cloning:**
   * When `spec.disk.sourceType == "dataSource"`, generate the `DataVolume` with:
     ```yaml
     spec:
       sourceRef:
         kind: DataSource
         name: <dataSourceName>
         namespace: <namespace>
     ```
2. **Secondary Data Volume Attachment:**
   * Iterate over `spec.dataDisks`:
     * If `pvcName` is provided, attach existing PVC directly.
     * If `pvcName` is empty, generate an independent `PersistentVolumeClaim` (or `DataVolume` with `source: blank`) named `<vm-name>-data-<disk-name>`.
     * Attach each disk under `spec.template.spec.domain.devices.disks` and `spec.template.spec.volumes`.
   * Honor `retentionPolicy: Retain` by omitting owner references on the persistent PVC so it survives VM deletion during rolling upgrades.

---

## 5. Zero-Downtime Rolling Update Architecture

### 5.1. Introducing `UDSVirtualMachinePool`

To orchestrate rolling updates across multiple instances without downtime, introduce a fleet CRD: `UDSVirtualMachinePool` (or wrap KubeVirt's native `VirtualMachinePool`).

```yaml
apiVersion: uds.dev/v1alpha1
kind: UDSVirtualMachinePool
metadata:
  name: tactical-web-fleet
  namespace: default
spec:
  replicas: 3
  selector:
    matchLabels:
      app: tactical-web
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  template:
    spec:
      osType: linux
      cpus: 2
      memory: 2Gi
      runStrategy: Always
      disk:
        sourceType: dataSource
        dataSourceName: ubuntu-2204-golden-v1
```

### 5.2. Rolling Update Sequence

```
Time ──►

T0: [VM-1 (v1)]  [VM-2 (v1)]  [VM-3 (v1)]          (Stable Fleet v1, Service points to all 3)
         │            │            │
T1: [VM-1 (v1)]  [VM-2 (v1)]  [VM-3 (v1)]  [VM-4 (v2)]  (Surge +1: Clone v2 booted)
                                                │
T2: [VM-1 (v1)]  [VM-2 (v1)]  [VM-3 (v1)]  [VM-4 (v2)]  (Readiness Gate: QEMU-GA & Port 80 Ready)
         │                                      │
T3:  [Drained]   [VM-2 (v1)]  [VM-3 (v1)]  [VM-4 (v2)]  (VM-1 removed from Service, ACPI stopped)
                                                │
T4: [VM-5 (v2)]  [VM-2 (v1)]  [VM-3 (v1)]  [VM-4 (v2)]  (Surge +1: Repeat for VM-2...)
```

### 5.3. Readiness Gates
A new VM replica is only considered `Ready` to accept production traffic when:
1. **QEMU Guest Agent Check:** The guest agent responds over virtio-serial with a valid IP address.
2. **Kubernetes Readiness Probe:** TCP connect or HTTP GET probe to the application port succeeds.
3. **Cloud-Init / Sysprep Status:** The zero-touch answer script completes execution without error flags.

---

## 6. Step-by-Step Implementation & Testing Phases

### Phase 1: Real Golden Image Creation & CLI Pre-Flight
1. **Download Upstream Images:**
   * Linux: Ubuntu 22.04 Minimal Cloud Image (`jammy-server-cloudimg-amd64.img`) or Rocky Linux 9 GenericCloud.
   * Windows: Windows Server 2022 evaluation ISO / pre-sysprepped QCOW2 with VirtIO drivers installed.
2. **Run `uds-vmctl prep`:**
   * Inspect header structure, verify format (`qcow2` or `raw`), ensure image is flattened with no external backing chain.
   * Stream SHA-256 digest and compute `+6%` filesystem padding.
3. **Publish CDI `DataSource`:**
   * Upload golden images into a dedicated `golden-images` namespace.
   * Register CDI `DataSource` objects (`ubuntu-2204-golden-v1`, `win2022-golden-v1`).

### Phase 2: Controller & CRD Schema Enhancements
1. Update `types.go` and regenerate CRD manifests for `spec.dataDisks` and `sourceType: dataSource`.
2. Update `reconciler.go` to construct the CDI `sourceRef: { kind: DataSource }` and secondary data volume mounts.
3. Add Go unit tests in `pkg/controller/reconciler_test.go` verifying:
   * Multi-disk attachment order and bus selection (`virtio` vs `sata`).
   * Retention policy behavior (`Retain` vs `Delete`).

### Phase 3: Stateless Fleet Rolling Upgrade Test
1. Deploy a 3-replica stateless Linux web service pool using `ubuntu-2204-golden-v1`.
2. Expose the pool via a Kubernetes `Service` with an HTTP readiness probe.
3. Start an external load generator (`hey` or `curl` loop in background) sending 50 req/sec.
4. Prepare `ubuntu-2204-golden-v2` (containing a software update or MOTD change).
5. Update the pool spec to target `dataSourceName: ubuntu-2204-golden-v2`.
6. **Pass Criteria:**
   * All 3 VMs roll over to `v2` one by one.
   * Zero HTTP 5xx errors or dropped connections recorded during the upgrade.
   * `uds-vmctl inventory` reports all 3 instances running on `v2`.

### Phase 4: Stateful VM Upgrade & Data Preservation Test
1. Deploy a stateful VM (`tactical-db-vm`) with:
   * Root OS: `ubuntu-2204-golden-v1`
   * Persistent Data Disk: `db-data` (10Gi, mounted at `/var/lib/data`).
2. SSH into the VM, create a verification test database / timestamp file with checksums on `/var/lib/data`.
3. Trigger an OS upgrade to `ubuntu-2204-golden-v2`.
4. Verify the operator workflow:
   * Drains and shuts down old VM.
   * Retains the `db-data` PVC.
   * Attaches `db-data` to the newly spawned `v2` VM.
5. SSH into the new `v2` VM and verify:
   * OS kernel / packages reflect `v2`.
   * The test files and database on `/var/lib/data` match original checksums with zero data loss.

---

## 7. Deliverables & Acceptance Checklist

| Item | Component | Done Definition |
| :--- | :--- | :--- |
| **CRD Extension** | `pkg/apis/uds/v1alpha1` | `dataDisks` and `dataSource` fields defined and generated in CRD. |
| **Controller Logic** | `pkg/controller` | Reconciles multi-disk attachments and CDI DataSource fast clones. |
| **Golden Images** | `uds-vmctl` / CDI | Real Ubuntu 22.04 and Windows Server 2022 golden DataSources deployed. |
| **Stateless Test** | Test Suite | 3-node pool rolls with 0% dropped HTTP traffic. |
| **Stateful Test** | Test Suite | Secondary PVC survives OS swap with byte-for-byte data integrity verified. |
| **Observability** | Prometheus / Grafana | Metrics expose pool update progress (`uds_vm_pool_replicas`). |
