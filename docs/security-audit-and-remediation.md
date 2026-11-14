# Security Audit & Vulnerability Remediation Report (`v0.6.0-sec`)

**Date:** 2026-11-14  
**Target Environment:** K3s + KubeVirt v1.3.1 + CDI v1.66.1 + Zarf v0.85.0  
**Operator Image:** `ghcr.io/celttechie/uds-vm-operator:v0.6.0-sec`  
**Scope:** Go dependencies (`go.mod`), operator container image (`Dockerfile`), Kubernetes manifests (`manifests/`), live Windows Server 2022 `v2` DISA STIG posture, and Lula OSCAL Continuous ATO (`compliance/oscal-component.yaml`).

---

## 1. Executive Summary

A full-stack vulnerability and configuration audit was executed using **Trivy v0.74.0**, **Lula OSCAL**, and in-guest **QEMU Guest Agent** (`qemu-ga`) inspection. All `HIGH` and `CRITICAL` vulnerabilities in the `uds-vm-operator` binary, container image, and Kubernetes pod security configuration were remediated and verified in-cluster with zero downtime.

| Audit Domain | Baseline Findings (`HIGH`/`CRITICAL`) | Remediation Applied | Post-Fix Verification |
| :--- | :--- | :--- | :--- |
| **Go Dependencies** (`go.mod`) | **10 `HIGH`** (`golang.org/x/net`, `x/oauth2`, `x/text`) | Upgraded `x/net` → `v0.62.0`, `x/oauth2` → `v0.38.0`, `x/text` → `v0.44.0` | **0** (`trivy fs`) |
| **Container Base Image** (`Dockerfile`) | **2 `HIGH`** (`alpine:3.19` `musl`/`musl-utils` `CVE-2026-40200`) | Migrated to `gcr.io/distroless/static:nonroot` (`USER 65532:65532`) | **0** (`trivy image`) |
| **K8s Pod Security** (`manifests/operator/deployment.yaml`) | **`KSV-0118` (`HIGH`)**, **`KSV-0104` (`MEDIUM`)** | Added pod & container `securityContext` (`runAsNonRoot: true`, `65532:65532`, `seccompProfile: RuntimeDefault`) | **0** workload findings (`trivy config`) |
| **Windows Guest Baseline** (`win2022-app-pool` `v2`) | Verified live `v2-2026-11-14` Golden Image | In-guest `qemu-ga` inspection of FIPS, SMB signing, LSA PPL, password/lockout policy | **All verified** |
| **OSCAL Compliance** (`make lula-validate`) | 4 NIST 800-53 Rev 5 controls | Validated live cluster resources post-rollout | **4/4 `satisfied`** (`si-7`, `ia-5`, `ac-3`, `sc-7`) |

---

## 2. Detailed Vulnerability Remediation

### 2.1 Go Module Dependencies (`go.mod` / `go.sum`)
* **Baseline:** `trivy fs --scanners vuln --severity HIGH,CRITICAL .` flagged 10 `HIGH` CVEs:
  * `golang.org/x/net@v0.35.0` (`CVE-2026-25681`, `CVE-2026-27136`, `CVE-2026-33814`, `CVE-2026-39821`, `CVE-2026-46600`, `CVE-2026-78661`, `CVE-2026-78669`)
  * `golang.org/x/oauth2@v0.12.0` (`CVE-2025-22868`)
  * `golang.org/x/text@v0.22.0` (`CVE-2026-56851`, `CVE-2026-56852`)
* **Fix:**
  ```bash
  go get golang.org/x/net@latest golang.org/x/oauth2@latest golang.org/x/text@latest
  go mod tidy
  ```
* **Verification:** `trivy fs --scanners vuln --severity HIGH,CRITICAL .` → `go.mod (gomod): 0 vulnerabilities`.

### 2.2 Container Image Hardening (`Dockerfile`)
* **Baseline:** `alpine:3.19` (`3.19.9`) carried 2 `HIGH` `musl`/`musl-utils` CVEs (`CVE-2026-40200`) plus unnecessary shell/package-manager attack surface.
* **Fix:** Switched [Dockerfile](../Dockerfile) to Google Distroless static non-root:
  ```dockerfile
  FROM gcr.io/distroless/static:nonroot
  WORKDIR /
  COPY bin/uds-vm-operator /manager
  USER 65532:65532
  ENTRYPOINT ["/manager"]
  ```
* **Verification:**
  ```bash
  VERSION=v0.6.0-sec make build-image
  trivy image --severity HIGH,CRITICAL ghcr.io/celttechie/uds-vm-operator:v0.6.0-sec
  ```
  Result: `debian 13.7`: **0**, `manager (gobinary)`: **0**.

