// Package sheet renders a licensed slot's one-page record for paper: the
// license, the identity Windows' activation is bound to, how to bring the
// slot back, and a QR code holding the license record that
// "winslot vm restore" reads.
package sheet

import (
	"fmt"
	"strings"
	"time"

	"rsc.io/qr"

	"winslot/internal/license"
	"winslot/internal/pdf"
)

// Render returns the sheet as a PDF.
func Render(r license.Record, printed time.Time, version string) ([]byte, error) {
	data, err := r.Marshal()
	if err != nil {
		return nil, err
	}
	code, err := qr.Encode(string(data), qr.M)
	if err != nil {
		return nil, fmt.Errorf("QR code: %w", err)
	}

	var p pdf.Page
	const left, right = 54.0, pdf.LetterWidth - 54
	y := 66.0

	p.Text(left, y, pdf.HelveticaBold, 20, "Windows license slot: "+r.Slot)
	y += 18
	p.Text(left, y, pdf.Helvetica, 10, fmt.Sprintf("Host %s. Printed %s. Keep this sheet like a key: it is one.", r.Host, printed.Format("2006-01-02")))
	y += 10
	p.Line(left, y, right, y, 1)

	// License.
	y += 26
	p.Text(left, y, pdf.HelveticaBold, 13, "License")
	y += 22
	key, keyNote := r.Key, keySourceText(r.KeySource)
	if key == "" {
		key = "(none recorded)"
	}
	p.Text(left, y, pdf.Helvetica, 10, "Product key")
	p.Text(left+110, y+1, pdf.CourierBold, 17, key)
	y += 14
	p.Text(left+110, y, pdf.Helvetica, 8.5, keyNote)
	rows := [][2]string{
		{"Edition", r.Edition},
		{"Channel", r.Channel},
		{"Activated", r.Activated.Format("2006-01-02") + " (first seen activated by winslot)"},
		{"Purchase", orDash(r.Purchase)},
	}
	y = table(&p, left, y+18, rows)

	// Identity.
	y += 16
	p.Text(left, y, pdf.HelveticaBold, 13, "Slot identity (what activation is bound to)")
	rows = [][2]string{
		{"SMBIOS UUID", r.UUID},
		{"NIC MAC", r.MAC},
		{"Disk serial", r.DiskSerial},
		{"Disk WWN", r.DiskWWN},
		{"Machine type", r.Machine},
		{"Size", fmt.Sprintf("%d vCPUs (1 socket), %d MiB memory, %d GiB disk%s", r.VCPUs, r.MemoryMiB, r.DiskBytes>>30, nestedText(r.Nested))},
	}
	y = table(&p, left, y+22, rows)

	y += 16
	p.Text(left, y, pdf.HelveticaBold, 13, "Bringing it back")
	y += 18
	for _, line := range recovery(r.Slot) {
		if line == "" {
			y += 6
			continue
		}
		p.Text(left, y, pdf.Helvetica, 9.5, line)
		y += 13
	}

	// QR code below: a 2.5-inch square, large modules scan easily with a phone.
	const quiet = 4
	const side = 180.0
	module := side / float64(code.Size+2*quiet)
	qx, qy := left-quiet*module, y
	for yy := 0; yy < code.Size; yy++ {
		for xx := 0; xx < code.Size; xx++ {
			if code.Black(xx, yy) {
				p.Rect(qx+float64(quiet+xx)*module, qy+float64(quiet+yy)*module, module, module)
			}
		}
	}
	p.Text(qx+side+8, qy+quiet*module+10, pdf.HelveticaBold, 10, "License record")
	p.Text(qx+side+8, qy+quiet*module+24, pdf.Helvetica, 9, "This code holds the record above as text")
	p.Text(qx+side+8, qy+quiet*module+36, pdf.Helvetica, 9, "(TOML), for winslot vm restore.")

	p.Text(left, pdf.LetterHeight-30, pdf.Helvetica, 7.5, "winslot "+version+" license sheet")
	return p.Bytes(), nil
}

func table(p *pdf.Page, x, y float64, rows [][2]string) float64 {
	for _, r := range rows {
		p.Text(x, y, pdf.Helvetica, 10, r[0])
		p.Text(x+110, y, pdf.Courier, 10, r[1])
		y += 15
	}
	return y
}

func recovery(name string) []string {
	return []string{
		"Same host, disk lost or replaced:   winslot vm recreate " + name,
		"Windows sees the same hardware and reactivates by itself (digital license).",
		"",
		"New host (winslot installed, golden image built):",
		"  1. Scan the QR code into a text file, e.g. " + name + ".toml",
		"  2. winslot vm restore " + name + ".toml",
		"  3. winslot vm recreate " + name,
		"",
		"Still not activated?  Settings > System > Activation > Change product key,",
		"enter the key above. Online activation refused: run slui 4 (phone).",
		"",
		"Moving the license to other hardware (retail transfer):",
		"  winslot vm decommission " + name + ", then winslot license release " + name + ",",
		"  then enter the key on the new machine.",
	}
}

func keySourceText(s string) string {
	switch s {
	case license.KeyDecoded:
		return "read from the VM and confirmed by Windows"
	case license.KeyEntered:
		return "entered by an admin and confirmed by Windows"
	case license.KeyUnchecked:
		return "entered by an admin; not confirmable (VM runs on its digital license)"
	}
	return "activated by digital license; record the key with: winslot license record --key"
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func nestedText(n bool) string {
	if n {
		return ", nested virtualization"
	}
	return ""
}
