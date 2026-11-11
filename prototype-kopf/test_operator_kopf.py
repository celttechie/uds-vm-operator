"""
Unit tests for the Phase 1 Python/Kopf prototype (`vm_prep.py` and `operator_kopf.py`).
These exact test vectors will be mirrored 1:1 in `pkg/controller/reconciler_test.go`.
"""

import os
import subprocess
import tempfile

import pytest

from operator_kopf import (
    build_cloudinit_secret,
    build_datavolume,
    build_owner_reference,
    build_virtual_machine,
    build_zero_trust_network_policy,
    calculate_pvc_size_mib,
    evaluate_status_from_datavolume,
)
from vm_prep import compute_sha256, generate_udsvm_manifest, inspect_disk_image

SAMPLE_META = {
    "name": "tactical-sensor-vm",
    "namespace": "default",
    "uid": "11111111-2222-3333-4444-555555555555",
}

SAMPLE_SPEC = {
    "cpus": 2,
    "memory": "1Gi",
    "runStrategy": "Halted",
    "disk": {
        "sourceType": "upload",
        "format": "raw",
        "virtualSizeBytes": 10485760,  # 10 MiB
        "checksum": "sha256:e5b844cc57f57094ea4585e235f36c78c1cd222262bb89d53c94dcb4d6b3e55d",
    },
    "securityProfile": {
        "zeroTrustNetwork": True,
        "disallowSSHPassword": True,
        "fipsMode": True,
    },
}


def test_calculate_pvc_size_mib_overhead():
    # 10 MiB * 1.06 = 10.6 MiB -> rounds up to 11Mi
    assert calculate_pvc_size_mib(10 * 1024 * 1024) == "11Mi"
    # 1 GiB (1024 MiB) * 1.06 = 1085.44 MiB -> rounds up to 1086Mi
    assert calculate_pvc_size_mib(1024 * 1024 * 1024) == "1086Mi"


def test_build_zero_trust_network_policy():
    owner_ref = build_owner_reference(SAMPLE_META)
    netpol = build_zero_trust_network_policy("tactical-sensor-vm", "default", owner_ref)
    assert netpol["metadata"]["name"] == "tactical-sensor-vm-netpol"
    assert netpol["metadata"]["labels"]["uds.dev/nist-control"] == "SC-7"
    assert netpol["spec"]["podSelector"]["matchLabels"]["vm.kubevirt.io/name"] == "tactical-sensor-vm"
    assert netpol["spec"]["ingress"] == []
    assert len(netpol["spec"]["egress"]) == 1


def test_build_cloudinit_secret():
    owner_ref = build_owner_reference(SAMPLE_META)
    secret = build_cloudinit_secret("tactical-sensor-vm", "default", SAMPLE_SPEC, owner_ref)
    assert secret["metadata"]["name"] == "tactical-sensor-vm-cloudinit"
    userdata = secret["stringData"]["userdata"]
    assert "ssh_pwauth: false" in userdata
    assert "disable_root: true" in userdata
    assert "FIPS profile active" in userdata


def test_build_datavolume_upload_checksum():
    owner_ref = build_owner_reference(SAMPLE_META)
    dv = build_datavolume("tactical-sensor-vm", "default", SAMPLE_SPEC, owner_ref)
    assert dv["metadata"]["name"] == "tactical-sensor-vm-dv"
    assert dv["metadata"]["labels"]["uds.dev/nist-control"] == "SI-7"
    assert dv["spec"]["source"]["upload"]["checksum"] == SAMPLE_SPEC["disk"]["checksum"]
    assert dv["spec"]["storage"]["resources"]["requests"]["storage"] == "11Mi"


def test_build_virtual_machine():
    owner_ref = build_owner_reference(SAMPLE_META)
    vm = build_virtual_machine("tactical-sensor-vm", "default", SAMPLE_SPEC, owner_ref)
    assert vm["metadata"]["name"] == "tactical-sensor-vm"
    assert vm["spec"]["runStrategy"] == "Halted"
    volumes = vm["spec"]["template"]["spec"]["volumes"]
    assert volumes[0]["dataVolume"]["name"] == "tactical-sensor-vm-dv"
    assert volumes[1]["cloudInitNoCloud"]["secretRef"]["name"] == "tactical-sensor-vm-cloudinit"