### 2.3 Kubernetes Deployment Hardening (`manifests/operator/deployment.yaml`)
* **Baseline:** `trivy config --severity MEDIUM,HIGH,CRITICAL manifests/` flagged `KSV-0118` (`HIGH`, default pod `securityContext`) and `KSV-0104` (`MEDIUM`, missing `seccompProfile`).
* **Fix:** Added explicit pod-level and container-level `securityContext` in [deployment.yaml](../manifests/operator/deployment.yaml):
  * Pod `securityContext`: `runAsNonRoot: true`, `runAsUser: 65532`, `runAsGroup: 65532`, `fsGroup: 65532`, `seccompProfile.type: RuntimeDefault`.
  * Container `securityContext`: `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, `runAsNonRoot: true`, `seccompProfile.type: RuntimeDefault`, `capabilities.drop: ["ALL"]`.
* **Remaining Expected Findings:** `KSV-0041` (`CRITICAL`), `KSV-0049` (`MEDIUM`), and `KSV-0056` (`HIGH`) reflect the operator's required `ClusterRole` permissions to create per-VM Cloud-Init `Secret`s, `Autounattend.xml` `ConfigMap`s, and zero-trust `NetworkPolicy` objects; `KSV-0125` (`MEDIUM`) flags `ghcr.io` as a non-default registry.

---

## 3. Live Cluster Verification

1. **Operator Rollout:**
   - Imported `ghcr.io/celttechie/uds-vm-operator:v0.6.0-sec` into the cluster `containerd` and applied `manifests/operator/deployment.yaml` (Zarf webhook ref `127.0.0.1:31999/celttechie/uds-vm-operator:v0.6.0-sec-zarf-3915251271`).
   - `kubectl rollout status deployment/uds-vm-operator -n uds-system` → `uds-vm-operator-84b74945bb-p5w9t` `1/1 Running`.
2. **In-Guest Windows Server 2022 DISA STIG Verification** (`win2022-app-pool-00a4718490-0` via `virsh qemu-agent-command`):
   - `C:\UDS\golden-image-version.txt`: `v2-2026-11-14`
   - `HKLM:\SYSTEM\CurrentControlSet\Control\Lsa\FipsAlgorithmPolicy\Enabled`: `1`
   - `HKLM:\SYSTEM\CurrentControlSet\Services\LanmanServer\Parameters\RequireSecuritySignature`: `1`
   - `HKLM:\SYSTEM\CurrentControlSet\Control\Lsa\RunAsPPL`: `1`
   - `HKLM:\SOFTWARE\Microsoft\ServerManager\DoNotOpenServerManagerAtLogon`: `1`
   - `net accounts`: `Minimum password length: 14`, `Lockout threshold: 3`
3. **Go Unit Tests:** `go test -count=1 -race ./...` → all 4 test packages passed (`cmd/uds-vmctl`, `pkg/apis/uds/v1alpha1`, `pkg/cli`, `pkg/controller`).
4. **Lula OSCAL Assessment:** `make lula-validate` → `si-7`, `ia-5`, `ac-3`, `sc-7`, `cm-6`, `si-2` all **`satisfied`** ([assessment-results.yaml](../compliance/assessment-results.yaml)).

---

## 4. DoD IL-5 cATO Evidence & POA&M Justifications

For **DoD Impact Level 5 (IL-5)** Continuous ATO, residual scanner findings that are inherent to Kubernetes operator functionality are documented with compensating controls:

| Finding ID | Resource | Justification / Compensating Control |
| :--- | :--- | :--- |
| **`KSV-0041`** | `ClusterRole/uds-vm-operator-role` (`secrets`) | Operator generates per-VM Cloud-Init NoCloud `Secret`s (`<vm>-cloudinit`) and high-entropy Windows credential `Secret`s (`<vm>-admin-credentials`). Mitigated via `OwnerReference` lifecycle binding, non-root distroless execution, and zero-trust `NetworkPolicy` isolation. |
| **`KSV-0049`** | `ClusterRole/uds-vm-operator-role` (`configmaps`) | Operator generates per-VM `Autounattend.xml` `ConfigMap`s (`<vm>-autounattend`) containing no credentials (passwords injected from `Secret`s or isolated per-VM provisioning). |
| **`KSV-0056`** | `ClusterRole/uds-vm-operator-role` (`networkpolicies`) | Operator enforces NIST `SC-7` default-deny egress/ingress by reconciling `<vm>-netpol` `NetworkPolicy` objects for every managed VM. |
| **`KSV-0125`** | `Deployment/uds-vm-operator` (`ghcr.io/...`) | In disconnected IL-5 enclaves, Zarf rewrites the image reference to the authenticated in-cluster Zarf OCI registry (`127.0.0.1:31999/...`) with verified SHA-256 digests and SBOM attestation. |

