# ADR 0003: `UDSVirtualMachinePool` for Zero-Downtime Rolling Fleet Updates

- **Status:** Accepted
- **Date:** 2026-11-13
- **Milestone:** v0.5.0 (Phase 5C)

## Context
KubeVirt provides a native `VirtualMachinePool` resource, but it operates directly on raw `kubevirt.io/VirtualMachine` templates and bypasses `UDSVirtualMachine` compliance bundling (`SC-7` NetworkPolicies, `AC-3`/`IA-5` hardened Cloud-Init/Sysprep secrets, `SI-7` CDI checksum verification, and `+6%` PVC sizing).

## Decision
Introduce `UDSVirtualMachinePool` (`uds.dev/v1alpha1`, shortName `udsvmpool`) reconciled by `PoolReconciler` (`pkg/controller/pool_reconciler.go`):
1. **Stamps `UDSVirtualMachine` Replicas:** Each pool replica is a full `UDSVirtualMachine` child object owned by the pool, inheriting all NIST 800-53 controls automatically.
2. **Deterministic Revision Hashing:** `ComputeTemplateHash` computes a 10-character SHA-256 digest of `spec.template.spec` and stamps it on `uds.dev/revision`.
3. **Readiness-Gated Surge & Drain:** During `RollingUpdate` (`maxSurge: 1`, `maxUnavailable: 0` defaults), `PoolReconciler` drains outdated-revision VMs only when `readyReplicas > desiredReplicas - maxUnavailable`, and surges replacement VMs up to `desiredReplicas + maxSurge`.

## Consequences
- **Positive:** Zero-downtime golden-image fleet rollouts with full NIST 800-53 compliance enforcement on every replica.
- **Negative:** Stateful pools requiring shared persistent storage must coordinate `pvcName` or per-replica storage classes carefully to avoid ReadWriteOnce multi-attach conflicts during surge.
