# Golden Windows 11 image, built unattended and offline.
#
# Driven by `winslot image build`, which stages the WINCFG content (answer
# files, payload, virtio drivers, admin keys) into var.wincfg and runs
# `packer build`. What happens inside the VM is defined by answer/:
# Setup (vioscsi from WINCFG) -> Audit Mode -> provision.ps1 -> Sysprep ->
# power off. Packer only boots it, presses the "boot from CD" key, and waits
# for the power-off. The result is a qcow2 that `winslot image import`
# verifies and stores.

packer {
  required_plugins {
    qemu = {
      source  = "github.com/hashicorp/qemu"
      version = "~> 1.1"
    }
  }
}

variable "iso" {
  type        = string
  description = "Windows 11 install ISO (local path)"
}
variable "iso_sha256" {
  type        = string
  description = "sha256 of the ISO"
}
variable "wincfg" {
  type        = string
  description = "staged WINCFG directory (answer files, payload, drivers)"
}
variable "output_dir" {
  type = string
}
variable "name" {
  type        = string
  description = "image name, e.g. win11-pro-20261011-0400"
}

source "qemu" "win11" {
  iso_url      = var.iso
  iso_checksum = "sha256:${var.iso_sha256}"

  # Hardware: identical to what winslot gives each VM, so Sysprep /mode:vm holds.
  machine_type   = "pc-q35-8.2"
  accelerator    = "kvm"
  cpu_model      = "IvyBridge-IBRS"
  cpus           = 4
  sockets        = 1             # Windows 11 Pro uses at most 2 sockets: present 1 socket x 4 cores
  cores          = 4
  threads        = 1
  memory         = 8192
  disk_size      = "80G"
  disk_interface = "virtio-scsi"
  disk_discard   = "unmap"
  disk_cache     = "none"
  format         = "qcow2"
  net_device     = "virtio-net"

  # UEFI with Secure Boot (Microsoft keys) and a TPM 2.0, as Windows 11 requires.
  efi_boot          = true
  efi_firmware_code = "/usr/share/OVMF/OVMF_CODE_4M.ms.fd"
  efi_firmware_vars = "/usr/share/OVMF/OVMF_VARS_4M.ms.fd"
  vtpm              = true

  # Answer files, payload and drivers on a second CD labelled WINCFG.
  cd_files = ["${var.wincfg}/"]
  cd_label = "WINCFG"

  qemuargs = [
    # Secure Boot firmware must run with protected flash.
    ["-global", "driver=cfi.pflash01,property=secure,value=on"],
    # Offline build: the NIC exists (so the virtio-net driver binds) but SLIRP
    # is restricted, so the guest can reach nothing (no Windows Update/Store).
    ["-netdev", "user,id=user.0,restrict=on"],
    # provision.ps1 reports progress on COM1.
    ["-serial", "file:${var.output_dir}/serial.log"],
  ]

  # Get past "Press any key to boot from CD or DVD"; later reboots fall
  # through to the disk because nobody presses a key.
  boot_wait    = "1s"
  boot_command = ["<spacebar><wait1s><spacebar><wait1s><spacebar><wait1s><spacebar><wait1s><spacebar><wait1s><spacebar><wait1s><spacebar><wait1s><spacebar><wait1s><spacebar><wait1s><spacebar>"]

  # No communicator: Sysprep powers the VM off when provisioning is done.
  communicator     = "none"
  shutdown_timeout = "120m"

  headless         = true
  vnc_bind_address = "127.0.0.1"
  output_directory = var.output_dir
  vm_name          = "${var.name}.qcow2"
}

build {
  sources = ["source.qemu.win11"]
}
