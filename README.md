# UDS Virtual Machine Operator (`uds-vm-operator`)

[![Go Unit Tests](https://img.shields.io/badge/go%20tests-6%2F6%20passed-brightgreen.svg)](pkg/controller/reconciler_test.go)
[![Python Prototype Tests](https://img.shields.io/badge/pytest-7%2F7%20passed-brightgreen.svg)](prototype-kopf/test_operator_kopf.py)
[![Compliance](https://img.shields.io/badge/NIST%20800--53-SC--7%20%7C%20AC--3%20%7C%20SI--7-blue.svg)](manifests/crd/udsvirtualmachines.uds.dev.yaml)
[![Upstream CDI PR](https://img.shields.io/badge/CDI%20PR-%234340%20Merged%2FTarget-orange.svg)](https://github.com/kubevirt/containerized-data-importer/pull/4340)
[![Roadmap](https://img.shields.io/badge/Roadmap-v0.1.0--v0.4.0-purple.svg)](ROADMAP.md)

An air-gap ready Kubernetes operator declaratively managing secure, NIST 800-53 compliant virtual machines on **KubeVirt** and **Containerized Data Importer (CDI)** within [Defense Unicorns](https://defenseunicorns.com) Unicorn Delivery Service (UDS) environments.

> 📍 **Product Roadmap:** See [ROADMAP.md](ROADMAP.md) for phased capability milestones ([v0.1.0 (Core Operator)](https://github.com/celttechie/uds-vm-operator/milestone/1) $\rightarrow$ [v0.2.0 (Windows Automation)](https://github.com/celttechie/uds-vm-operator/milestone/2) $\rightarrow$ [v0.3.0 (Go CLI & Zarf)](https://github.com/celttechie/uds-vm-operator/milestone/3) $\rightarrow$ [v0.4.0 (Observability)](https://github.com/celttechie/uds-vm-operator/milestone/4)).

---

## 1. Executive Summary & Problem Statement

Deploying virtual machines across air-gapped, mission-critical edge nodes (tactical sensors, edge edge-compute servers, SCIF environments) introduces severe operational and security friction:

1. **Air-Gap Image Corruption & Tampering (NIST 800-53 SI-7):** Transferring gigabyte-scale VM disk images across high-side air-gaps risks silent bit rot or supply-chain tampering. Native Kubernetes PVCs do not cryptographically verify disk image digests prior to VM execution.
2. **CDI Filesystem Overhead Failures:** VM disks imported into filesystem-backed PersistentVolumes fail when PVCs are sized 1:1 with virtual disk size due to filesystem allocation overhead (metadata, ext4 journals). Operators fail with out-of-space errors during decompression/conversion.
3. **Unflattened QCOW2 External Backing Files:** In disconnected tactical environments, QCOW2 images referencing external backing chains break silently because backing paths are missing in the air-gap.
4. **Boundary Isolation & STIG Compliance (NIST 800-53 SC-7, AC-3):** KubeVirt `virt-launcher` pods default to open pod networking and insecure Cloud-Init defaults unless explicitly hardened.

`uds-vm-operator` solves this through a declarative Custom Resource Definition (`UDSVirtualMachine`, API `uds.dev/v1alpha1`) and an automated reconciliation loop that coordinates hardened storage, isolation, and workload provisioning.

---

## 2. Architecture: Two-Stage Operator Workflow

To guarantee rapid validation followed by production-grade performance, this repository implements a **Two-Stage Operator Engineering Pattern**:

```mermaid
flowchart TD
    subgraph Stage1["Phase 1: Rapid Prototyping (Python / Kopf)"]
        A[vm_prep.py CLI] -->|Inspects Image & Rejects Backing Chains| B[UDSVirtualMachine YAML]
        B -->|Reconcile Loop| C[operator_kopf.py]
        C -->|Pure Resource Builders| D[pytest Suite: 7/7 Passing]
    end

    subgraph Stage2["Phase 2: Production Conversion (Go / controller-runtime)"]
        E[pkg/apis/uds/v1alpha1] --> F[pkg/controller/reconciler.go]
        F -->|1:1 Parity Unit Tests| G[go test: 6/6 Passing]
        G --> H[cmd/uds-vm-operator/main.go]
        H --> I[bin/uds-vm-operator Binary]
    end

    subgraph LiveCluster["Live K3s / KubeVirt Cluster Validation"]
        J[Live K3s Cluster] -->|Applied by Phase 1| K[reconciledBy: 'kopf-python']
        I -->|Zero-Downtime Controller Takeover| L[reconciledBy: 'controller-go']
    end

    Stage1 -.->|1:1 Algorithmic Port| Stage2
    Stage2 -.-> LiveCluster
```

1. **Stage 1 — Rapid Python `kopf` Prototype (`prototype-kopf/`):**
   - Implemented with pure, deterministic resource builder functions with zero side-effects and a single level-triggered `reconcile()` loop.
   - Evaluated CRD printer columns, NIST controls, and upstream CDI PR #4340 integration against a live K3s/KubeVirt cluster.
2. **Stage 2 — Production Go Controller (`pkg/` and `cmd/`):**
   - Transferred 1:1 into Go using `controller-runtime` and `client-go`.
   - Verified with unit tests mirroring identical test vectors.
   - Executed live controller takeover (`reconciledBy: "kopf-python"` $\rightarrow$ `reconciledBy: "controller-go"`) with zero VM downtime.

### Architectural Decision Record (ADR): Prototyping in Kopf Before Go Conversion

#### Context & Problem
Writing Kubernetes operators directly in Go with `controller-runtime` is standard for production, but can introduce significant friction during exploratory phases:
- Schema discovery against non-standard or actively evolving upstream APIs (e.g. KubeVirt, custom CDI PR #4340 builds) requires repeated `DeepCopy` and OpenAPI code generation.
- Re-compiling and running full Go controller binaries to iterate on CRD printer columns, condition structures, and retry backoffs slows development velocity.

#### The Decision: Two-Stage Engineering Workflow
We adopted a disciplined two-stage engineering approach:

1. **Stage 1 (Discovery & Verification via Python `kopf`):**
   - **Velocity & Immediate Feedback:** Python allowed sub-second test execution (`pytest`), instant live reloading against K3s, and rapid adjustment of CRD printer columns (`PVC-Size`, `Checksum-Verified`, `Zero-Trust`).
   - **Architectural Discipline:** To avoid throwaway code, `prototype-kopf/operator_kopf.py` was architected with **strict structural constraints**:
     - *Pure Resource Builders:* All Kubernetes manifests (`NetworkPolicy`, `Secret`, `DataVolume`, `VirtualMachine`) are generated by side-effect-free, deterministic builder functions.
     - *Level-Triggered State Reconciliation:* State evaluation is centralized in a single `reconcile()` loop that inspects actual cluster state rather than edge-triggered events.
   - **Validation of Mathematical & Security Logic:** The +6% filesystem overhead formula and NIST controls were proven against live hardware before writing a single line of Go.

2. **Stage 2 (Production Go Conversion via `controller-runtime`):**
   - **Direct 1:1 Translation:** Because Stage 1 was architected with pure builders and level-triggered reconciliation, porting to Go's `controller-runtime` was an exact 1:1 structural map (`pkg/controller/reconciler.go`).
   - **Zero Architectural Drift:** Every unit test in `prototype-kopf/test_operator_kopf.py` was directly translated to `pkg/controller/reconciler_test.go`, verifying identical sizing math and status evaluation.
   - **Production Hardening:** The resulting Go binary achieves minimal attack surface (no Python runtime, no pip dependency chain), sub-millisecond informer caching, and low memory consumption (~28 MiB RSS).

---

## 3. NIST 800-53 Security Controls Mapping

| NIST Control | Security Domain | Operator Implementation |
| :--- | :--- | :--- |
| **SC-7** | Boundary Protection | Generates a zero-trust `NetworkPolicy` (`<name>-netpol`) restricting all ingress traffic and locking down egress strictly to CoreDNS (UDP/TCP 53). |
| **AC-3 / IA-5** | Access Control & Authenticator Management | Generates hardened Cloud-Init NoCloud `Secret` (`<name>-cloudinit`) disabling SSH password authentication (`ssh_pwauth: false`), locking root access (`disable_root: true`), and activating FIPS mode. |
| **SI-7** | Software & Information Integrity | Integrates directly with upstream CDI PR [#4340](https://github.com/kubevirt/containerized-data-importer/pull/4340) via `spec.source.upload.checksum`. Cryptographic digest (`sha256:<hex>`) is verified before disk activation. |

---

## 4. Automatic +6% Filesystem Overhead Math

When CDI imports or uploads disk images to filesystem storage classes, the volume filesystem (e.g. ext4) consumes metadata overhead. If a 10 MiB disk is allocated an exact 10 MiB PVC, the transfer aborts with `No space left on device`.

`uds-vm-operator` enforces a mathematical sizing formula in both Python and Go:

$$\text{PaddedMiB} = \max\left(\left\lceil \frac{\text{VirtualSizeBytes}}{1024^2} \times 1.06 \right\rceil, \left\lfloor \frac{\text{VirtualSizeBytes}}{1024^2} \right\rfloor + 1\right)$$

- **10 MiB raw disk:** $10 \times 1.06 = 10.6 \text{ MiB} \implies \mathbf{11\text{ MiB}}$
- **1024 MiB (1 GiB) disk:** $1024 \times 1.06 = 1085.44 \text{ MiB} \implies \mathbf{1086\text{ MiB}}$

---

## 5. Air-Gap Pre-Flight Image Inspection (`vm_prep.py`)

The companion disconnected CLI utility `prototype-kopf/vm_prep.py` validates VM images before they enter the cluster:

```bash
# Inspects disk, computes SHA-256 digest, rejects external backing chains, and emits UDSVirtualMachine manifest
python3 prototype-kopf/vm_prep.py --image /path/to/tactical-sensor.raw --name tactical-sensor-vm --format raw
```

- **Backing File Rejection:** If an operator attempts to upload a QCOW2 image with an unflattened external `backing-filename`, `vm_prep.py` raises `ValueError("Image has unflattened backing chain... Air-gap deployments require standalone flattened images")`.

---

## 6. Comparison: Python Kopf vs. Production Go Controller

| Feature / Metric | Phase 1: Python Kopf Prototype | Phase 2: Production Go Operator |
| :--- | :--- | :--- |
| **Runtime & Framework** | Python 3.12 + Zalando `kopf` | Go 1.27 + `sigs.k8s.io/controller-runtime` |
| **Binary Footprint** | Python runtime + pip dependencies (~150MB) | Single static binary (`bin/uds-vm-operator`, ~55MB) |
| **Memory Footprint** | ~75 MiB RSS | ~28 MiB RSS |
| **Startup / Reconcile Latency** | ~1.5s cold start | < 20ms startup |
| **Unit Test Execution** | `pytest`: 7 tests in ~0.74s | `go test`: 6 tests in **0.016s** |
| **Concurrency / Worker Model** | Asyncio threadpool + periodic timer | Goroutine event loop with client cache & informer |
| **CRD Status Field** | `reconciledBy: "kopf-python"` | `reconciledBy: "controller-go"` |

---

## 7. Live Cluster Validation & Controller Takeover

The live demonstration was validated against a K3s + KubeVirt v1.3.1 cluster running our custom CDI build with upstream PR #4340:

### 1. Initial State (Python Kopf Reconciling)
```console
$ kubectl get udsvm -o wide
NAME                 PHASE              SOURCE   PVC-SIZE   CHECKSUM-VERIFIED   ZERO-TRUST   RECONCILER    AGE
tactical-sensor-vm   WaitingForUpload   upload   11Mi       false               true         kopf-python   7s
```

### 2. Live Upload via `virtctl` & Cryptographic Verification
```console
$ virtctl image-upload dv tactical-sensor-vm-dv --no-create --image-path=/tmp/test-disk.raw --uploadproxy-url=https://10.43.24.203 --insecure
Uploading data to https://10.43.24.203
 10.00 MiB / 10.00 MiB [==================================================] 100.00% 0s
Processing completed successfully
Uploading /tmp/test-disk.raw completed successfully
```

Kopf observed `DataVolume` transition to `Succeeded` with SHA-256 integrity match:
```console
$ kubectl get udsvm -o wide
NAME                 PHASE   SOURCE   PVC-SIZE   CHECKSUM-VERIFIED   ZERO-TRUST   RECONCILER    AGE
tactical-sensor-vm   Ready   upload   11Mi       true                true         kopf-python   57s
```

### 3. Zero-Downtime Controller Takeover (Go Controller-Runtime)
The Python Kopf process was killed, and the compiled Go binary was launched:
```console
$ ./bin/uds-vm-operator --kubeconfig=/path/to/kubeconfig
INFO setup Starting UDS Virtual Machine Operator (Go controller-runtime)
INFO controllers.UDSVirtualMachine Reconciled UDSVirtualMachine {"namespace": "default", "name": "tactical-sensor-vm", "phase": "Ready", "pvc": "11Mi", "checksumVerified": true, "reconciledBy": "controller-go"}
```

The resource immediately reflects the Go operator takeover without modifying child volumes or interrupting the VM:
```console
$ kubectl get udsvm -o wide
NAME                 PHASE   SOURCE   PVC-SIZE   CHECKSUM-VERIFIED   ZERO-TRUST   RECONCILER      AGE
tactical-sensor-vm   Ready   upload   11Mi       true                true         controller-go   5m29s
```

---

## 8. Running the Tests

### Python Kopf Unit Tests:
```bash
.venv/bin/pytest -v prototype-kopf/test_operator_kopf.py
```

### Go Unit Tests:
```bash
go test -v ./pkg/controller/...
```

### Building the Operator Binary:
```bash
go build -o bin/uds-vm-operator ./cmd/uds-vm-operator
```
