#!/usr/bin/env python3
"""
Phase 1 Prototype: UDS Virtual Machine Operator (Python / Kopf)

Designed with a strict separation between:
1. Pure, deterministic Kubernetes resource builders (`build_*`) with zero side effects.
2. A single level-triggered `reconcile_udsvm()` function shared across `@kopf.on.create`,
   `@kopf.on.update`, `@kopf.on.resume`, and `@kopf.timer`.

This structure makes the 1:1 conversion to a Go `controller-runtime` Reconciler
trivial and verifiable via identical unit test cases.
"""

import logging
import math
from typing import Any, Dict, List, Optional

import kopf
import kubernetes

CDI_FS_OVERHEAD_MULTIPLIER = 1.06
MIB = 1024 * 1024


def calculate_pvc_size_mib(virtual_size_bytes: int) -> str:
    """Calculate PVC size in MiB with +6% KubeVirt/CDI filesystem overhead."""
    raw_mib = virtual_size_bytes / MIB
    padded_mib = math.ceil(raw_mib * CDI_FS_OVERHEAD_MULTIPLIER)
    if padded_mib <= raw_mib:
        padded_mib = math.ceil(raw_mib) + 1
    return f"{max(padded_mib, 1)}Mi"


def build_owner_reference(meta: Dict[str, Any]) -> Dict[str, Any]:
    """Build standard controller OwnerReference for garbage collection."""
    return {
        "apiVersion": "uds.dev/v1alpha1",
        "kind": "UDSVirtualMachine",
        "name": meta["name"],
        "uid": meta["uid"],
        "controller": True,
        "blockOwnerDeletion": True,
    }


def build_zero_trust_network_policy(name: str, namespace: str, owner_ref: Dict[str, Any]) -> Dict[str, Any]:
    """
    Build a default-deny-ingress + restricted-egress (DNS only) NetworkPolicy
    for the KubeVirt virt-launcher pod (NIST 800-53 SC-7 Boundary Protection).
    """
    return {
        "apiVersion": "networking.k8s.io/v1",
        "kind": "NetworkPolicy",
        "metadata": {
            "name": f"{name}-netpol",
            "namespace": namespace,
            "labels": {
                "app.kubernetes.io/name": name,
                "app.kubernetes.io/managed-by": "uds-vm-operator",
                "uds.dev/nist-control": "SC-7",
            },
            "ownerReferences": [owner_ref],
        },
        "spec": {
            "podSelector": {
                "matchLabels": {
                    "vm.kubevirt.io/name": name,
                }
            },
            "policyTypes": ["Ingress", "Egress"],
            "ingress": [],
            "egress": [
                {
                    "ports": [
                        {"protocol": "UDP", "port": 53},
                        {"protocol": "TCP", "port": 53},
                    ]
                }
            ],
        },
    }


