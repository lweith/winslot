package vm

import (
	"context"
	"encoding/base64"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winslot/internal/license"
	"winslot/internal/slot"
	"winslot/internal/virt"
	"winslot/internal/winkey"
)

const retailKey = "W269N-WFGWX-YVC9B-4J6C9-T83GX"

// activation is what queryActivation prints in a VM with key installed.
func activation(t *testing.T, status int, installed string) string {
	t.Helper()
	dpid, err := winkey.Encode(installed)
	if err != nil {
		t.Fatal(err)
	}
	return "STATUS " + string(rune('0'+status)) + "\r\nPARTIAL " + winkey.Partial(installed) + "\r\nCHANNEL Retail\r\n" +
		"EDITION Windows(R), Professional edition\r\nDPID " + base64.StdEncoding.EncodeToString(dpid) + "\r\nACTIVATION-END\r\n"
}

func createSlot(t *testing.T, e env, name string) {
	t.Helper()
	if _, err := e.m.Create(context.Background(), name, CreateOptions{DiskBytes: 80 << 30, MemoryMiB: 4096, VCPUs: 2}); err != nil {
		t.Fatalf("Create: %v\n%s", err, e.out)
	}
}

func scan(t *testing.T, e env, names ...string) ScanResult {
	t.Helper()
	res, err := e.m.LicenseScan(context.Background(), names, "testhost")
	if err != nil || len(res) != 1 {
		t.Fatalf("LicenseScan = %+v, %v", res, err)
	}
	return res[0]
}

