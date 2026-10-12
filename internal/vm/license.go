package vm

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"winslot/internal/domain"
	"winslot/internal/license"
	"winslot/internal/sheet"
	"winslot/internal/sshca"
	"winslot/internal/virt"
	"winslot/internal/winkey"
)

// queryActivation runs in the VM: Windows' own licensing state, and the
// encoded installed key. Windows' application ID selects Windows itself
// among the licensing service's products.
const queryActivation = `$p = Get-CimInstance SoftwareLicensingProduct -Filter "ApplicationID='55c92734-d682-4d71-983e-d6ec3f16059f' AND PartialProductKey IS NOT NULL" | Select-Object -First 1
$d = (Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion').DigitalProductId
"STATUS " + $p.LicenseStatus
"PARTIAL " + $p.PartialProductKey
"CHANNEL " + $p.ProductKeyChannel
"EDITION " + $p.Name
if ($d) { "DPID " + [Convert]::ToBase64String($d) }
"ACTIVATION-END"`

// Activation is what Windows in a VM says about its license.
type Activation struct {
	Status  int // SoftwareLicensingProduct.LicenseStatus: 1 = licensed
	Partial string
	Channel string
	Edition string
	DPID    []byte
}

// Licensed reports whether Windows is activated.
func (a Activation) Licensed() bool { return a.Status == 1 }

// StatusText names LicenseStatus values.
func (a Activation) StatusText() string {
	switch a.Status {
	case 0:
		return "unlicensed"
	case 1:
		return "activated"
	case 2:
		return "out-of-box grace"
	case 3:
		return "out-of-tolerance grace"
	case 4:
		return "non-genuine grace"
	case 5:
		return "not activated"
	case 6:
		return "extended grace"
	}
	return "status " + strconv.Itoa(a.Status)
}

func parseActivation(out string) (Activation, error) {
	var a Activation
	end := false
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		var err error
		switch k {
		case "STATUS":
			a.Status, err = strconv.Atoi(v)
		case "PARTIAL":
			a.Partial = v
		case "CHANNEL":
			a.Channel = v
		case "EDITION":
			a.Edition = v
		case "DPID":
			a.DPID, err = base64.StdEncoding.DecodeString(v)
		case "ACTIVATION-END":
			end = true
		}
		if err != nil {
			return Activation{}, fmt.Errorf("activation query: %s: %w", k, err)
		}
	}
	if !end || a.Partial == "" {
		return Activation{}, errors.New("activation query: incomplete answer from Windows")
	}
	return a, nil
}

// QueryActivation asks a running VM for its licensing state, over SSH
// verified with the host CA.
func (m *Manager) QueryActivation(ctx context.Context, name string) (Activation, error) {
	etc := m.Cfg.Storage.Etc
	ca, err := sshca.LoadSigner(filepath.Join(etc, "host_ca"))
	if err != nil {
		return Activation{}, err
	}
	mgmt, err := sshca.LoadSigner(filepath.Join(etc, "mgmt_key"))
	if err != nil {
		return Activation{}, err
	}
	out, err := m.Certifier.Run(m.FQDN(name), mgmt, sshca.CAHostKey(ca.PublicKey()), sshca.CertAlgos, queryActivation, "")
	if err != nil {
		return Activation{}, fmt.Errorf("%s: %w", name, err)
	}
	a, err := parseActivation(out)
	if err != nil {
		return Activation{}, fmt.Errorf("%s: %w", name, err)
	}
	return a, nil
}

// markLicensed sets (or, with nil, clears) the precious marker in the slot's
// libvirt metadata, a second record that survives losing the register file.
func (m *Manager) markLicensed(name string, at *time.Time) error {
	meta, err := m.Slot(name)
	if err != nil {
		return err
	}
	meta.Licensed = at
	return m.setMetadata(name, meta)
}

// Precious reports whether slot name's identity must be kept: it is in the
// license register or its metadata says it was activated.
func (m *Manager) Precious(name string) (bool, error) {
	if ok, err := m.Licensed(name); err != nil || ok {
		return ok, err
	}
	meta, err := m.Slot(name)
	if err != nil {
		return false, err
	}
	return meta.Licensed != nil, nil
}

