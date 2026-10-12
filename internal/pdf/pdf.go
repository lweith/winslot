// Package pdf writes simple one-page PDF documents: text in the standard
// Helvetica and Courier fonts, filled rectangles and lines. The standard
// fonts need no embedding, so a document is a few kilobytes of plain PDF
// any viewer or printer handles.
package pdf

import (
	"bytes"
	"fmt"
	"strings"
)

// Letter is US Letter in points.
const (
	LetterWidth  = 612.0
	LetterHeight = 792.0
)

// Font is one of the standard fonts.
type Font int

const (
	Helvetica Font = iota
	HelveticaBold
	Courier
	CourierBold
)

var fontNames = []string{"Helvetica", "Helvetica-Bold", "Courier", "Courier-Bold"}

// Page is a US Letter page. Coordinates are points from the top-left corner.
type Page struct {
	content bytes.Buffer
}

// Text draws s with its baseline at (x, y). Characters outside printable
// ASCII are replaced by '?'.
func (p *Page) Text(x, y float64, f Font, size float64, s string) {
	fmt.Fprintf(&p.content, "BT /F%d %.2f Tf %.2f %.2f Td (%s) Tj ET\n", f, size, x, LetterHeight-y, escape(s))
}

// Rect fills a black rectangle whose top-left corner is (x, y).
func (p *Page) Rect(x, y, w, h float64) {
	fmt.Fprintf(&p.content, "%.3f %.3f %.3f %.3f re f\n", x, LetterHeight-y-h, w, h)
}

// Line draws a black line.
func (p *Page) Line(x1, y1, x2, y2, width float64) {
	fmt.Fprintf(&p.content, "%.2f w %.2f %.2f m %.2f %.2f l S\n", width, x1, LetterHeight-y1, x2, LetterHeight-y2)
}

// CourierWidth is the width of s in Courier (every glyph is 0.6 em).
// Helvetica is proportional; callers lay it out with generous margins.
func CourierWidth(size float64, s string) float64 {
	return float64(len(s)) * 0.6 * size
}

func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '(' || r == ')' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r >= 0x20 && r < 0x7f:
			b.WriteRune(r)
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

// Bytes renders the page as a complete PDF document.
func (p *Page) Bytes() []byte {
	var out bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	// 1 catalog, 2 pages, 3 page, 4 content, 5.. fonts
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	var fonts strings.Builder
	for i := range fontNames {
		fmt.Fprintf(&fonts, "/F%d %d 0 R ", i, 5+i)
	}
	obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << %s>> >> /Contents 4 0 R >>",
		LetterWidth, LetterHeight, fonts.String()))
	obj(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", p.content.Len(), p.content.String()))
	for _, name := range fontNames {
		obj(fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /%s /Encoding /WinAnsiEncoding >>", name))
	}

	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return out.Bytes()
}
