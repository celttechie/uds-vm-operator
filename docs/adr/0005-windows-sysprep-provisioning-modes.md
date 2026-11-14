# ADR 0005: Explicit Windows Sysprep `provisioningMode`, Persistent ISO Targets & Pool NetBIOS Naming

- **Status:** Accepted
- **Date:** 2026-11-14
- **Milestone:** v0.6.0 (Windows Server Workload)

## Context
Previously, [`BuildWindowsAutounattendXML`](../../pkg/controller/reconciler.go) generated a single `Autounattend.xml` containing a `<settings pass="windowsPE">` block with `<WillWipeDisk>true</WillWipeDisk>` and `FirstLogonCommands` scanning CD-ROM drives for `qemu-ga-x86_64.msi`. Additionally:
1. When booting from a generalized Golden Image (`sourceType: dataSource` or a pre-sysprepped QCOW2 `upload`), `windowsPE` disk-wiping directives are unnecessary and unsafe if an ISO is ever attached, and runtime `qemu-ga` `msiexec` wastes boot time when `qemu-ga` is already slipstreamed.
2. When `format: iso` was used, `BuildVirtualMachine` attached `installdisk` as an ephemeral `emptyDisk`, preventing the installed OS disk from persisting for `DataSource` promotion.
3. [`SanitizeNetBIOSName`](../../pkg/controller/reconciler.go) truncated names to the first 15 characters, which caused all replicas in a `UDSVirtualMachinePool` (e.g., `windows-server-pool-<rev>-<idx>`) to collide on the same Windows `ComputerName`.

## Decision
1. **Explicit `windowsProfile.provisioningMode` (`iso | clone | auto`):**
   - `iso`: Deterministically emits full `windowsPE` + `specialize` + `oobeSystem` `Autounattend.xml`.
   - `clone`: Deterministically emits lightweight `specialize` + `oobeSystem` `unattend.xml` (no `windowsPE` wipe), and skips `qemu-ga` `msiexec` in `FirstLogonCommands` when `injectVirtioWinISO: false`.
   - `auto` (or omitted): Resolves to `clone` when `disk.sourceType == "dataSource"`, and `iso` otherwise (preserving full backward compatibility with existing manifests and tests).
   - `uds-vmctl prep` explicitly emits `provisioningMode: clone` for `--golden-image` / disk images and `provisioningMode: iso` for `.iso` files.
2. **Persistent ISO Install Target:**
   - When `disk.format == "iso"`, if `spec.dataDisks` contains a disk named `installdisk`, `BuildVirtualMachine` uses that persistent PVC as the installation target instead of an ephemeral `emptyDisk`.
3. **Collision-Free 15-Character Pool NetBIOS Names:**
   - Update `SanitizeNetBIOSName` so names longer than 15 characters preserve the unique trailing suffix (e.g., first 9 chars + `-` + last 5 chars), ensuring `UDSVirtualMachinePool` replicas receive distinct Windows hostnames.

## Consequences
- **Positive:**
  - Deterministic, auditable Sysprep behavior (`iso` vs `clone`) with safe backward-compatible `auto` fallback.
  - Enables Stage 1 in-cluster ISO Golden Image builds onto persistent PVCs and collision-free `UDSVirtualMachinePool` Windows fleets.
- **Negative:**
  - Adds `provisioningMode` to `WindowsProfileSpec` and the `UDSVirtualMachine` CRD schema.
