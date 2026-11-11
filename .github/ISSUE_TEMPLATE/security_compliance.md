---
name: Security & NIST 800-53 Compliance Issue
about: Report a vulnerability, STIG deviation, or NIST compliance gap
title: "[SECURITY]: "
labels: ["security", "nist-800-53"]
assignees: []
---

### Affected NIST 800-53 Control
- [ ] SC-7 Boundary Protection / Zero-Trust NetworkPolicy
- [ ] AC-3 / IA-5 Access Control & Cloud-Init Hardening
- [ ] SI-7 Software & Information Integrity / Image Checksums
- [ ] CM-6 Configuration Settings
- [ ] Other: 

### Description of the Security Gap or Vulnerability
<!-- Provide a clear and concise description of the security issue. -->

### Impact in Disconnected / Air-Gap Environments
<!-- Does this issue allow unauthorized egress, unverified disk execution, or credential exposure across an air gap? -->

### Steps to Reproduce
1. Apply manifest `...`
2. Inspect child resource `...`
3. Observe security posture failure `...`

### Expected Hardened Behavior
<!-- What should the operator do to satisfy the compliance control? -->

### Suggested Fix or Remediation
<!-- Any suggested changes to pkg/controller/reconciler.go, CRD, or Cloud-Init templates? -->