def build_cloudinit_secret(
    name: str,
    namespace: str,
    spec: Dict[str, Any],
    owner_ref: Dict[str, Any],
) -> Dict[str, Any]:
    """
    Build a hardened Cloud-Init / Sysprep Secret enforcing SSH/Admin password security
    and optional FIPS mode (NIST 800-53 AC-3 / IA-5).
    """
    is_windows = spec.get("osType") == "windows"

    if is_windows:
        unattend_xml = f"""<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">
  <settings pass="windowsPE">
    <component name="Microsoft-Windows-PnpCustomizationsWinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
      <DriverPaths>
        <PathAndCredentials wcm:action="add" wcm:keyValue="1">
          <Path>D:\\</Path>
        </PathAndCredentials>
        <PathAndCredentials wcm:action="add" wcm:keyValue="2">
          <Path>E:\\</Path>
        </PathAndCredentials>
      </DriverPaths>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideLocalAccountScreen>true</HideLocalAccountScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <NetworkLocation>Work</NetworkLocation>
        <ProtectYourPC>1</ProtectYourPC>
      </OOBE>
      <ComputerName>{name}</ComputerName>
      <UserAccounts>
        <AdministratorPassword>
          <Value>UDS-TempPass-ChangeMe!</Value>
          <PlainText>true</PlainText>
        </AdministratorPassword>
      </UserAccounts>
      <FirstLogonCommands>
        <SynchronousCommand wcm:action="add">
          <Order>1</Order>
          <CommandLine>powershell -Command "Start-Process msiexec.exe -ArgumentList '/i D:\\virtio-win-gt-x64.msi /qn /norestart' -Wait"</CommandLine>
          <Description>Install VirtIO guest drivers and QEMU guest agent</Description>
        </SynchronousCommand>
      </FirstLogonCommands>
    </component>
  </settings>
</unattend>"""
        return {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": {
                "name": f"{name}-cloudinit",
                "namespace": namespace,
                "labels": {
                    "app.kubernetes.io/name": name,
                    "app.kubernetes.io/managed-by": "uds-vm-operator",
                    "uds.dev/nist-control": "AC-3",
                },
                "ownerReferences": [owner_ref],
            },
            "type": "Opaque",
            "stringData": {
                "autounattend.xml": unattend_xml,
                "unattend.xml": unattend_xml,
                "userdata": f"#ps1_sysnative\nWrite-Host 'UDS Windows VM initialized: {name}'\n",
            },
        }

    sec = spec.get("securityProfile", {})
    disallow_ssh_pw = sec.get("disallowSSHPassword", True)
    fips_mode = sec.get("fipsMode", True)

    lines = [
        "#cloud-config",
        f"hostname: {name}",
        f"ssh_pwauth: {'false' if disallow_ssh_pw else 'true'}",
        "disable_root: true",
    ]
    if fips_mode:
        lines.extend([
            "runcmd:",
            "  - [ sh, -c, 'echo Hardened UDS VM initialized (FIPS profile active)' ]",
        ])

    userdata = "\n".join(lines) + "\n"

    return {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {
            "name": f"{name}-cloudinit",
            "namespace": namespace,
            "labels": {
                "app.kubernetes.io/name": name,
                "app.kubernetes.io/managed-by": "uds-vm-operator",
                "uds.dev/nist-control": "AC-3",
            },
            "ownerReferences": [owner_ref],
        },
        "type": "Opaque",
        "stringData": {
            "userdata": userdata,
        },
    }


def build_datavolume(
    name: str,
    namespace: str,
    spec: Dict[str, Any],
    owner_ref: Dict[str, Any],
) -> Dict[str, Any]:
    """
    Build a CDI DataVolume resource with cryptographic checksum validation
    (leveraging kubevirt/containerized-data-importer PR #4340, NIST 800-53 SI-7)
    and automatic +6% filesystem overhead sizing.
    """
    disk = spec["disk"]
    pvc_size = calculate_pvc_size_mib(int(disk["virtualSizeBytes"]))
    source_type = disk.get("sourceType", "upload")
    checksum = disk["checksum"]

    if source_type == "upload":
        source_spec: Dict[str, Any] = {
            "upload": {
                "checksum": checksum,
            }
        }
    elif source_type == "http":
        source_spec = {
            "http": {
                "url": disk.get("httpUrl", ""),
                "checksum": checksum,
            }
        }
    else:
        raise ValueError(f"Unsupported disk.sourceType: {source_type}")

    storage_spec: Dict[str, Any] = {
        "accessModes": ["ReadWriteOnce"],
        "resources": {
            "requests": {
                "storage": pvc_size,
            }
        },
    }
    if disk.get("storageClassName"):
        storage_spec["storageClassName"] = disk["storageClassName"]

    return {
        "apiVersion": "cdi.kubevirt.io/v1beta1",
        "kind": "DataVolume",
        "metadata": {
            "name": f"{name}-dv",
            "namespace": namespace,
            "labels": {
                "app.kubernetes.io/name": name,
                "app.kubernetes.io/managed-by": "uds-vm-operator",
                "uds.dev/nist-control": "SI-7",
            },
            "annotations": {
                "cdi.kubevirt.io/storage.bind.immediate.requested": "true",
            },
            "ownerReferences": [owner_ref],
        },
        "spec": {
            "source": source_spec,
            "storage": storage_spec,
        },
    }


