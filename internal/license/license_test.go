package license

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winslot/internal/winkey"
)

func testRecord(name string) Record {
	return Record{
		Slot: name, Edition: "Windows(R), Professional edition", Channel: "Retail",
		Key: "W269N-WFGWX-YVC9B-4J6C9-T83GX", KeySource: KeyDecoded, Partial: "T83GX",
		Activated: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), Host: "vmhost",
		UUID: "fa8b8620-313f-4499-b70c-92b1d6842d3a", MAC: "52:54:00:12:34:56",
		DiskSerial: "WS0123456789ABCD", DiskWWN: "5525400123456789", Machine: "pc-q35-8.2",
		VCPUs: 4, MemoryMiB: 8192, DiskBytes: 80 << 30,
	}
}

func TestValidate(t *testing.T) {
	if err := testRecord("wiresx").Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*Record){
		"name":             func(r *Record) { r.Slot = "Bad Name" },
		"identity":         func(r *Record) { r.UUID = "" },
		"sizing":           func(r *Record) { r.DiskBytes = 0 },
		"key format":       func(r *Record) { r.Key = "nope" },
		"key not windows'": func(r *Record) { r.Partial = "ABCDE" },
		"none with key":    func(r *Record) { r.KeySource = KeyNone },
		"source":           func(r *Record) { r.KeySource = "guess" },
	}
	for what, mod := range bad {
		r := testRecord("wiresx")
		mod(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
	r := testRecord("wiresx")
	r.KeySource, r.Partial = KeyUnchecked, winkey.Partial(winkey.GenericPro)
	if err := r.Validate(); err != nil {
		t.Errorf("unchecked key on a digital license: %v", err)
	}
	r.Key, r.KeySource = "", KeyNone
	if err := r.Validate(); err != nil {
		t.Errorf("digital license without key: %v", err)
	}
}

func TestRecordRoundTrip(t *testing.T) {
	r := testRecord("wiresx")
	b, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseRecord(b)
	if err != nil {
		t.Fatal(err)
	}
	if got != r {
		t.Fatalf("round trip:\n%+v\n%+v", got, r)
	}
	if _, err := ParseRecord(append(b, []byte("bogus = 1\n")...)); err == nil {
		t.Error("unknown key accepted")
	}
}

func TestRegister(t *testing.T) {
	path := filepath.Join(t.TempDir(), "licenses.toml")
	reg, err := Load(path)
	if err != nil || len(reg.Licenses) != 0 {
		t.Fatalf("missing register: %v %v", reg, err)
	}
	if err := reg.Add(testRecord("winlink")); err != nil {
		t.Fatal(err)
	}
	w := testRecord("wiresx")
	w.UUID = "0b8b8620-313f-4499-b70c-92b1d6842d3a"
	if err := reg.Add(w); err != nil {
		t.Fatal(err)
	}
	dup := testRecord("other") // same UUID as winlink
	if err := reg.Add(dup); err == nil {
		t.Error("duplicate identity accepted")
	}
	if err := reg.Save(path); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("register mode %v %v", fi.Mode(), err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Licenses) != 2 || again.Licenses[0].Slot != "winlink" {
		t.Fatalf("reloaded %+v", again.Licenses)
	}
	if r, ok := again.Find("wiresx"); !ok || r.UUID != w.UUID {
		t.Fatal("Find")
	}
	if !again.Remove("wiresx") || again.Remove("wiresx") || len(again.Licenses) != 1 {
		t.Fatal("Remove")
	}
}

func TestLoadRejectsDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "licenses.toml")
	reg := &Register{Licenses: []Record{testRecord("a1"), testRecord("a1")}}
	if err := reg.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate loaded: %v", err)
	}
}
