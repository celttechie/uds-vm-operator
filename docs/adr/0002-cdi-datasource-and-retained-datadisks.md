# ADR 0002: CDI DataSource Fast Cloning & Decoupled Retained DataDisks

- **Status:** Accepted
- **Date:** 2026-11-13
- **Milestone:** v0.5.0 (Phases 5A & 5B)

## Context
Uploading multi-gigabyte golden OS images per VM instance is slow and impractical for fleet scaling. Additionally, stateful workloads (databases, telemetry stores) require application data to survive root OS replacements or VM deletion during rolling upgrades.

## Decision
1. **Centralized `DataSource` Cloning:** Support `spec.disk.sourceType: dataSource` (`dataSourceName`, `dataSourceNamespace`) in `UDSVirtualMachine`, translating to CDI `spec.sourceRef` (`kind: DataSource`) for storage-layer Copy-on-Write (CoW) cloning.
2. **Decoupled Persistent `dataDisks[]`:** Model secondary disks in `spec.dataDisks[]` with explicit `retentionPolicy: Retain | Delete`. When `Retain`, the operator intentionally **omits `OwnerReferences`** on the generated PVC so Kubernetes garbage collection preserves the volume if the parent `UDSVirtualMachine` is deleted.
3. **Zarf Air-Gap Provisioning (`noWait: true`):** In `zarf.yaml`, mark `golden-image-datasource` manifests with `noWait: true` because `source.upload` DataVolumes remain in `UploadReady` until an operator streams the disk image post-deploy, and label `uds-golden-images` with `zarf.dev/agent: ignore` so Zarf's mutating webhook does not rewrite CDI importer/cloner pod images.

## Consequences
- **Positive:** Sub-minute VM provisioning from verified golden images, safe stateful workload upgrades, and reliable offline Zarf deploys.
- **Negative:** Retained PVCs require explicit out-of-band cleanup when a stateful workload is permanently decommissioned.
