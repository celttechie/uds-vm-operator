# End-to-End Verification Runbook: Zero-to-Ignition Testing Guide

This runbook provides a complete, step-by-step walkthrough to test `uds-vm-operator` from scratch using your nested `kubevirt-dev-platform` VM (`kubevirt-sandbox`) running K3s, KubeVirt, and the patched CDI build from upstream PR [#4340](https://github.com/kubevirt/containerized-data-importer/pull/4340).

It specifically walks you through:
1. **Starting from a clean slate** on your cluster.
2. **Negative Test 1:** Zarf air-gap supply-chain rejection of a tampered golden image.
3. **Negative Test 2:** CDI PR #4340 in-cluster rejection of an image with an invalid cryptographic checksum (NIST SI-7).
4. **Positive Test:** Disk inspection, automatic `+6%` sizing, Zarf packaging, and zero-touch ignition with the correct hash.
5. **Fleet Telemetry & Power Orchestration:** Managing live VMs via `uds-vmctl`.
6. **Continuous ATO Validation:** Running Defense Unicorns Lula against live cluster resources.
7. **Prometheus Observability:** Scraping live metrics from the operator.

---

## 1. Prerequisites & Cluster Connection

All commands can be executed directly from your workstation inside `/home/bjarrett/Projects/uds-vm-operator`.

### Export Cluster Kubeconfig
```bash
export KUBECONFIG=/home/bjarrett/Projects/kubevirt-dev-platform/kubeconfig
```

### Verify Cluster Connectivity & Patched CDI
Confirm that KubeVirt and the custom CDI build (PR #4340) are healthy:

```bash
# Check node connectivity
kubectl get nodes -o wide

# Check KubeVirt components
kubectl get pods -n kubevirt

# Check patched CDI components (cdi-uploadserver, cdi-controller, cdi-apiserver with test image tags)
kubectl get pods -n cdi -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[*].image}{"\n"}{end}'
```

> [!NOTE]
> **Pre-Merge Custom CDI Configuration:**  
> If the cluster was initialized from standard upstream release manifests (`v1.66.1`), point the CDI deployments to the pre-loaded local `:test` images and preserve the upload checksum CRD field:
> ```bash
> # 1. Point CDI controller & apiserver to the local PR #4340 test builds
> kubectl set env deployment/cdi-operator -n cdi \
>   CONTROLLER_IMAGE=docker.io/library/cdi-controller:test \
>   APISERVER_IMAGE=docker.io/library/cdi-apiserver:test \
>   UPLOAD_SERVER_IMAGE=docker.io/library/cdi-uploadserver:test \
>   PULL_POLICY=Never
>
> # 2. Scale down cdi-operator so it does not reconcile back to stock v1.66.1 CRDs during local testing
> kubectl scale deployment -n cdi cdi-operator --replicas=0
>
> # 3. Ensure datavolumes CRD preserves the upload checksum schema
> kubectl patch crd datavolumes.cdi.kubevirt.io --type=json -p='[
>   {"op": "add", "path": "/spec/versions/0/schema/openAPIV3Schema/properties/spec/properties/source/properties/upload/x-kubernetes-preserve-unknown-fields", "value": true}
> ]'
> ```

*(Optional) Direct SSH Access:*
If you ever need to inspect the sandbox VM directly:
```bash
ssh -F /home/bjarrett/Projects/kubevirt-dev-platform/.ssh/config kubevirt-sandbox
```

---

## 2. Resetting the Cluster to a Clean Slate

Before testing, remove any existing virtual machines, data volumes, and operator deployments so you are starting from a completely clean state:

```bash
# 1. Delete all existing UDSVirtualMachine instances
kubectl delete udsvirtualmachine --all -A --timeout=30s --ignore-not-found

# 2. Delete existing VirtualMachines and DataVolumes in default namespace
kubectl delete vm,vmi,dv,pvc -n default --all --timeout=60s --ignore-not-found

# 3. Delete operator deployment in uds-system if present
kubectl delete deployment -n uds-system uds-vm-operator --ignore-not-found

# 4. Confirm clean state
kubectl get udsvm,vm,vmi,dv,pvc -n default
```

---

## 3. Step 1: Compile Operator & CLI Binaries

Build the native Go binaries (`uds-vm-operator` and `uds-vmctl`) with embedded git commit and version metadata:

```bash
make build
```

**Why this is needed:**
- `bin/uds-vm-operator`: The Kubernetes controller binary running `controller-runtime`.
- `bin/uds-vmctl`: The standalone CLI used by operators for pre-flight disk inspection, power state changes, and tabular fleet inventory without external Python or `qemu-img` dependencies.

Verify unit tests pass with race detection:
```bash
make test
```
*Expected: 15/15 Go unit tests passed.*

---

## 4. Step 2: Negative Test 1 — Supply-Chain Tamper Detection in Zarf

Defense Unicorns Zarf enforces cryptographic integrity at package build time. If a disk image has been modified, tampered with, or corrupted in the supply chain, Zarf aborts package creation before the air-gap bundle is ever produced.

### 1. Intentionally Alter the Golden Image Hash
Create a corrupted copy of the golden image with altered bytes:
```bash
# Copy the clean golden image to a temporary file
cp images/tactical-linux-golden.raw /tmp/corrupted-disk.raw

# Flip a byte at offset 512
printf "\xFF\xFF\xFF\xFF" | dd of=/tmp/corrupted-disk.raw bs=1 seek=512 conv=notrunc
```

Verify that the SHA-256 hash has changed:
```bash
sha256sum /tmp/corrupted-disk.raw
# Result will NOT match e5b844cc57f57094ea4585e235f36c78c1cd222262bb89d53c94dcb4d6b3e55d
```

### 2. Attempt to Build a Zarf Package with the Bad Hash
Temporarily point `zarf.yaml` to the corrupted disk:
```bash
# Back up zarf.yaml
cp zarf.yaml zarf.yaml.bak

# Substitute the source file in zarf.yaml to the corrupted image
sed -i 's|images/tactical-linux-golden.raw|/tmp/corrupted-disk.raw|g' zarf.yaml

# Attempt to package
zarf package create . --confirm --skip-sbom
```

### Expected Result:
Zarf halts immediately with an error:
```text
failed to create package: shasum mismatch for /tmp/corrupted-disk.raw: expected e5b844cc57f57094ea4585e235f36c78c1cd222262bb89d53c94dcb4d6b3e55d, got <corrupted-hash>
```
**Why:** Zarf acts as the first line of defense, guaranteeing that corrupted files cannot be packaged for air-gap distribution.

Restore the valid `zarf.yaml`:
```bash
mv zarf.yaml.bak zarf.yaml
rm -f /tmp/corrupted-disk.raw
```

---

## 5. Step 3: Negative Test 2 — CDI In-Cluster Checksum Rejection (NIST SI-7)

Even if an attacker bypasses the packaging gate or an operator pushes a disk directly via `virtctl`, **upstream CDI PR #4340** guarantees that KubeVirt will never execute a disk whose content does not match the manifest's declared cryptographic digest.

### 1. Apply a UDSVirtualMachine with the Wrong Hash
Create a test manifest claiming a bogus SHA-256 digest:

```bash
cat <<EOF | kubectl apply -f -
apiVersion: uds.dev/v1alpha1
kind: UDSVirtualMachine
metadata:
  name: corrupted-hash-vm
  namespace: default
spec:
  cpus: 1
  memory: 512Mi
  runStrategy: Always
  disk:
    sourceType: upload
    format: raw
    virtualSizeBytes: 10485760 # 10 MiB
    checksum: sha256:0000000000000000000000000000000000000000000000000000000000000000
  securityProfile:
    zeroTrustNetwork: true
EOF
```

### 2. Run the Go Operator Locally
Start the operator in a terminal so you can watch reconciliation in real time:
```bash
./bin/uds-vm-operator --kubeconfig $KUBECONFIG
```

In a second terminal, observe the operator create the child `DataVolume`:
```bash
kubectl get udsvm corrupted-hash-vm -o wide
# Status: Phase: WaitingForUpload, PVC-SIZE: 11Mi, CHECKSUM-VERIFIED: false
```

### 3. Attempt to Upload the Actual Disk
Now upload the real `tactical-linux-golden.raw` image (which has hash `e5b844cc...`) to the DataVolume. 

Because `cdi-uploadproxy` is an internal Kubernetes `ClusterIP`, port-forward it to your workstation:

```bash
# In a background process or separate terminal, port-forward the uploadproxy
kubectl port-forward -n cdi svc/cdi-uploadproxy 8443:443 &

# Attempt upload via virtctl through localhost
virtctl image-upload dv corrupted-hash-vm-dv \
  --no-create \
  --image-path=images/tactical-linux-golden.raw \
  --uploadproxy-url=https://127.0.0.1:8443 \
  --insecure
```

### Expected Result:
1. The CDI upload server streams the image, computes the SHA-256 digest on the fly, detects that the calculated hash does not match `sha256:00000000...`, and terminates the connection:
   ```console
   unexpected return value 500, Saving stream failed: Unable to transfer source data to target file: checksum validation failed: checksum mismatch: expected sha256:0000000000000000000000000000000000000000000000000000000000000000, calculated sha256:e5b844cc57f57094ea4585e235f36c78c1cd222262bb89d53c94dcb4d6b3e55d
   ```
2. The upload server logs confirm that **zero bytes** were written to `/data/disk.img`:
   ```console
   $ kubectl logs pod/cdi-upload-corrupted-hash-vm-dv
   Read 10485760 bytes, wrote 0 bytes to /data/disk.img
   checksum validation failed: checksum mismatch...
   ```
2. CDI aborts the transfer and marks the `DataVolume` as `Failed`:
   ```console
   $ kubectl get dv corrupted-hash-vm-dv -o wide
   NAME                   PHASE    PROGRESS   RESTARTS   AGE
   corrupted-hash-vm-dv   Failed                         45s
   ```
3. Inspecting the DataVolume conditions:
   ```bash
   kubectl describe dv corrupted-hash-vm-dv | grep -A 5 -i "checksum"
   ```
4. `uds-vm-operator` detects the failure, flips `UDSVirtualMachine` status to `Failed`, and **refuses to ignite the VM**:
   ```console
   $ kubectl get udsvm corrupted-hash-vm -o wide
   NAME                PHASE    SOURCE   PVC-SIZE   CHECKSUM-VERIFIED   ZERO-TRUST   RECONCILER      AGE
   corrupted-hash-vm   Failed   upload   11Mi       false               true         controller-go   2m
   ```
   **Security Control Validated:** NIST SP 800-53 SI-7 (Information Integrity) strictly prevents untrusted or tampered disk execution.

### Clean Up Negative Test
```bash
kubectl delete udsvm corrupted-hash-vm -n default
```

---

## 6. Step 4: Pre-Flight Golden Image Inspection (`uds-vmctl prep`)

Now inspect the genuine golden image using the compiled Go CLI:

```bash
./bin/uds-vmctl prep images/tactical-linux-golden.raw --zarf --name tactical-sensor-vm
```

### Expected Output:
```text
==================================================================
        UDS AIR-GAP DISK IMAGE INSPECTION & PREPARATION          
==================================================================
File:                tactical-linux-golden.raw
Path:                images/tactical-linux-golden.raw
Format:              raw
Virtual Size:        10485760 bytes (0.01 GiB)
Actual Size:         10485760 bytes (0.01 GiB)
Required PVC Size:   11Mi (includes +6% filesystem overhead)
SHA-256 Digest:      sha256:e5b844cc57f57094ea4585e235f36c78c1cd222262bb89d53c94dcb4d6b3e55d
Air-Gap Verified:    PASSED (Standalone, 0 external backing references)
==================================================================
```

**Why this matters:**
- **External Backing Chains:** If an image is a QCOW2 with an unflattened backing chain, `uds-vmctl prep` immediately rejects it because external paths will break in an air-gapped SCIF.
- **+6% Overhead Formula:** Calculates $\max(\lceil 10 \times 1.06 \rceil, 10 + 1) = 11\text{ MiB}$ to prevent CDI `No space left on device` crashes.
- Emits the declarative `manifests/examples/tactical-sensor-vm.yaml` with the exact computed digest.

---

## 7. Step 5: Positive Test — Zarf Air-Gap Packaging & Deployment

Deploy the entire operator stack and sample virtual machine via Defense Unicorns Zarf.

### 1. Build the Zarf Air-Gap Distribution
```bash
make zarf-package
```
*Creates: `zarf-package-uds-vm-operator-amd64-0.4.0.tar.zst`.*

### 2. Deploy the Package to the Live Cluster
```bash
zarf package deploy zarf-package-uds-vm-operator-amd64-0.4.0.tar.zst --confirm
```

### What Happens During Deployment:
1. Zarf verifies the cryptographic checksum of the included golden image fixture.
2. Applies CRDs (`udsvirtualmachines.uds.dev`).
3. Deploys the operator in `uds-system` with RBAC.
4. Applies `manifests/examples/tactical-sensor-vm.yaml`.
5. Uploads the golden image fixture to `tactical-sensor-vm-dv`.
6. CDI PR #4340 cryptographically validates the SHA-256 digest.
7. Upon checksum match (`checksumVerified: true`), the operator transitions the VM to `Ready` and launches the KubeVirt `VirtualMachine`.

---

## 8. Step 6: Verify Live Cluster Workload & Power Orchestration

### 1. View Tabular Fleet Telemetry
```bash
./bin/uds-vmctl inventory
```

**Expected Output:**
```text
NAMESPACE  NAME                 PHASE  OS     PVC-SIZE  CHECKSUM-VERIFIED  ZERO-TRUST  RECONCILER     AGE
default    tactical-sensor-vm   Ready  linux  11Mi      true               true        controller-go  2m
```

### 2. Inspect Running VM Instance via kubectl
```bash
kubectl get vmis -n default
# Shows tactical-sensor-vm in PHASE: Running with allocated IP (10.42.0.x)
```

### 3. Test Declarative Power State Transitions
Orchestrate power state between `Always` and `Halted` without deleting the VM definition:

```bash
# Stop the VM
./bin/uds-vmctl power stop tactical-sensor-vm

# Check status (VMI terminates, spec.runStrategy is Halted)
kubectl get vmis -n default

# Restart the VM
./bin/uds-vmctl power start tactical-sensor-vm

# Confirm VMI boots back to Running
kubectl get vmis -n default
```

---

## 9. Step 7: Continuous ATO Validation with Lula (OSCAL)

Evaluate Defense Unicorns Lula against the live cluster child objects (`NetworkPolicy`, `Secret`, `DataVolume`):

```bash
make lula-validate
```

### Expected Output:
```text
🔍 Collecting Requirements and Validations for Target: NIST SP 800-53 Rev 5
  • Found 4 Implemented Requirements
  • Found 4 runnable Lula Validations

💡 Findings   
Control ID | Status
si-7       | satisfied
ia-5       | satisfied
ac-3       | satisfied
sc-7       | satisfied

  • OSCAL artifact written to: compliance/assessment-results.yaml
```

**Controls Verified on Live Hardware:**
- **SC-7 (Boundary Protection):** Asserts `tactical-sensor-vm-netpol` enforces default-deny ingress and restricts egress strictly to DNS (port 53).
- **AC-3 (Access Enforcement):** Asserts `tactical-sensor-vm-cloudinit` enforces `ssh_pwauth: false` and `disable_root: true`.
- **SI-7 (Integrity):** Asserts `tactical-sensor-vm-dv` enforces PR #4340 SHA-256 upload checksum verification.
- **IA-5 (Authenticator Management):** Asserts Windows Sysprep secrets enforce dynamic $\ge 24$-char high-entropy credentials.

---

## 10. Step 8: Prometheus Observability Verification

Validate that the operator is actively publishing continuous ATO telemetry and storage metrics.

### 1. Port-Forward Operator Metrics Port
In a background or separate terminal:
```bash
kubectl port-forward -n uds-system deployment/uds-vm-operator 8080:8080
```

### 2. Scrape Live Prometheus Metrics
```bash
curl -s http://localhost:8080/metrics | grep uds_vm_
```

### Expected Metrics Sample:
```text
# Continuous ATO compliance gauge (1 = verified)
uds_vm_compliance_verified{control="AC-3",namespace="default",vm="tactical-sensor-vm"} 1
uds_vm_compliance_verified{control="SC-7",namespace="default",vm="tactical-sensor-vm"} 1
uds_vm_compliance_verified{control="SI-7",namespace="default",vm="tactical-sensor-vm"} 1

# Filesystem overhead ratio (+6% math verification)
uds_vm_storage_overhead_ratio{namespace="default",vm="tactical-sensor-vm"} 1.06

# Active lifecycle phase gauge
uds_vm_phase_status{namespace="default",phase="Ready",vm="tactical-sensor-vm"} 1

# Controller loop count
uds_vm_reconcile_total{status="success"} 4
```

---

## 11. Quick Reference: Command Cheat Sheet

| Purpose | Command |
| :--- | :--- |
| **Set Context** | `export KUBECONFIG=/home/bjarrett/Projects/kubevirt-dev-platform/kubeconfig` |
| **Compile Binaries** | `make build` |
| **Run Unit Tests** | `make test` |
| **Inspect Golden Disk** | `./bin/uds-vmctl prep images/tactical-linux-golden.raw --zarf --name tactical-sensor-vm` |
| **Build Zarf Package** | `make zarf-package` |
| **Deploy Zarf Package** | `zarf package deploy zarf-package-uds-vm-operator-amd64-0.4.0.tar.zst --confirm` |
| **Fleet Inventory** | `./bin/uds-vmctl inventory` |
| **Power Control** | `./bin/uds-vmctl power [start\|stop\|restart] <vm-name>` |
| **Validate cATO (Lula)** | `make lula-validate` |
| **Scrape Telemetry** | `curl -s http://localhost:8080/metrics \| grep uds_vm_` |
| **Clean Slate Reset** | `kubectl delete udsvm --all -A && kubectl delete vm,dv,pvc --all -n default` |
