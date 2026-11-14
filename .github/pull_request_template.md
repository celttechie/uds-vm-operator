## Summary of Changes
<!-- Provide a clear, concise summary of the changes introduced in this PR. -->

## Problem & Motivation
<!-- What problem does this PR solve? Reference any relevant issue or JIRA ticket. -->

## NIST 800-53 Compliance Checklist
Check all applicable controls impacted or verified by this change:
- [ ] **SC-7 (Boundary Protection):** Zero-Trust NetworkPolicy isolation maintained or updated.
- [ ] **AC-3 / IA-5 (Access Control & Identification):** Cloud-Init hardening, SSH password restrictions, and FIPS enforcement verified.
- [ ] **SI-7 (Software & Information Integrity):** CDI DataVolume upload cryptographic checksum verification (`sha256:<hex>`) validated.
- [ ] **SA-11 (Developer Security Testing):** Automated Go (`go test -race`) and Python test suites pass without regressions.
- [ ] **CM-6 (Configuration Settings):** CRD and declarative manifests pass validation schema.

## Air-Gap & Edge Readiness
- [ ] No unflattened QCOW2 external backing chains introduced.
- [ ] All required artifacts/dependencies can be bundled in disconnected Zarf packages.
- [ ] +6% CDI filesystem overhead calculation validated for storage sizing.

## Testing & Verification
### Automated Tests
```bash
go test -v -race -cover -count=1 ./...
```

### Live Cluster Verification
<!-- Detail any live testing performed against K3s/KubeVirt clusters (e.g. virtctl image-upload, controller takeover). -->
- [ ] Tested on live K3s / KubeVirt cluster
- [ ] Verified status subresource transitions (`WaitingForUpload` -> `Ready`)

## Documentation
- [ ] `README.md` updated if API or operational behavior changed.
- [ ] CRD manifests updated and synchronized with Go types.
