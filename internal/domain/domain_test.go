package domain

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winslot/internal/slot"
)

var update = flag.Bool("update", false, "rewrite golden files")

func spec() Spec {
	return Spec{
		Name:        "wiresx",
		Title:       "Windows 11 Pro (license slot)",
		Description: "Windows 11 Pro license slot.",
		Identity: slot.Identity{
			UUID: "84baff84-19e0-4bc7-bc42-7f5a4e81dc60", MAC: "52:54:00:09:a6:7b",
			DiskSerial: "WSFB990FA6376A67", DiskWWN: "5525400f628e4351", Machine: slot.Machine,
		},
		MemoryMiB:   8192,
		VCPUs:       4,
		DiskPath:    "/var/lib/winslot/disks/wiresx.qcow2",
		Network:     "default",
		SerialLog:   "/var/log/libvirt/qemu/wiresx.serial.log",
		VirtFeature: "vmx",
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run: go test ./internal/domain -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from golden file; run with -update after checking the change:\n%s", name, got)
	}
}

func TestBuildGolden(t *testing.T) {
	for _, tc := range []struct {
		file   string
		nested bool
	}{{"slot.xml", false}, {"slot-nested.xml", true}} {
		s := spec()
		s.Nested = tc.nested
		d, err := Build(s)
		if err != nil {
			t.Fatal(err)
		}
		x, err := XML(d)
		if err != nil {
			t.Fatal(err)
		}
		golden(t, tc.file, x+"\n")
	}
}

// The identity must reach the XML verbatim: these are what Windows sees.
func TestBuildCarriesIdentity(t *testing.T) {
	d, err := Build(spec())
	if err != nil {
		t.Fatal(err)
	}
	x, _ := XML(d)
	for _, want := range []string{
		"<uuid>84baff84-19e0-4bc7-bc42-7f5a4e81dc60</uuid>",
		`<mac address="52:54:00:09:a6:7b">`,
		"<serial>WSFB990FA6376A67</serial>",
		"<wwn>5525400f628e4351</wwn>",
		`machine="pc-q35-8.2"`,
		`<feature policy="disable" name="vmx">`,
		`<parameter name="CTRL_IP_LEARNING" value="dhcp">`,
		`sockets="1"`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("XML lacks %s", want)
		}
	}
}

func TestBuildRejects(t *testing.T) {
	for name, mutate := range map[string]func(*Spec){
		"bad name":     func(s *Spec) { s.Name = "Bad_Name" },
		"no disk":      func(s *Spec) { s.DiskPath = "" },
		"no vcpus":     func(s *Spec) { s.VCPUs = 0 },
		"virt feature": func(s *Spec) { s.VirtFeature = "" },
	} {
		s := spec()
		mutate(&s)
		if _, err := Build(s); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
