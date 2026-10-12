package pdf

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"testing"
)

func TestDocumentStructure(t *testing.T) {
	var p Page
	p.Text(72, 72, HelveticaBold, 18, "Slot (wiresx) \\ test")
	p.Text(72, 100, Courier, 12, "café")
	p.Rect(72, 120, 10, 10)
	p.Line(72, 140, 540, 140, 0.5)
	doc := p.Bytes()

	for _, want := range []string{"%PDF-1.4", "(Slot \\(wiresx\\) \\\\ test) Tj", "(caf?) Tj", "/BaseFont /Courier-Bold", "%%EOF"} {
		if !bytes.Contains(doc, []byte(want)) {
			t.Errorf("document lacks %q", want)
		}
	}

	// Every xref offset must point at its "N 0 obj".
	m := regexp.MustCompile(`startxref\n(\d+)\n`).FindSubmatch(doc)
	if m == nil {
		t.Fatal("no startxref")
	}
	xref, _ := strconv.Atoi(string(m[1]))
	if !bytes.HasPrefix(doc[xref:], []byte("xref\n")) {
		t.Fatal("startxref does not point at the xref table")
	}
	entries := regexp.MustCompile(`(\d{10}) 00000 n `).FindAllSubmatch(doc[xref:], -1)
	if len(entries) != 8 {
		t.Fatalf("%d xref entries, want 8", len(entries))
	}
	for i, e := range entries {
		off, _ := strconv.Atoi(string(e[1]))
		if want := fmt.Sprintf("%d 0 obj", i+1); !bytes.HasPrefix(doc[off:], []byte(want)) {
			t.Errorf("xref entry %d points at %q", i+1, doc[off:off+10])
		}
	}

	// The content stream length must be exact.
	sm := regexp.MustCompile(`/Length (\d+) >>\nstream\n`).FindSubmatchIndex(doc)
	n, _ := strconv.Atoi(string(doc[sm[2]:sm[3]]))
	if !bytes.HasPrefix(doc[sm[1]+n:], []byte("endstream")) {
		t.Error("content stream /Length is wrong")
	}
}