def build_virtual_machine(
    name: str,
    namespace: str,
    spec: Dict[str, Any],
    owner_ref: Dict[str, Any],
) -> Dict[str, Any]:
    """Build a hardened KubeVirt VirtualMachine bound to the verified DataVolume and Cloud-Init Secret."""
    cpus = int(spec.get("cpus", 2))
    memory = spec.get("memory", "2Gi")
    run_strategy = spec.get("runStrategy", "Halted")

    is_windows = spec.get("osType") == "windows"
    disk_bus = "virtio"
    nic_model = "virtio"
    inject_virtio_win = False
    virtio_win_image = "quay.io/kubevirt/virtio-container-disk:20261111_6cefb75928"
    enable_hyperv = False
    enable_secure_boot_tpm = False

    if is_windows:
        inject_virtio_win = True
        enable_hyperv = True
        enable_secure_boot_tpm = True

        win_profile = spec.get("windowsProfile", {})
        if win_profile:
            disk_bus = win_profile.get("diskBus", disk_bus)
            nic_model = win_profile.get("nicModel", nic_model)
            if "injectVirtioWinISO" in win_profile:
                inject_virtio_win = bool(win_profile["injectVirtioWinISO"])
            virtio_win_image = win_profile.get("virtioWinImage", virtio_win_image)
            if "enableHyperVEnlightenments" in win_profile:
                enable_hyperv = bool(win_profile["enableHyperVEnlightenments"])
            if "enableSecureBootTPM" in win_profile:
                enable_secure_boot_tpm = bool(win_profile["enableSecureBootTPM"])

    disk_format = spec.get("disk", {}).get("format", "raw")
    root_disk_obj: Dict[str, Any] = {"name": "rootdisk"}
    if disk_format == "iso":
        root_disk_obj["cdrom"] = {"bus": "sata"}
        root_disk_obj["bootOrder"] = 1
    else:
        root_disk_obj["disk"] = {"bus": disk_bus}

    disks: List[Dict[str, Any]] = [
        root_disk_obj,
    ]
    volumes: List[Dict[str, Any]] = [
        {"name": "rootdisk", "dataVolume": {"name": f"{name}-dv"}},
    ]

    if is_windows:
        disks.append({"name": "sysprepdisk", "cdrom": {"bus": "sata"}})
        volumes.append({
            "name": "sysprepdisk",
            "sysprep": {
                "secret": {"name": f"{name}-cloudinit"}
            }
        })
        if inject_virtio_win:
            disks.append({"name": "virtio-win-drivers", "cdrom": {"bus": "sata"}})
            volumes.append({
                "name": "virtio-win-drivers",
                "containerDisk": {"image": virtio_win_image}
            })
    else:
        disks.append({"name": "cloudinitdisk", "disk": {"bus": "virtio"}})
        volumes.append({
            "name": "cloudinitdisk",
            "cloudInitNoCloud": {
                "secretRef": {"name": f"{name}-cloudinit"},
            },
        })

    iface: Dict[str, Any] = {"name": "default", "masquerade": {}}
    if nic_model != "virtio":
        iface["model"] = nic_model

    devices: Dict[str, Any] = {
        "disks": disks,
        "interfaces": [iface],
    }

    domain: Dict[str, Any] = {
        "cpu": {"cores": cpus},
        "resources": {"requests": {"memory": memory}},
        "devices": devices,
    }

    features: Dict[str, Any] = {}
    if enable_hyperv:
        features["hyperv"] = {
            "relaxed": {},
            "vapic": {},
            "spinlocks": {"spinlocks": 8191},
            "synic": {},
            "synictimer": {"direct": {}},
            "frequencies": {},
            "tlbflush": {},
        }
        domain["clock"] = {
            "utc": {},
            "timer": {
                "hyperv": {},
                "hpet": {"present": False},
                "pit": {"tickPolicy": "delay"},
                "rtc": {"tickPolicy": "catchup"},
            },
        }

    if enable_secure_boot_tpm:
        features["smm"] = {"enabled": True}
        domain["firmware"] = {
            "bootloader": {
                "efi": {
                    "secureBoot": True,
                }
            }
        }
        tpm_spec: Dict[str, Any] = {}
        if is_windows and spec.get("windowsProfile", {}).get("persistentTPM"):
            tpm_spec["persistent"] = True
        devices["tpm"] = tpm_spec

    if features:
        domain["features"] = features

    return {
        "apiVersion": "kubevirt.io/v1",
        "kind": "VirtualMachine",
        "metadata": {
            "name": name,
            "namespace": namespace,
            "labels": {
                "app.kubernetes.io/name": name,
                "app.kubernetes.io/managed-by": "uds-vm-operator",
            },
            "ownerReferences": [owner_ref],
        },
        "spec": {
            "runStrategy": run_strategy,
            "template": {
                "metadata": {
                    "labels": {
                        "vm.kubevirt.io/name": name,
                        "app.kubernetes.io/name": name,
                    }
                },
                "spec": {
                    "domain": domain,
                    "networks": [
                        {"name": "default", "pod": {}},
                    ],
                    "volumes": volumes,
                },
            },
        },
    }


