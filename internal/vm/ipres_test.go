package vm

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

func create(t *testing.T, e env, name string) {
	t.Helper()
	if _, err := e.m.Create(context.Background(), name, CreateOptions{DiskBytes: 80 << 30, MemoryMiB: 4096, VCPUs: 2}); err != nil {
		t.Fatalf("Create %s: %v\n%s", name, err, e.out)
	}
}

func reserved(t *testing.T, e env, name string) string {
	t.Helper()
	r, err := e.m.Reservations()
	if err != nil {
		t.Fatal(err)
	}
	return r[name]
}

func TestCreateReservesLowestFreeAddress(t *testing.T) {
	e := setup(t)
	create(t, e, "wiresx")
	create(t, e, "winlink")
	if got := reserved(t, e, "wiresx"); got != "10.0.3.2" {
		t.Errorf("wiresx = %s", got)
	}
	if got := reserved(t, e, "winlink"); got != "10.0.3.3" {
		t.Errorf("winlink = %s", got)
	}
}

func TestAddressSurvivesDecommissionAndRecreate(t *testing.T) {
	e := setup(t)
	create(t, e, "wiresx")
	ctx := context.Background()
	if err := e.virt.Shutdown("wiresx"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Decommission(ctx, "wiresx"); err != nil {
		t.Fatal(err)
	}
	if got := reserved(t, e, "wiresx"); got != "10.0.3.2" {
		t.Errorf("after decommission = %q", got)
	}
	if _, err := e.m.Recreate(ctx, "wiresx", 0); err != nil {
		t.Fatalf("Recreate: %v\n%s", err, e.out)
	}
	if got := reserved(t, e, "wiresx"); got != "10.0.3.2" {
		t.Errorf("after recreate = %q", got)
	}
	if n := len(e.virt.hosts); n != 1 {
		t.Errorf("%d reservations: %v", n, e.virt.hosts)
	}
}

func TestDeleteFreesAddressForReuse(t *testing.T) {
	e := setup(t)
	create(t, e, "scratch")
	if err := e.virt.Shutdown("scratch"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Delete(context.Background(), "scratch"); err != nil {
		t.Fatal(err)
	}
	if got := reserved(t, e, "scratch"); got != "" {
		t.Errorf("still reserved: %s", got)
	}
	create(t, e, "wiresx")
	if got := reserved(t, e, "wiresx"); got != "10.0.3.2" {
		t.Errorf("wiresx = %s", got)
	}
}

func TestReserveExplicitAddress(t *testing.T) {
	e := setup(t)
	create(t, e, "wiresx")
	create(t, e, "winlink")
	// Inside the dynamic range is allowed: an existing VM keeps its address.
	a, err := e.m.ReserveIP("wiresx", netip.MustParseAddr("10.0.3.161"))
	if err != nil || a.String() != "10.0.3.161" {
		t.Fatalf("ReserveIP = %v, %v", a, err)
	}
	if got := reserved(t, e, "wiresx"); got != "10.0.3.161" {
		t.Errorf("wiresx = %s", got)
	}
	if n := len(e.virt.hosts); n != 2 {
		t.Errorf("old reservation kept: %v", e.virt.hosts)
	}
	// No address asked for: it keeps the one it has.
	if a, err := e.m.ReserveIP("wiresx", netip.Addr{}); err != nil || a.String() != "10.0.3.161" {
		t.Errorf("idempotent ReserveIP = %v, %v", a, err)
	}
	for addr, want := range map[string]string{
		"10.0.3.161": "already reserved",
		"10.0.3.0":   "network address",
		"10.0.3.255": "broadcast",
		"10.0.3.1":   "this host",
		"10.0.0.5":   "outside",
	} {
		_, err := e.m.ReserveIP("winlink", netip.MustParseAddr(addr))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", addr, err, want)
		}
	}
}

func TestStaticPoolExhausted(t *testing.T) {
	v, err := parseVMNet(`<network><ip address='10.0.3.1' prefix='24'><dhcp><range start='10.0.3.3' end='10.0.3.9'/><host mac='52:54:00:00:00:01' name='a' ip='10.0.3.2'/></dhcp></ip></network>`)
	if err != nil {
		t.Fatal(err)
	}
	if a, err := v.next(); err == nil {
		t.Errorf("next = %v, want an error", a)
	}
}

func TestParseVMNetNetmaskAndNoDHCP(t *testing.T) {
	v, err := parseVMNet(`<network><ip address='10.0.3.1' netmask='255.255.255.0'><dhcp><range start='10.0.3.100' end='10.0.3.254'/></dhcp></ip></network>`)
	if err != nil || v.subnet.String() != "10.0.3.0/24" {
		t.Errorf("subnet = %v, %v", v.subnet, err)
	}
	if _, err := parseVMNet(`<network><ip address='10.0.3.1' prefix='24'/></network>`); err == nil {
		t.Error("accepted a network without DHCP")
	}
}
