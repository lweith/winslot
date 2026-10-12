// Package domain builds the libvirt domain definition of a license slot's VM.
//
// The hardware matches what the golden image was built on (Packer: q35,
// virtio-scsi, virtio-net, TPM TIS 2.0, UEFI Secure Boot), so Sysprep's
// /mode:vm assumption holds, and it carries the slot's identity verbatim.
package domain

import (
	"encoding/xml"
	"fmt"

	"libvirt.org/go/libvirtxml"

	"winslot/internal/slot"
)

// Spec is everything that varies between slots.
type Spec struct {
	Name        string
	Title       string
	Description string
	Identity    slot.Identity
	MemoryMiB   uint
	VCPUs       uint
	DiskPath    string // qcow2
	Network     string // libvirt network
	SerialLog   string // COM1 output file (first-boot host keys are read from it)
	// Nested exposes hardware virtualization to the guest (WSL2, Docker
	// Desktop, VBS/HVCI). Off by default: it widens the host's attack surface.
	Nested bool
	// VirtFeature is the host CPU's virtualization flag, "vmx" (Intel) or "svm" (AMD).
	VirtFeature string
}

func uintp(v uint) *uint { return &v }

// Build returns the domain for s.
func Build(s Spec) (*libvirtxml.Domain, error) {
	if err := slot.ValidateName(s.Name); err != nil {
		return nil, err
	}
	if s.VCPUs == 0 || s.MemoryMiB == 0 || s.DiskPath == "" || s.Network == "" || s.SerialLog == "" {
		return nil, fmt.Errorf("domain %s: incomplete spec", s.Name)
	}
	if s.VirtFeature != "vmx" && s.VirtFeature != "svm" {
		return nil, fmt.Errorf("domain %s: VirtFeature must be vmx or svm, got %q", s.Name, s.VirtFeature)
	}
	id := s.Identity

	cpu := &libvirtxml.DomainCPU{
		Mode: "host-passthrough", Check: "none", Migratable: "on",
		// One socket: Windows 11 Pro uses at most two.
		Topology: &libvirtxml.DomainCPUTopology{Sockets: 1, Dies: 1, Cores: int(s.VCPUs), Threads: 1},
	}
	if !s.Nested {
		cpu.Features = []libvirtxml.DomainCPUFeature{{Policy: "disable", Name: s.VirtFeature}}
	}

	return &libvirtxml.Domain{
		Type:        "kvm",
		Name:        s.Name,
		UUID:        id.UUID,
		Title:       s.Title,
		Description: s.Description,
		Memory:      &libvirtxml.DomainMemory{Value: s.MemoryMiB, Unit: "MiB"},
		VCPU:        &libvirtxml.DomainVCPU{Placement: "static", Value: s.VCPUs},
		OS: &libvirtxml.DomainOS{
			Firmware: "efi",
			Type:     &libvirtxml.DomainOSType{Arch: "x86_64", Machine: id.Machine, Type: "hvm"},
			FirmwareInfo: &libvirtxml.DomainOSFirmwareInfo{Features: []libvirtxml.DomainOSFirmwareFeature{
				{Enabled: "yes", Name: "enrolled-keys"},
				{Enabled: "yes", Name: "secure-boot"},
			}},
			BootDevices: []libvirtxml.DomainBootDevice{{Dev: "hd"}},
		},
		Features: &libvirtxml.DomainFeatureList{
			ACPI: &libvirtxml.DomainFeature{},
			APIC: &libvirtxml.DomainFeatureAPIC{},
			HyperV: &libvirtxml.DomainFeatureHyperV{
				Mode:      "custom",
				Relaxed:   &libvirtxml.DomainFeatureState{State: "on"},
				VAPIC:     &libvirtxml.DomainFeatureState{State: "on"},
				Spinlocks: &libvirtxml.DomainFeatureHyperVSpinlocks{DomainFeatureState: libvirtxml.DomainFeatureState{State: "on"}, Retries: 8191},
			},
			SMM: &libvirtxml.DomainFeatureSMM{State: "on"}, // required by Secure Boot firmware
		},
		CPU: cpu,
		Clock: &libvirtxml.DomainClock{Offset: "localtime", Timer: []libvirtxml.DomainTimer{
			{Name: "rtc", TickPolicy: "catchup"},
			{Name: "pit", TickPolicy: "delay"},
			{Name: "hpet", Present: "no"},
			{Name: "hypervclock", Present: "yes"},
		}},
		PM: &libvirtxml.DomainPM{
			SuspendToMem:  &libvirtxml.DomainPMPolicy{Enabled: "no"},
			SuspendToDisk: &libvirtxml.DomainPMPolicy{Enabled: "no"},
		},
		Devices: &libvirtxml.DomainDeviceList{
			Disks: []libvirtxml.DomainDisk{{
				Device: "disk",
				Driver: &libvirtxml.DomainDiskDriver{Name: "qemu", Type: "qcow2", Cache: "none", IO: "native", Discard: "unmap"},
				Source: &libvirtxml.DomainDiskSource{File: &libvirtxml.DomainDiskSourceFile{File: s.DiskPath}},
				Target: &libvirtxml.DomainDiskTarget{Dev: "sda", Bus: "scsi"},
				Serial: id.DiskSerial,
				WWN:    id.DiskWWN,
			}},
			Controllers: []libvirtxml.DomainController{{Type: "scsi", Index: uintp(0), Model: "virtio-scsi"}},
			Interfaces: []libvirtxml.DomainInterface{{
				MAC:    &libvirtxml.DomainInterfaceMAC{Address: id.MAC},
				Source: &libvirtxml.DomainInterfaceSource{Network: &libvirtxml.DomainInterfaceSourceNetwork{Network: s.Network}},
				Model:  &libvirtxml.DomainInterfaceModel{Type: "virtio"},
				// Pin the VM to its MAC and DHCP-assigned address. Learning must be
				// DHCP-only: by default it can latch onto the 169.254.x address
				// Windows uses for a moment at first boot.
				FilterRef: &libvirtxml.DomainInterfaceFilterRef{Filter: "clean-traffic",
					Parameters: []libvirtxml.DomainInterfaceFilterParam{{Name: "CTRL_IP_LEARNING", Value: "dhcp"}}},
			}},
			Serials: []libvirtxml.DomainSerial{{
				Source: &libvirtxml.DomainChardevSource{File: &libvirtxml.DomainChardevSourceFile{Path: s.SerialLog}},
				Target: &libvirtxml.DomainSerialTarget{Type: "isa-serial", Port: uintp(0)},
			}},
			Channels: []libvirtxml.DomainChannel{{
				Source: &libvirtxml.DomainChardevSource{UNIX: &libvirtxml.DomainChardevSourceUNIX{}},
				Target: &libvirtxml.DomainChannelTarget{VirtIO: &libvirtxml.DomainChannelTargetVirtIO{Name: "org.qemu.guest_agent.0"}},
			}},
			TPMs: []libvirtxml.DomainTPM{{
				Model:   "tpm-tis", // matches the Packer build VM
				Backend: &libvirtxml.DomainTPMBackend{Emulator: &libvirtxml.DomainTPMBackendEmulator{Version: "2.0"}},
			}},
			Graphics: []libvirtxml.DomainGraphic{{VNC: &libvirtxml.DomainGraphicVNC{Port: -1, AutoPort: "yes", Listen: "127.0.0.1"}}},
			// virtio GPU: Windows drives it with viogpudo (installed with the
			// virtio drivers), so the console resolution is adjustable. Without
			// it libvirt picks cirrus, stuck at 800x600 on the basic display driver.
			Videos: []libvirtxml.DomainVideo{{Model: libvirtxml.DomainVideoModel{Type: "virtio", Heads: 1, Primary: "yes"}}},
			RNGs: []libvirtxml.DomainRNG{{
				Model:   "virtio",
				Backend: &libvirtxml.DomainRNGBackend{Random: &libvirtxml.DomainRNGBackendRandom{Device: "/dev/urandom"}},
			}},
		},
	}, nil
}

// XML renders the domain definition.
func XML(d *libvirtxml.Domain) (string, error) {
	b, err := xml.MarshalIndent(d, "", "  ")
	return string(b), err
}