// WriteSheet renders slot name's license sheet into dir (default SheetDir)
// and returns its path.
func (m *Manager) WriteSheet(name, dir string) (string, error) {
	reg, err := license.Load(m.RegisterPath())
	if err != nil {
		return "", err
	}
	rec, ok := reg.Find(name)
	if !ok {
		return "", fmt.Errorf("%s is not in the license register", name)
	}
	doc, err := sheet.Render(*rec, m.now(), m.Version)
	if err != nil {
		return "", err
	}
	if dir == "" {
		dir = m.SheetDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".pdf")
	return path, os.WriteFile(path, doc, 0o600)
}

// RegisterPath is the license register.
func (m *Manager) RegisterPath() string { return filepath.Join(m.Cfg.Storage.Etc, "licenses.toml") }

// SheetDir holds the printable per-slot sheets (they carry keys: root only).
func (m *Manager) SheetDir() string { return filepath.Join(m.Cfg.Storage.Etc, "sheets") }

// Licensed reports whether slot name is in the license register.
func (m *Manager) Licensed(name string) (bool, error) {
	reg, err := license.Load(m.RegisterPath())
	if err != nil {
		return false, err
	}
	_, ok := reg.Find(name)
	return ok, nil
}

// ScanResult is what LicenseScan found for one slot.
type ScanResult struct {
	Slot     string
	State    string // "recorded", "known", "unlicensed", "skipped", "warning", "error"
	Detail   string
	Recorded *license.Record // set when State is "recorded"
}

// LicenseScan checks running slots' activation against the register. A slot
// found activated and not yet registered is recorded, with its key if it
// can be recovered and confirmed. It never removes or changes a record's
// key or identity; discrepancies are reported as warnings.
func (m *Manager) LicenseScan(ctx context.Context, names []string, host string) ([]ScanResult, error) {
	reg, err := license.Load(m.RegisterPath())
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		entries, err := m.List()
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			names = append(names, e.Name)
		}
	}
	var results []ScanResult
	changed := false
	for _, name := range names {
		r := m.scanOne(ctx, reg, name, host)
		if r.Recorded != nil {
			if err := reg.Add(*r.Recorded); err != nil {
				r = ScanResult{Slot: name, State: "error", Detail: err.Error()}
			} else {
				changed = true
				if err := m.markLicensed(name, &r.Recorded.Activated); err != nil {
					r.State, r.Detail = "warning", r.Detail+"; marking the slot's metadata failed: "+err.Error()
				}
			}
		}
		results = append(results, r)
	}
	if changed {
		if err := reg.Save(m.RegisterPath()); err != nil {
			return results, err
		}
		for i, r := range results {
			if r.Recorded == nil {
				continue
			}
			if path, err := m.WriteSheet(r.Slot, ""); err != nil {
				results[i].Detail += "; sheet: " + err.Error()
			} else {
				results[i].Detail += "; sheet " + path
			}
		}
	}
	return results, nil
}

func (m *Manager) scanOne(ctx context.Context, reg *license.Register, name, host string) ScanResult {
	res := func(state, format string, args ...any) ScanResult {
		return ScanResult{Slot: name, State: state, Detail: fmt.Sprintf(format, args...)}
	}
	if _, err := m.Slot(name); err != nil {
		return res("error", "%v", err)
	}
	rec, known := reg.Find(name)
	st, err := m.Virt.State(name)
	if err != nil {
		return res("error", "%v", err)
	}
	if st != virt.Running {
		if known {
			return res("known", "not running (%s); recorded %s", st, rec.Activated.Format("2006-01-02"))
		}
		return res("skipped", "not running (%s)", st)
	}
	a, err := m.QueryActivation(ctx, name)
	if err != nil {
		return res("error", "%v", err)
	}

	if known {
		switch {
		case !a.Licensed():
			return res("warning", "in the register but Windows says %s", a.StatusText())
		case rec.Key != "" && !winkey.Matches(rec.Key, a.Partial) && !winkey.Matches(winkey.GenericPro, a.Partial):
			return res("warning", "registered key ends in %s, Windows now has a key ending in %s", winkey.Partial(rec.Key), a.Partial)
		}
		return res("known", "activated (%s)", keyText(*rec))
	}
	if !a.Licensed() {
		return res("unlicensed", "Windows says %s", a.StatusText())
	}

	// Newly activated: record it.
	x, err := m.Virt.XML(name)
	if err != nil {
		return res("error", "%v", err)
	}
	cur, err := domain.Parse(x)
	if err != nil {
		return res("error", "%v", err)
	}
	meta, _ := m.Slot(name)
	rec2 := license.Record{
		Slot: name, Edition: a.Edition, Channel: a.Channel, Partial: a.Partial,
		Activated: m.now().UTC(), Host: host, KeySource: license.KeyNone,
		VCPUs: cur.VCPUs, MemoryMiB: cur.MemoryMiB, DiskBytes: meta.DiskBytes, Nested: meta.Nested,
	}
	rec2.SetIdentity(cur.Identity)
	detail := "activated by digital license; no key on the VM. If you have its key: winslot license record " + name + " --key KEY"
	if !winkey.Matches(winkey.GenericPro, a.Partial) {
		if k, err := winkey.Decode(a.DPID); err == nil && winkey.Matches(k, a.Partial) {
			rec2.Key, rec2.KeySource = k, license.KeyDecoded
			detail = "activated; key recovered and confirmed by Windows"
		} else {
			detail = fmt.Sprintf("activated with a key ending in %s that could not be recovered: winslot license record %s --key KEY", a.Partial, name)
		}
	}
	r := res("recorded", "%s", detail)
	r.Recorded = &rec2
	return r
}

