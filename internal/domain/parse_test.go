package domain

import (
	"strings"
	"testing"
)

// Build then Parse must give back exactly what went in: this is what keeps a
// slot's identity intact when its definition is re-rendered.
func TestParseRoundTrip(t *testing.T) {
	s := spec()
	d, err := Build(s)
	if err != nil {
		t.Fatal(err)
	}
	x, _ := XML(d)
	c, err := Parse(x)
	if err != nil {
		t.Fatal(err)
	}
	if c.Identity != s.Identity || c.MemoryMiB != s.MemoryMiB || c.VCPUs != s.VCPUs || c.DiskPath != s.DiskPath || c.Network != s.Network {
		t.Errorf("round trip:\n got %+v\nfrom %+v", c, s)
	}
}

// A definition as libvirt returns it (KiB, single quotes, extra devices),
// here test05's, which virt-install created.
func TestParseLibvirtOutput(t *testing.T) {
	c, err := Parse(`<domain type='kvm'><name>test05</name><uuid>84baff84-19e0-4bc7-bc42-7f5a4e81dc60</uuid>
<memory unit='KiB'>8388608</memory><vcpu placement='static'>4</vcpu>
<os firmware='efi'><type arch='x86_64' machine='pc-q35-8.2'>hvm</type></os>
<devices>
 <disk type='file' device='cdrom'><source file='/x.iso'/><target dev='sdb' bus='sata'/></disk>
 <disk type='file' device='disk'><driver name='qemu' type='qcow2'/><source file='/var/lib/winslot/disks/test05.qcow2'/>
  <target dev='sda' bus='scsi'/><serial>WSFB990FA6376A67</serial><wwn>5525400f628e4351</wwn></disk>
 <interface type='network'><mac address='52:54:00:09:a6:7b'/><source network='default'/><model type='virtio'/></interface>
</devices></domain>`)
	if err != nil {
		t.Fatal(err)
	}
	if c.MemoryMiB != 8192 || c.Identity.DiskSerial != "WSFB990FA6376A67" || c.Identity.MAC != "52:54:00:09:a6:7b" || c.DiskPath != "/var/lib/winslot/disks/test05.qcow2" {
		t.Errorf("%+v", c)
	}
}

func TestParseRefusesIncomplete(t *testing.T) {
	d, _ := Build(spec())
	x, _ := XML(d)
	noSerial := strings.Replace(x, "<serial>WSFB990FA6376A67</serial>", "", 1)
	if _, err := Parse(noSerial); err == nil {
		t.Error("definition without a disk serial accepted")
	}
	if _, err := Parse("<not-xml"); err == nil {
		t.Error("garbage accepted")
	}
}
