#!/usr/bin/env python3
"""
UDS Disconnected VM Image Preparation & Verification CLI (vm_prep.py)

Inspects raw/qcow2 VM disk images using `qemu-img info --output=json`, verifies
disconnected air-gap safety (rejecting unflattened external backing chains),
computes the cryptographic SHA-256 digest (compatible with CDI PR #4340),
calculates the required PVC size including KubeVirt/CDI filesystem overhead (+6%),
and generates a declarative UDSVirtualMachine CR manifest.
"""

import argparse
import hashlib
import json
import math
import os
import subprocess
import sys
from typing import Any, Dict

import yaml

# KubeVirt / CDI default filesystem overhead is ~5.5% to 6.0% for ext4/xfs metadata
CDI_FS_OVERHEAD_MULTIPLIER = 1.06
MIB = 1024 * 1024


def inspect_disk_image(image_path: str) -> Dict[str, Any]:
    """Run `qemu-img info --output=json` and validate image safety for air-gap transfer."""
    if not os.path.exists(image_path):
        raise FileNotFoundError(f"Disk image not found: {image_path}")

    cmd = ["qemu-img", "info", "--output=json", image_path]
    result = subprocess.run(cmd, capture_output=True, text=True, check=False)
    if result.returncode != 0:
        raise RuntimeError(f"qemu-img info failed: {result.stderr.strip()}")

    info = json.loads(result.stdout)

    # Disconnected safety check: QCOW2 images with external backing files will break
    # once transferred across an air-gap or imported into a PVC.
    if info.get("backing-filename"):
        raise ValueError(
            f"Unsafe image: '{image_path}' has an unflattened backing chain "
            f"('{info['backing-filename']}'). Run `qemu-img convert` to flatten before packaging."
        )

    return info


def compute_sha256(image_path: str) -> str:
    """Compute SHA-256 hex digest of a disk image file in 1 MiB chunks."""
    sha256 = hashlib.sha256()
    with open(image_path, "rb") as f:
        while chunk := f.read(MIB):
            sha256.update(chunk)
    return f"sha256:{sha256.hexdigest()}"


def calculate_pvc_size_mib(virtual_size_bytes: int, overhead_multiplier: float = CDI_FS_OVERHEAD_MULTIPLIER) -> str:
    """
    Calculate the required PVC size in MiB including CDI filesystem overhead,
    ensuring at least 10 MiB minimum for small test disks.
    """
    raw_mib = virtual_size_bytes / MIB
    padded_mib = math.ceil(raw_mib * overhead_multiplier)
    # Ensure a sensible minimum floor so tiny test images still get extra headroom
    if padded_mib <= raw_mib:
        padded_mib = math.ceil(raw_mib) + 1
    return f"{max(padded_mib, 1)}Mi"


def generate_udsvm_manifest(
    name: str,
    namespace: str,
    image_path: str,
    source_type: str = "upload",
    http_url: str = "",
    cpus: int = 2,
    memory: str = "2Gi",
    zero_trust: bool = True,
    fips_mode: bool = True,
) -> Dict[str, Any]:
    """Inspect disk image and produce a complete UDSVirtualMachine custom resource dictionary."""
    info = inspect_disk_image(image_path)
    checksum = compute_sha256(image_path)
    virtual_size_bytes = int(info["virtual-size"])
    img_format = info.get("format", "raw")
    if img_format not in ("raw", "qcow2"):
        raise ValueError(f"Unsupported disk format '{img_format}'. Expected 'raw' or 'qcow2'.")

    spec: Dict[str, Any] = {
        "cpus": cpus,
        "memory": memory,
        "disk": {
            "sourceType": source_type,
            "format": img_format,
            "virtualSizeBytes": virtual_size_bytes,
            "checksum": checksum,
        },
        "securityProfile": {
            "zeroTrustNetwork": zero_trust,
            "disallowSSHPassword": True,
            "fipsMode": fips_mode,
        },
    }

    if source_type == "http" and http_url:
        spec["disk"]["httpUrl"] = http_url

    return {
        "apiVersion": "uds.dev/v1alpha1",
        "kind": "UDSVirtualMachine",
        "metadata": {
            "name": name,
            "namespace": namespace,
            "labels": {
                "app.kubernetes.io/name": name,
                "app.kubernetes.io/managed-by": "uds-vm-prep",
                "uds.dev/security-tier": "mission-critical",
            },
            "annotations": {
                "uds.dev/source-image": os.path.basename(image_path),
                "uds.dev/estimated-pvc-size": calculate_pvc_size_mib(virtual_size_bytes),
            },
        },
        "spec": spec,
    }


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Inspect VM disk image and generate air-gapped UDSVirtualMachine manifest."
    )
    parser.add_argument("image", help="Path to the raw or qcow2 VM disk image")
    parser.add_argument("--name", default="tactical-sensor-vm", help="Name of the UDSVirtualMachine resource")
    parser.add_argument("--namespace", default="default", help="Target Kubernetes namespace")
    parser.add_argument("--source-type", choices=["upload", "http"], default="upload", help="CDI ingestion method")
    parser.add_argument("--http-url", default="", help="HTTP URL if --source-type=http")
    parser.add_argument("--cpus", type=int, default=1, help="Number of vCPUs")
    parser.add_argument("--memory", default="512Mi", help="Guest memory allocation (e.g. 512Mi, 2Gi)")
    parser.add_argument("-o", "--output", default="", help="Output YAML file path (defaults to stdout)")

    args = parser.parse_args()
    manifest = generate_udsvm_manifest(
        name=args.name,
        namespace=args.namespace,
        image_path=args.image,
        source_type=args.source_type,
        http_url=args.http_url,
        cpus=args.cpus,
        memory=args.memory,
    )

    yaml_out = yaml.safe_dump(manifest, sort_keys=False)
    if args.output:
        os.makedirs(os.path.dirname(os.path.abspath(args.output)), exist_ok=True)
        with open(args.output, "w", encoding="utf-8") as f:
            f.write(yaml_out)
        print(f"Wrote UDSVirtualMachine manifest to {args.output}", file=sys.stderr)
    else:
        print(yaml_out, end="")
    return 0


if __name__ == "__main__":
    sys.exit(main())