func keyText(r license.Record) string {
	if r.Key == "" {
		return "no key recorded"
	}
	return "key …" + winkey.Partial(r.Key) + ", " + r.KeySource
}

// RecordKey sets the key (and/or purchase note) of a registered slot. When
// the slot runs, the key must be the one Windows reports installed; a VM on
// a digital license (generic key installed) can't confirm it, and the key is
// stored as unchecked.
func (m *Manager) RecordKey(ctx context.Context, name, key, purchase string) error {
	reg, err := license.Load(m.RegisterPath())
	if err != nil {
		return err
	}
	rec, ok := reg.Find(name)
	if !ok {
		return fmt.Errorf("%s is not in the license register; it is recorded once Windows is activated (winslot license scan %s)", name, name)
	}
	if key != "" {
		k, err := winkey.Normalize(key)
		if err != nil {
			return err
		}
		partial := rec.Partial
		if st, err := m.Virt.State(name); err == nil && st == virt.Running {
			a, err := m.QueryActivation(ctx, name)
			if err != nil {
				return err
			}
			partial = a.Partial
		}
		switch {
		case winkey.Matches(k, partial):
			rec.Key, rec.KeySource, rec.Partial = k, license.KeyEntered, partial
		case winkey.Matches(winkey.GenericPro, partial):
			rec.Key, rec.KeySource = k, license.KeyUnchecked
			m.logf("%s runs on a digital license: the key can't be checked against Windows; recorded as unchecked", name)
		default:
			return fmt.Errorf("that key ends in %s; Windows on %s has a key ending in %s", winkey.Partial(k), name, partial)
		}
	}
	if purchase != "" {
		rec.Purchase = purchase
	}
	if err := reg.Save(m.RegisterPath()); err != nil {
		return err
	}
	path, err := m.WriteSheet(name, "")
	if err != nil {
		return err
	}
	m.logf("recorded %s (%s); sheet %s: print it again", name, keyText(*rec), path)
	return nil
}

// Release removes a slot from the register, for a retail transfer. The slot
// must be decommissioned or gone: the license moves elsewhere, the VM must
// not keep using it.
func (m *Manager) Release(name string) error {
	reg, err := license.Load(m.RegisterPath())
	if err != nil {
		return err
	}
	if _, ok := reg.Find(name); !ok {
		return fmt.Errorf("%s is not in the license register", name)
	}
	if st, err := m.Virt.State(name); err != nil {
		return err
	} else if st != virt.NotFound {
		if _, err := os.Stat(m.DiskPath(name)); err == nil {
			return fmt.Errorf("%s still has a disk; decommission it before releasing its license", name)
		}
	}
	reg.Remove(name)
	if err := reg.Save(m.RegisterPath()); err != nil {
		return err
	}
	if st, err := m.Virt.State(name); err == nil && st != virt.NotFound {
		if err := m.markLicensed(name, nil); err != nil {
			return err
		}
	}
	os.Remove(filepath.Join(m.SheetDir(), name+".pdf"))
	m.logf("released %s: no longer in the register; the slot is disposable (winslot vm delete %s)", name, name)
	return nil
}
