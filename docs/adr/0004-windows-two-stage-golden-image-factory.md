# ADR 0004: Two-Stage Windows Golden Image Factory, Compressed QCOW2 & SI-7 Integrity

- **Status:** Accepted
- **Date:** 2026-11-14
- **Milestone:** v0.6.0 (Windows Server Workload)

## Context
Deploying Windows Server workloads on KubeVirt from a raw installation ISO takes 15–30+ minutes per VM due to `windowsPE` setup, runtime VirtIO driver injection, Windows Updates, and `qemu-ga` installation. For `UDSVirtualMachinePool` rolling updates and air-gapped deployments, this creates unacceptable boot latency and runtime variance while requiring large (40+ GiB) uncompressed disk transfers if using `raw` images.

At the same time, some environments require vendor-signed ISO chain-of-custody for the initial OS baseline (or clean major OS upgrades such as Server 2019 $\rightarrow$ 2022), while iterative Patch Tuesday / application updates benefit from offline slipstreaming or Stage 2 clone-and-patch builds.

## Decision
Adopt a **two-stage Golden Image Factory** using **compressed QCOW2** (`qemu-img convert -O qcow2 -c`) and **CDI `DataSource` cloning**:

1. **Stage 1 — Initial Baseline via In-Cluster ISO Install (`v1`):**
   - Upload the vendor-signed Microsoft ISO with **NIST 800-53 SI-7** SHA-256 verification (`spec.disk.checksum`).
   - Boot a builder `UDSVirtualMachine` (`format: iso`, `provisioningMode: iso`, `injectVirtioWinISO: true`) targeting a persistent `dataDisks` PVC (rather than an ephemeral `emptyDisk`).
   - Install VirtIO drivers, `qemu-ga`, and baseline configuration, then generalize with `sysprep /generalize /oobe /shutdown`.
   - Promote the resulting PVC to a CDI `DataSource` in `uds-golden-images` (e.g., `windows-server-2022-v1`).
   - Full major OS upgrades (e.g., 2019 $\rightarrow$ 2022) always start from a clean Stage 1 ISO build rather than an in-place OS upgrade.

2. **Stage 2 — Fast Slipstreamed / Patched Golden Images (`v2`, `v3`, ...):**
   - For Cumulative Updates (`.msu`), VirtIO driver updates, DISA STIG baselines, and baked application software, build `v2` either via **offline DISM/Packer slipstreaming** or by **cloning `v1` in a Stage 2 builder VM**, applying updates, and re-running `sysprep /generalize /oobe /shutdown`.
   - Freeze and export as a flattened, compressed **QCOW2** (~3–5× smaller than `raw` on the wire and in Zarf packages).
   - Verify the single immutable SHA-256 via `uds-vmctl prep --golden-image` and CDI `spec.source.upload.checksum` (`SI-7`).

3. **Cluster Storage & Cloning:**
   - Target `local-path` as the baseline StorageClass on `kubevirt-sandbox` (keeping Ceph out of scope); CDI automatically performs host-assisted PVC cloning (~30–90s) while keeping the exact same `DataSource` manifests ready for instant CSI CoW clones on Ceph RBD, Longhorn, or TopoLVM.
   - Fleet updates roll out via `UDSVirtualMachinePool` (`maxSurge: 1, maxUnavailable: 0` for stateless/per-replica pools; `strategy.type: Recreate` when sharing a single `ReadWriteOnce` `pvcName` `dataDisk`).

## Consequences
- **Positive:**
  - Preserves vendor-ISO chain-of-custody when needed while reducing production Windows VM provisioning to ~1–3 minutes.
  - Compressed QCOW2 shrinks air-gap Zarf packages and `virtctl image-upload` transfers by 3–5× without sacrificing `SI-7` SHA-256 verification.
  - Baking patches and software into `v2` Golden Images avoids runtime package-repo egress, aligning with `SC-7` zero-trust `NetworkPolicy` enforcement.
- **Negative:**
  - CDI performs a one-time `qemu-img` conversion during Golden Image upload to `uds-golden-images`.
  - Without a CSI snapshot/clone StorageClass, `local-path` falls back to host-assisted copy rather than sub-second CoW.
