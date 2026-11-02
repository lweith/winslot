package slot

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestValidateName(t *testing.T) {
	for name, ok := range map[string]bool{
		"wiresx": true, "web01": true, "a": true, "abcdefghijklmno": true,
		"abcdefghijklmnop": false, // 16 characters
		"Web01":            false, "-web": false, "web-": false, "web_01": false, "": false, "web.01": false,
	} {
		if err := ValidateName(name); (err == nil) != ok {
			t.Errorf("ValidateName(%q) = %v, want ok=%v", name, err, ok)
		}
	}
}

func TestNewIdentityFormats(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for field, re := range map[string]struct {
		val string
		re  string
	}{
		"UUID":       {id.UUID, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`},
		"MAC":        {id.MAC, `^52:54:00(:[0-9a-f]{2}){3}$`},
		"DiskSerial": {id.DiskSerial, `^WS[0-9A-F]{14}$`},
		"DiskWWN":    {id.DiskWWN, `^5525400[0-9a-f]{9}$`},
	} {
		if !regexp.MustCompile(re.re).MatchString(re.val) {
			t.Errorf("%s = %q does not match %s", field, re.val, re.re)
		}
	}
	if id.Machine != Machine {
		t.Errorf("Machine = %q", id.Machine)
	}
}

func TestNewIdentityDeterministic(t *testing.T) {
	id, err := newIdentity(bytes.NewReader(bytes.Repeat([]byte{0xff}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	want := Identity{
		UUID: "ffffffff-ffff-4fff-bfff-ffffffffffff", MAC: "52:54:00:ff:ff:ff",
		DiskSerial: "WSFFFFFFFFFFFFFF", DiskWWN: "5525400fffffffff", Machine: Machine, CPU: CPU,
	}
	if id != want {
		t.Errorf("got %+v\nwant %+v", id, want)
	}
	if _, err := newIdentity(bytes.NewReader(nil)); err == nil {
		t.Error("short random source: want error")
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	created := time.Date(2026, 10, 11, 16, 57, 19, 0, time.UTC)
	m := Metadata{Edition: "Windows 11 Pro", DiskBytes: 80 << 30, Created: created, Golden: "win11-20261011-0353"}
	s, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `edition="Windows 11 Pro"`) || strings.Contains(s, "recreated") || strings.Contains(s, "nested") {
		t.Errorf("marshalled: %s", s)
	}
	back, err := ParseMetadata(s)
	if err != nil {
		t.Fatal(err)
	}
	if back.Edition != m.Edition || back.DiskBytes != m.DiskBytes || !back.Created.Equal(created) || back.Golden != m.Golden || back.Recreated != nil {
		t.Errorf("round trip: %+v", back)
	}
}

func TestParseMetadataRejects(t *testing.T) {
	for _, s := range []string{"", "<slot/>", `<slot xmlns="https://w8hhf.org/xmlns/winslot" edition="x"/>`, "not xml"} {
		if _, err := ParseMetadata(s); err == nil {
			t.Errorf("ParseMetadata(%q): want error", s)
		}
	}
}

// libvirt's virDomainGetMetadata returns the element with the namespace stripped.
func TestParseMetadataLibvirtForm(t *testing.T) {
	m, err := ParseMetadata(`<slot edition="Windows 11 Pro" disk_bytes="85899345920" created="2026-10-11T16:57:19Z" golden="win11-20261011-0353" nested="true"/>`)
	if err != nil {
		t.Fatal(err)
	}
	if m.DiskBytes != 80<<30 || !m.Nested || m.Golden != "win11-20261011-0353" {
		t.Errorf("%+v", m)
	}
}