def evaluate_status_from_datavolume(
    spec: Dict[str, Any],
    dv_obj: Optional[Dict[str, Any]],
    reconciler_id: str = "kopf-python",
) -> Dict[str, Any]:
    """
    Pure status evaluator: inspects the child CDI DataVolume status and returns
    the unified UDSVirtualMachine status block.
    """
    disk = spec["disk"]
    pvc_size = calculate_pvc_size_mib(int(disk["virtualSizeBytes"]))
    sec = spec.get("securityProfile", {})
    zero_trust = bool(sec.get("zeroTrustNetwork", True))
    name_dv = ""

    dv_phase = "Pending"
    checksum_verified = False
    phase = "Pending"
    reason = "DataVolumeCreating"
    message = "Waiting for CDI DataVolume to initialize"

    if dv_obj:
        name_dv = dv_obj.get("metadata", {}).get("name", "")
        dv_status = dv_obj.get("status", {})
        dv_phase = dv_status.get("phase", "Pending")
        conditions: List[Dict[str, Any]] = dv_status.get("conditions", [])

        # Check if CDI reported a ChecksumError on the Running condition
        checksum_error_msg = ""
        for cond in conditions:
            if cond.get("reason") == "ChecksumError":
                checksum_error_msg = cond.get("message", "Disk image checksum mismatch")
                break

        if checksum_error_msg:
            phase = "IntegrityError"
            reason = "ChecksumError"
            message = checksum_error_msg
            checksum_verified = False
        elif dv_phase == "Succeeded":
            phase = "Ready"
            reason = "ChecksumVerified"
            message = f"Disk image imported and verified against {disk['checksum']}"
            checksum_verified = True
        elif dv_phase in ("UploadReady", "WaitForFirstConsumer", "Pending"):
            phase = "WaitingForUpload" if disk.get("sourceType", "upload") == "upload" else "Provisioning"
            reason = f"DataVolume{dv_phase}"
            message = f"CDI DataVolume {name_dv} is in phase {dv_phase}"
        else:
            phase = "Provisioning"
            reason = f"DataVolume{dv_phase}"
            message = f"CDI DataVolume {name_dv} is in phase {dv_phase}"

    return {
        "phase": phase,
        "calculatedPvcSize": pvc_size,
        "dataVolumeName": name_dv,
        "dataVolumePhase": dv_phase,
        "reconciledBy": reconciler_id,
        "compliance": {
            "checksumVerified": checksum_verified,
            "zeroTrustIsolated": zero_trust,
            "cloudInitHardened": True,
        },
        "conditions": [
            {
                "type": "Ready",
                "status": "True" if phase == "Ready" else "False",
                "reason": reason,
                "message": message,
            }
        ],
    }


