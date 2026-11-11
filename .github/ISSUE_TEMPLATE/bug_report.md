---
name: Bug Report
about: Create a report to help us fix a bug in the operator or prep CLI
title: "[BUG]: "
labels: ["bug"]
assignees: []
---

### Describe the Bug
<!-- A clear and concise description of what the bug is. -->

### Environment
- **Kubernetes / K3s Version:**
- **KubeVirt Version:**
- **CDI Version:**
- **uds-vm-operator Version/Commit:**
- **StorageClass / VolumeMode:** (e.g. local-path, Longhorn, Ceph, filesystem vs block)

### Steps to Reproduce
1. Apply UDSVirtualMachine: `...`
2. Perform upload or reconciliation: `...`
3. Check `kubectl get udsvm <name> -o yaml`: `...`
4. Error observed: `...`

### Expected Behavior
<!-- What you expected to happen. -->

### Operator Logs & Resource Status
```console
# Paste operator logs or kubectl get udsvm output here
```

### Additional Context
<!-- Any details about air-gap environment, disk format (raw/qcow2), backing chains, etc. -->
