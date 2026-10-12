package domain

import (
	"fmt"

	"libvirt.org/go/libvirtxml"

	"winslot/internal/slot"
)

// Current is what an existing slot's definition says about its identity and
// sizing, read back so the definition can be re-rendered from the current
// template without changing either.
type Current struct {
	Identity    slot.Identity
	Description string
	MemoryMiB   uint
	VCPUs       uint
	DiskPath    string
	Network     string
}

// Parse extracts Current from a domain's XML. Every identity field must be
// present: a slot whose identity can't be read back must not be rewritten.
func Parse(x string) (Current, error) {
	var d libvirtxml.Domain
	if err := d.Unmarshal(x); err != nil {
		return Current{}, fmt.Errorf("domain XML: %w", err)
	}
	var c Current
	c.Identity.UUID = d.UUID
	c.Description = d.Description
	if d.OS != nil && d.OS.Type != nil {
		c.Identity.Machine = d.OS.Type.Machine
	}
	if d.Memory != nil {
		c.MemoryMiB = toMiB(d.Memory.Value, d.Memory.Unit)
	}
	if d.VCPU != nil {
		c.VCPUs = d.VCPU.Value
	}
	if d.Devices != nil {
		for _, disk := range d.Devices.Disks {
			if disk.Device == "disk" && disk.Source != nil && disk.Source.File != nil {
				c.DiskPath = disk.Source.File.File
				c.Identity.DiskSerial = disk.Serial
				c.Identity.DiskWWN = disk.WWN
				break
			}
		}
		for _, i := range d.Devices.Interfaces {
			if i.MAC != nil && i.Source != nil && i.Source.Network != nil {
				c.Identity.MAC = i.MAC.Address
				c.Network = i.Source.Network.Network
				break
			}
		}
	}
	id := c.Identity
	if id.UUID == "" || id.MAC == "" || id.DiskSerial == "" || id.DiskWWN == "" || id.Machine == "" ||
		c.MemoryMiB == 0 || c.VCPUs == 0 || c.DiskPath == "" || c.Network == "" {
		return Current{}, fmt.Errorf("domain %s: identity or sizing incomplete (%+v); refusing to rewrite it", d.Name, c)
	}
	return c, nil
}

func toMiB(v uint, unit string) uint {
	switch unit {
	case "KiB", "k", "K", "":
		return v / 1024
	case "MiB", "M", "m":
		return v
	case "GiB", "G", "g":
		return v * 1024
	}
	return 0
}
