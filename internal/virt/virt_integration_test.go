//go:build libvirt

// Integration tests against a real libvirtd (run as root on a winslot host):
//
//	go test -tags libvirt ./internal/virt
//
// They create and remove a throwaway network and domain named winslot-itest.
package virt

import (
	"strings"
	"testing"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"

	"winslot/internal/domain"
	"winslot/internal/slot"
)

const (
	itNet = "winslot-itest"
	itDom = "winslot-itest"
	itSRV = `<srv service='windows-exporter' protocol='tcp' domain='itest.example' target='vm1.itest.example' port='9182' priority='10' weight='10'/>`
)

func connect(t *testing.T) *Conn {
	t.Helper()
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestIntegrationDNSSRV(t *testing.T) {
	c := connect(t)
	n, err := c.l.NetworkDefineXML(`<network><name>` + itNet + `</name><bridge name='virbr-itest'/>
	  <domain name='itest.example' localOnly='yes'/><ip address='192.0.2.1' prefix='24'/></network>`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.l.NetworkDestroy(n); c.l.NetworkUndefine(n) })
	if err := c.l.NetworkCreate(n); err != nil {
		t.Fatal(err)
	}
	netXML := func() string {
		x, err := c.l.NetworkGetXMLDesc(n, 0)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}

	if err := c.AddDNSSRV(itNet, itSRV); err != nil {
		t.Fatalf("AddDNSSRV: %v", err)
	}
	if x := netXML(); !strings.Contains(x, "target='vm1.itest.example'") {
		t.Fatalf("SRV record not in network XML:\n%s", x)
	}
	if err := c.DeleteDNSSRV(itNet, itSRV); err != nil {
		t.Fatalf("DeleteDNSSRV: %v", err)
	}
	if x := netXML(); strings.Contains(x, "vm1.itest.example") {
		t.Fatalf("SRV record still present:\n%s", x)
	}
}

func TestIntegrationDomainLifecycle(t *testing.T) {
	c := connect(t)
	id, _ := slot.NewIdentity()
	d, err := domain.Build(domain.Spec{
		Name: itDom, Title: "itest", Description: "winslot integration test", Identity: id,
		MemoryMiB: 256, VCPUs: 1, DiskPath: "/nonexistent/itest.qcow2", Network: "default",
		SerialLog: "/tmp/winslot-itest.serial.log", VirtFeature: "vmx",
	})
	if err != nil {
		t.Fatal(err)
	}
	x, _ := domain.XML(d)
	if err := c.Define(x); err != nil {
		t.Fatalf("Define: %v", err)
	}
	t.Cleanup(func() {
		if dom, err := c.l.DomainLookupByName(itDom); err == nil {
			c.l.DomainUndefineFlags(dom, libvirt.DomainUndefineNvram|libvirt.DomainUndefineTpm)
		}
	})

	if st, err := c.State(itDom); err != nil || st != ShutOff {
		t.Errorf("State = %v, %v", st, err)
	}
	if st, err := c.State("winslot-itest-missing"); err != nil || st != NotFound {
		t.Errorf("State(missing) = %v, %v", st, err)
	}
	if names, _ := c.ListNames(); !contains(names, itDom) {
		t.Errorf("ListNames lacks %s: %v", itDom, names)
	}

	if _, ok, err := c.SlotMetadata(itDom); err != nil || ok {
		t.Fatalf("fresh domain: SlotMetadata ok=%v err=%v", ok, err)
	}
	m := slot.Metadata{Edition: "Windows 11 Pro", DiskBytes: 80 << 30, Created: time.Now().UTC().Truncate(time.Second), Golden: "win11-test"}
	mx, _ := m.Marshal()
	if err := c.SetSlotMetadata(itDom, mx); err != nil {
		t.Fatalf("SetSlotMetadata: %v", err)
	}
	got, ok, err := c.SlotMetadata(itDom)
	if err != nil || !ok {
		t.Fatalf("SlotMetadata: ok=%v err=%v", ok, err)
	}
	back, err := slot.ParseMetadata(got)
	if err != nil || back.Golden != "win11-test" || back.DiskBytes != m.DiskBytes {
		t.Errorf("metadata round trip: %q -> %+v, %v", got, back, err)
	}

	if err := c.SetDescription(itDom, "decommissioned"); err != nil {
		t.Fatal(err)
	}
	if x, _ := c.XML(itDom); !strings.Contains(x, "<description>decommissioned</description>") {
		t.Error("description not set")
	}
	if err := c.DeleteSnapshotMetadata(itDom); err != nil {
		t.Errorf("DeleteSnapshotMetadata with no snapshots: %v", err)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