def test_evaluate_status_lifecycle():
    # 1. UploadReady state
    dv_upload_ready = {
        "metadata": {"name": "tactical-sensor-vm-dv"},
        "status": {"phase": "UploadReady", "conditions": []},
    }
    st1 = evaluate_status_from_datavolume(SAMPLE_SPEC, dv_upload_ready)
    assert st1["phase"] == "WaitingForUpload"
    assert st1["compliance"]["checksumVerified"] is False
    assert st1["reconciledBy"] == "kopf-python"

    # 2. ChecksumError state (from CDI PR #4340)
    dv_checksum_err = {
        "metadata": {"name": "tactical-sensor-vm-dv"},
        "status": {
            "phase": "UploadReady",
            "conditions": [
                {
                    "type": "Running",
                    "status": "False",
                    "reason": "ChecksumError",
                    "message": "checksum mismatch: expected e5b8..., got 9999...",
                }
            ],
        },
    }
    st2 = evaluate_status_from_datavolume(SAMPLE_SPEC, dv_checksum_err)
    assert st2["phase"] == "IntegrityError"
    assert st2["compliance"]["checksumVerified"] is False
    assert "checksum mismatch" in st2["conditions"][0]["message"]

    # 3. Succeeded state
    dv_succeeded = {
        "metadata": {"name": "tactical-sensor-vm-dv"},
        "status": {"phase": "Succeeded", "conditions": []},
    }
    st3 = evaluate_status_from_datavolume(SAMPLE_SPEC, dv_succeeded)
    assert st3["phase"] == "Ready"
    assert st3["compliance"]["checksumVerified"] is True
    assert st3["compliance"]["zeroTrustIsolated"] is True


def test_vm_prep_inspect_and_backing_chain_rejection():
    with tempfile.TemporaryDirectory() as tmpdir:
        base_img = os.path.join(tmpdir, "base.raw")
        with open(base_img, "wb") as f:
            f.write(b"\x00" * (10 * 1024 * 1024))

        info = inspect_disk_image(base_img)
        assert info["format"] == "raw"
        assert info["virtual-size"] == 10485760

        manifest = generate_udsvm_manifest("test-vm", "default", base_img)
        assert manifest["spec"]["disk"]["virtualSizeBytes"] == 10485760
        assert manifest["spec"]["disk"]["checksum"] == compute_sha256(base_img)

        # Create a qcow2 overlay with a backing file — must be rejected for air-gap safety
        overlay_img = os.path.join(tmpdir, "overlay.qcow2")
        subprocess.run(
            ["qemu-img", "create", "-f", "qcow2", "-b", base_img, "-F", "raw", overlay_img],
            check=True,
            capture_output=True,
        )
        with pytest.raises(ValueError, match="unflattened backing chain"):
            inspect_disk_image(overlay_img)


def test_build_windows_virtual_machine_and_sysprep():
    win_meta = {
        "name": "tactical-windows-vm",
        "namespace": "default",
        "uid": "99999999-8888-7777-6666-555555555555",
    }
    win_spec = {
        "cpus": 4,
        "memory": "4Gi",
        "runStrategy": "Halted",
        "osType": "windows",
        "disk": {
            "sourceType": "upload",
            "format": "raw",
            "virtualSizeBytes": 21474836480,
            "checksum": "sha256:111122223333444455556666777788889999aaaabbbbccccddddeeeeffff0000",
        },
        "windowsProfile": {
            "diskBus": "virtio",
            "nicModel": "virtio",
            "injectVirtioWinISO": True,
            "enableHyperVEnlightenments": True,
            "enableSecureBootTPM": True,
            "persistentTPM": True,
        },
    }
    owner_ref = build_owner_reference(win_meta)

    # 1. Cloudinit Secret / Sysprep
    sec = build_cloudinit_secret("tactical-windows-vm", "default", win_spec, owner_ref)
    assert sec["metadata"]["name"] == "tactical-windows-vm-cloudinit"
    assert "<ComputerName>tactical-windows-vm</ComputerName>" in sec["stringData"]["autounattend.xml"]
    assert "<AdministratorPassword>" in sec["stringData"]["autounattend.xml"]

    # 2. VirtualMachine object
    vm = build_virtual_machine("tactical-windows-vm", "default", win_spec, owner_ref)
    domain = vm["spec"]["template"]["spec"]["domain"]
    features = domain["features"]
    assert "hyperv" in features
    assert "relaxed" in features["hyperv"]
    assert "vapic" in features["hyperv"]
    assert "spinlocks" in features["hyperv"]

    clock = domain["clock"]
    assert "hyperv" in clock["timer"]

    firmware = domain["firmware"]
    assert firmware["bootloader"]["efi"]["secureBoot"] is True
    assert domain["devices"]["tpm"]["persistent"] is True

    disks = domain["devices"]["disks"]
    assert len(disks) == 3
    assert disks[0]["name"] == "rootdisk"
    assert disks[1]["name"] == "sysprepdisk"
    assert disks[2]["name"] == "virtio-win-drivers"

    volumes = vm["spec"]["template"]["spec"]["volumes"]
    assert len(volumes) == 3
    assert volumes[1]["name"] == "sysprepdisk"
    assert volumes[1]["sysprep"]["secret"]["name"] == "tactical-windows-vm-cloudinit"
    assert volumes[2]["name"] == "virtio-win-drivers"