func register(t *testing.T, e env) *license.Register {
	t.Helper()
	reg, err := license.Load(e.m.RegisterPath())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestParseActivation(t *testing.T) {
	a, err := parseActivation(activation(t, 1, retailKey))
	if err != nil || !a.Licensed() || a.Partial != "T83GX" || a.Channel != "Retail" || len(a.DPID) == 0 {
		t.Fatalf("parseActivation = %+v, %v", a, err)
	}
	if _, err := parseActivation("STATUS 1\r\nPARTIAL T83GX\r\n"); err == nil {
		t.Error("truncated answer accepted")
	}
	if _, err := parseActivation("STATUS x\r\nPARTIAL T83GX\r\nACTIVATION-END\r\n"); err == nil {
		t.Error("bad status accepted")
	}
}

func TestUnlicensedSlotIsDisposable(t *testing.T) {
	e := setup(t)
	createSlot(t, e, "scratch")
	e.cert.activation = activation(t, 5, winkey.GenericPro)
	if r := scan(t, e); r.State != "unlicensed" {
		t.Fatalf("scan = %+v", r)
	}
	if len(register(t, e).Licenses) != 0 {
		t.Fatal("unactivated slot recorded")
	}
	if err := e.m.Delete(context.Background(), "scratch"); err == nil {
		t.Error("deleted a running slot")
	}
	e.virt.Shutdown("scratch")
	if err := e.m.Delete(context.Background(), "scratch"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.virt.doms["scratch"]; ok {
		t.Error("domain still defined")
	}
	if _, err := os.Stat(e.m.DiskPath("scratch")); !os.IsNotExist(err) {
		t.Error("disk left behind")
	}
}

func TestScanRecordsActivatedSlot(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	createSlot(t, e, "wiresx")
	e.cert.activation = activation(t, 1, retailKey)

	r := scan(t, e)
	if r.State != "recorded" || !strings.Contains(r.Detail, "key recovered") {
		t.Fatalf("scan = %+v", r)
	}
	rec, ok := register(t, e).Find("wiresx")
	if !ok || rec.Key != retailKey || rec.KeySource != license.KeyDecoded || rec.Host != "testhost" {
		t.Fatalf("record = %+v", rec)
	}
	if uuid := between(e.virt.doms["wiresx"].xml, "<uuid>", "</uuid>"); rec.UUID != uuid || rec.VCPUs != 2 || rec.MemoryMiB != 4096 || rec.DiskBytes != 80<<30 {
		t.Errorf("record identity/sizing = %+v (domain uuid %s)", rec, uuid)
	}
	if fi, err := os.Stat(filepath.Join(e.m.SheetDir(), "wiresx.pdf")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("sheet: %v %v", fi, err)
	}
	meta, _ := slot.ParseMetadata(e.virt.doms["wiresx"].meta)
	if meta.Licensed == nil {
		t.Error("slot metadata not marked licensed")
	}

	if r := scan(t, e); r.State != "known" {
		t.Errorf("second scan = %+v", r)
	}
	// Precious: no delete, even after decommissioning, and even if the
	// register file is lost (the metadata marker remains).
	e.virt.Shutdown("wiresx")
	if err := e.m.Delete(ctx, "wiresx"); err == nil || !strings.Contains(err.Error(), "licensed") {
		t.Errorf("Delete of a licensed slot: %v", err)
	}
	os.Remove(e.m.RegisterPath())
	if err := e.m.Delete(ctx, "wiresx"); err == nil {
		t.Error("deleted a licensed slot after the register was lost")
	}
	list, _ := e.m.List()
	if len(list) != 1 || !strings.Contains(list[0].License, "not in register") {
		t.Errorf("List = %+v", list)
	}
}

func TestScanWarnings(t *testing.T) {
	e := setup(t)
	createSlot(t, e, "wiresx")
	e.cert.activation = activation(t, 1, retailKey)
	scan(t, e)

	e.cert.activation = activation(t, 5, retailKey)
	if r := scan(t, e); r.State != "warning" {
		t.Errorf("registered but not activated: %+v", r)
	}
	e.cert.activation = activation(t, 1, "YTMG3-N6DKC-DKB77-7M9GH-8HVX7")
	if r := scan(t, e); r.State != "warning" || !strings.Contains(r.Detail, "8HVX7") {
		t.Errorf("different key: %+v", r)
	}
	// After a recreate the VM runs on its digital license with the generic key: fine.
	e.cert.activation = activation(t, 1, winkey.GenericPro)
	if r := scan(t, e); r.State != "known" {
		t.Errorf("digital license after recreate: %+v", r)
	}
	if rec, _ := register(t, e).Find("wiresx"); rec.Key != retailKey {
		t.Error("scan changed a recorded key")
	}
}

func TestDigitalLicenseAndRecordKey(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	createSlot(t, e, "winlink")
	e.cert.activation = activation(t, 1, winkey.GenericPro)
	if r := scan(t, e); r.State != "recorded" || !strings.Contains(r.Detail, "digital license") {
		t.Fatalf("scan = %+v", r)
	}
	if rec, _ := register(t, e).Find("winlink"); rec.Key != "" || rec.KeySource != license.KeyNone {
		t.Fatalf("record = %+v", rec)
	}
	if err := e.m.RecordKey(ctx, "winlink", "not-a-key", ""); err == nil {
		t.Error("malformed key accepted")
	}
	if err := e.m.RecordKey(ctx, "winlink", strings.ToLower(retailKey), "Store order 42"); err != nil {
		t.Fatal(err)
	}
	rec, _ := register(t, e).Find("winlink")
	if rec.Key != retailKey || rec.KeySource != license.KeyUnchecked || rec.Purchase != "Store order 42" {
		t.Errorf("record = %+v", rec)
	}

	// With the real key installed, a key that isn't it is refused.
	e.cert.activation = activation(t, 1, retailKey)
	if err := e.m.RecordKey(ctx, "winlink", "YTMG3-N6DKC-DKB77-7M9GH-8HVX7", ""); err == nil {
		t.Error("key not installed in the VM accepted")
	}
	if err := e.m.RecordKey(ctx, "winlink", retailKey, ""); err != nil {
		t.Fatal(err)
	}
	if rec, _ := register(t, e).Find("winlink"); rec.KeySource != license.KeyEntered {
		t.Errorf("confirmed key source = %s", rec.KeySource)
	}
	if err := e.m.RecordKey(ctx, "nobody", retailKey, ""); err == nil {
		t.Error("recorded a key for a slot not in the register")
	}
}

func TestUnrecoverableKeyIsStillRecorded(t *testing.T) {
	e := setup(t)
	createSlot(t, e, "wiresx")
	// Windows reports one key; the registry holds another (or slmgr /cpky ran).
	e.cert.activation = strings.Replace(activation(t, 1, "YTMG3-N6DKC-DKB77-7M9GH-8HVX7"), "PARTIAL 8HVX7", "PARTIAL T83GX", 1)
	r := scan(t, e)
	if r.State != "recorded" || !strings.Contains(r.Detail, "could not be recovered") {
		t.Fatalf("scan = %+v", r)
	}
	if rec, _ := register(t, e).Find("wiresx"); rec.Key != "" || rec.Partial != "T83GX" {
		t.Errorf("record = %+v", rec)
	}
}

func TestReleaseMakesSlotDisposable(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	createSlot(t, e, "wiresx")
	e.cert.activation = activation(t, 1, retailKey)
	scan(t, e)

	if err := e.m.Release("wiresx"); err == nil {
		t.Error("released a slot that still has its disk")
	}
	e.virt.Shutdown("wiresx")
	if err := e.m.Decommission(ctx, "wiresx"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Release("wiresx"); err != nil {
		t.Fatal(err)
	}
	if _, ok := register(t, e).Find("wiresx"); ok {
		t.Error("still registered")
	}
	if _, err := os.Stat(filepath.Join(e.m.SheetDir(), "wiresx.pdf")); !os.IsNotExist(err) {
		t.Error("sheet left behind")
	}
	if err := e.m.Delete(ctx, "wiresx"); err != nil {
		t.Errorf("Delete after release: %v", err)
	}
}

// A recorded address the new host's network can't give doesn't stop a restore.
func TestRestoreAddressOutsideSubnet(t *testing.T) {
	old := setup(t)
	createSlot(t, old, "wiresx")
	old.cert.activation = activation(t, 1, retailKey)
	scan(t, old)
	rec, _ := register(t, old).Find("wiresx")
	r := *rec
	r.Address = "192.0.2.5"

	e := setup(t)
	if err := e.m.Restore(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	addrs, err := e.m.Reservations()
	if err != nil || addrs["wiresx"] == "" || addrs["wiresx"] == "192.0.2.5" {
		t.Errorf("restored address = %v, %v", addrs, err)
	}
}

func TestRestoreFromRecord(t *testing.T) {
	old := setup(t)
	createSlot(t, old, "wiresx")
	old.cert.activation = activation(t, 1, retailKey)
	scan(t, old)
	// Moving the reservation updates the record, and the record carries it.
	if _, err := old.m.ReserveIP("wiresx", netip.MustParseAddr("10.0.3.161")); err != nil {
		t.Fatal(err)
	}
	rec, _ := register(t, old).Find("wiresx")
	if rec.Address != "10.0.3.161" {
		t.Errorf("recorded address = %q", rec.Address)
	}
	text, err := rec.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	// A new host: nothing but the sheet's QR text.
	e := setup(t)
	parsed, err := license.ParseRecord(text)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.m.Restore(context.Background(), parsed); err != nil {
		t.Fatal(err)
	}
	d := e.virt.doms["wiresx"]
	if d == nil || between(d.xml, "<uuid>", "</uuid>") != rec.UUID || !strings.Contains(d.xml, rec.MAC) || !strings.Contains(d.xml, rec.DiskSerial) {
		t.Fatalf("restored domain lacks the recorded identity:\n%v", d)
	}
	if got, ok := register(t, e).Find("wiresx"); !ok || got.Key != retailKey {
		t.Error("record not added to the new host's register")
	}
	if addrs, err := e.m.Reservations(); err != nil || addrs["wiresx"] != "10.0.3.161" {
		t.Errorf("restored address = %v, %v", addrs, err)
	}
	list, _ := e.m.List()
	if len(list) != 1 || list[0].State != "decommissioned" {
		t.Errorf("List = %+v", list)
	}
	if _, err := e.m.Recreate(context.Background(), "wiresx", 0); err != nil {
		t.Fatalf("Recreate after restore: %v", err)
	}
	if between(e.virt.doms["wiresx"].xml, "<uuid>", "</uuid>") != rec.UUID {
		t.Error("recreate after restore changed the identity")
	}
	if e.virt.doms["wiresx"].state != virt.Running {
		t.Error("not running after recreate")
	}
	if err := e.m.Restore(context.Background(), parsed); err == nil {
		t.Error("restored over an existing slot")
	}
}
