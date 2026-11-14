packer {
  required_plugins {
    qemu = {
      version = ">= 1.1.0"
      source  = "github.com/hashicorp/qemu"
    }
  }
}

variable "iso_url" {
  type        = string
  description = "Path or URL to the Windows Server 2022 ISO"
}

variable "iso_checksum" {
  type        = string
  description = "SHA-256 checksum of the Windows Server 2022 ISO (NIST 800-53 SI-7)"
}

variable "virtio_win_iso" {
  type        = string
  default     = "/usr/share/virtio-win/virtio-win.iso"
  description = "Path to virtio-win ISO containing VirtIO drivers and qemu-ga-x86_64.msi"
}

variable "disk_size" {
  type    = string
  default = "40960M"
}

variable "output_dir" {
  type    = string
  default = "output-win2022-golden"
}

source "qemu" "win2022" {
  iso_url          = var.iso_url
  iso_checksum     = var.iso_checksum
  output_directory = var.output_dir
  vm_name          = "win2022-golden-raw.qcow2"
  format           = "qcow2"
  disk_size        = var.disk_size
  disk_interface   = "virtio"
  net_device       = "virtio-net"
  accelerator      = "kvm"
  cpus             = 4
  memory           = 8192
  headless         = true

  efi_boot          = true
  efi_firmware_code = "/usr/share/OVMF/OVMF_CODE_4M.secboot.fd"
  efi_firmware_vars = "/usr/share/OVMF/OVMF_VARS_4M.ms.fd"

  vtpm       = true
  tpm_device = "tpm-tis"

  cd_files = [
    "./scripts/prepare-golden-image.ps1"
  ]
  cd_label = "UDS-SCRIPTS"

  floppy_files = [
    "./autounattend/Autounattend.xml"
  ]

  qemuargs = [
    ["-drive", "file=${var.virtio_win_iso},media=cdrom,index=2"]
  ]

  communicator   = "winrm"
  winrm_username = "Administrator"
  winrm_password = "UdsBuildTempP@ss2026!"
  winrm_timeout  = "45m"

  shutdown_command = "C:\\Windows\\System32\\Sysprep\\sysprep.exe /generalize /oobe /shutdown /quiet"
  shutdown_timeout = "15m"
}

build {
  sources = ["source.qemu.win2022"]

  provisioner "powershell" {
    inline = [
      "Get-ChildItem -Path D:\\,E:\\,F:\\ -Filter 'prepare-golden-image.ps1' -ErrorAction SilentlyContinue | ForEach-Object { & $_.FullName }"
    ]
  }

  post-processor "shell-local" {
    inline = [
      "echo 'Compressing generalized QCOW2 for fast SI-7 upload & Zarf packaging...'",
      "qemu-img convert -f qcow2 -O qcow2 -c ${var.output_dir}/win2022-golden-raw.qcow2 ${var.output_dir}/windows-server-2022-golden.qcow2",
      "rm -f ${var.output_dir}/win2022-golden-raw.qcow2",
      "sha256sum ${var.output_dir}/windows-server-2022-golden.qcow2 > ${var.output_dir}/windows-server-2022-golden.qcow2.sha256",
      "echo 'Done! Run: uds-vmctl prep --os windows --golden-image ${var.output_dir}/windows-server-2022-golden.qcow2'"
    ]
  }
}
