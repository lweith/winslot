package sheet

import (
	"bytes"
	"flag"
	"os"
	"testing"
	"time"

	"winslot/internal/license"
)

var out = flag.String("out", "", "also write the test sheet to this file (to look at it)")

func TestRender(t *testing.T) {
	r := license.Record{
		Slot: "wiresx", Edition: "Windows(R), Professional edition", Channel: "Retail",
		Key: "W269N-WFGWX-YVC9B-4J6C9-T83GX", KeySource: license.KeyDecoded, Partial: "T83GX",
		Activated: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), Host: "vmhost",
		Purchase: "Microsoft Store order 1234567890, TMRA treasurer, 2026-10-01",
		UUID:     "fa8b8620-313f-4499-b70c-92b1d6842d3a", MAC: "52:54:00:12:34:56",
		DiskSerial: "WS0123456789ABCD", DiskWWN: "5525400123456789", Machine: "pc-q35-8.2",
		VCPUs: 4, MemoryMiB: 8192, DiskBytes: 80 << 30,
	}
	doc, err := Render(r, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"(W269N-WFGWX-YVC9B-4J6C9-T83GX) Tj", "(fa8b8620-313f-4499-b70c-92b1d6842d3a) Tj", "winslot vm restore wiresx.toml"} {
		if !bytes.Contains(doc, []byte(want)) {
			t.Errorf("sheet lacks %q", want)
		}
	}
	if *out != "" {
		if err := os.WriteFile(*out, doc, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	r.Key, r.KeySource = "", license.KeyNone
	if doc, err = Render(r, time.Now(), "test"); err != nil || !bytes.Contains(doc, []byte(`(\(none recorded\)) Tj`)) {
		t.Errorf("keyless sheet: %v", err)
	}
}