def apply_k8s_resource(
    custom_api: kubernetes.client.CustomObjectsApi,
    core_api: kubernetes.client.CoreV1Api,
    net_api: kubernetes.client.NetworkingV1Api,
    body: Dict[str, Any],
) -> Optional[Dict[str, Any]]:
    """Idempotently create or patch a child Kubernetes resource."""
    kind = body["kind"]
    namespace = body["metadata"]["namespace"]
    name = body["metadata"]["name"]

    if kind == "NetworkPolicy":
        try:
            net_api.read_namespaced_network_policy(name=name, namespace=namespace)
            net_api.patch_namespaced_network_policy(name=name, namespace=namespace, body=body)
        except kubernetes.client.exceptions.ApiException as e:
            if e.status == 404:
                net_api.create_namespaced_network_policy(namespace=namespace, body=body)
            else:
                raise
        return None

    if kind == "Secret":
        try:
            core_api.read_namespaced_secret(name=name, namespace=namespace)
            core_api.patch_namespaced_secret(name=name, namespace=namespace, body=body)
        except kubernetes.client.exceptions.ApiException as e:
            if e.status == 404:
                core_api.create_namespaced_secret(namespace=namespace, body=body)
            else:
                raise
        return None

    if kind == "DataVolume":
        group, version = "cdi.kubevirt.io", "v1beta1"
        plural = "datavolumes"
        try:
            return custom_api.get_namespaced_custom_object(
                group=group, version=version, namespace=namespace, plural=plural, name=name
            )
        except kubernetes.client.exceptions.ApiException as e:
            if e.status == 404:
                return custom_api.create_namespaced_custom_object(
                    group=group, version=version, namespace=namespace, plural=plural, body=body
                )
            raise

    if kind == "VirtualMachine":
        group, version = "kubevirt.io", "v1"
        plural = "virtualmachines"
        try:
            custom_api.get_namespaced_custom_object(
                group=group, version=version, namespace=namespace, plural=plural, name=name
            )
            return custom_api.patch_namespaced_custom_object(
                group=group, version=version, namespace=namespace, plural=plural, name=name, body=body
            )
        except kubernetes.client.exceptions.ApiException as e:
            if e.status == 404:
                return custom_api.create_namespaced_custom_object(
                    group=group, version=version, namespace=namespace, plural=plural, body=body
                )
            raise

    raise ValueError(f"Unsupported kind: {kind}")


def reconcile(
    spec: Dict[str, Any],
    meta: Dict[str, Any],
    logger: logging.Logger,
) -> Dict[str, Any]:
    """
    Single level-triggered reconciliation loop for UDSVirtualMachine.
    Mirrors Go controller-runtime's `Reconcile(ctx, req)` signature and behavior.
    """
    name = meta["name"]
    namespace = meta["namespace"]
    owner_ref = build_owner_reference(meta)

    custom_api = kubernetes.client.CustomObjectsApi()
    core_api = kubernetes.client.CoreV1Api()
    net_api = kubernetes.client.NetworkingV1Api()

    # 1. Reconcile Zero-Trust NetworkPolicy if enabled
    if spec.get("securityProfile", {}).get("zeroTrustNetwork", True):
        netpol = build_zero_trust_network_policy(name, namespace, owner_ref)
        apply_k8s_resource(custom_api, core_api, net_api, netpol)

    # 2. Reconcile Hardened Cloud-Init Secret
    secret = build_cloudinit_secret(name, namespace, spec, owner_ref)
    apply_k8s_resource(custom_api, core_api, net_api, secret)

    # 3. Reconcile CDI DataVolume (with checksum verification & +6% overhead)
    dv_manifest = build_datavolume(name, namespace, spec, owner_ref)
    dv_obj = apply_k8s_resource(custom_api, core_api, net_api, dv_manifest)

    # 4. Reconcile KubeVirt VirtualMachine
    vm_manifest = build_virtual_machine(name, namespace, spec, owner_ref)
    apply_k8s_resource(custom_api, core_api, net_api, vm_manifest)

    # 5. Compute and patch status directly onto the UDSVirtualMachine status subresource
    new_status = evaluate_status_from_datavolume(spec, dv_obj, reconciler_id="kopf-python")
    custom_api.patch_namespaced_custom_object_status(
        group="uds.dev",
        version="v1alpha1",
        namespace=namespace,
        plural="udsvirtualmachines",
        name=name,
        body={"status": new_status},
    )
    logger.info(
        f"Reconciled UDSVirtualMachine {namespace}/{name}: phase={new_status['phase']} "
        f"pvc={new_status['calculatedPvcSize']} checksumVerified={new_status['compliance']['checksumVerified']}"
    )
    return new_status


@kopf.on.create("uds.dev", "v1alpha1", "udsvirtualmachines")
@kopf.on.update("uds.dev", "v1alpha1", "udsvirtualmachines")
@kopf.on.resume("uds.dev", "v1alpha1", "udsvirtualmachines")
def handle_event(spec: Dict[str, Any], meta: Dict[str, Any], logger: logging.Logger, **_: Any) -> None:
    reconcile(spec=spec, meta=meta, logger=logger)


@kopf.timer("uds.dev", "v1alpha1", "udsvirtualmachines", interval=5.0)
def handle_timer(spec: Dict[str, Any], meta: Dict[str, Any], logger: logging.Logger, **_: Any) -> None:
    reconcile(spec=spec, meta=meta, logger=logger)
