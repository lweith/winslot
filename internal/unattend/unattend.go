// Package unattend renders a VM's first-boot answer file: its hostname, time
// zone and a fresh local administrator password.
package unattend

import (
	"bytes"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"math/big"
	"text/template"

	"winslot/assets"
)

// Params fill the template.
type Params struct {
	ComputerName  string
	TimeZone      string // Windows time zone ID, e.g. "Eastern Standard Time"
	AdminPassword string
}

var tmpl = template.Must(template.New("clone-unattend.xml.tmpl").
	Funcs(template.FuncMap{"xml": func(s string) (string, error) {
		var b bytes.Buffer
		err := xml.EscapeText(&b, []byte(s))
		return b.String(), err
	}}).
	ParseFS(assets.FS, "answer/clone-unattend.xml.tmpl"))

// Render returns the answer file for p.
func Render(p Params) ([]byte, error) {
	if p.ComputerName == "" || p.TimeZone == "" || p.AdminPassword == "" {
		return nil, fmt.Errorf("unattend: incomplete parameters")
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, p); err != nil {
		return nil, fmt.Errorf("unattend: %w", err)
	}
	return b.Bytes(), nil
}

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// NewPassword returns a random password of n characters from an unambiguous
// alphanumeric alphabet, plus "-9a" so Windows' complexity rules (upper,
// lower, digit, symbol) are always met even if a policy turns them on.
func NewPassword(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = passwordAlphabet[v.Int64()]
	}
	return string(b) + "-9a", nil
}
