package vm

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"

	"winslot/internal/domain"
	"winslot/internal/license"
	"winslot/internal/slot"
	"winslot/internal/virt"
)

// Delete removes a slot that was never activated: domain, UEFI variables,
// vTPM, disk, host keys, discovery record. A precious slot (in the license
// register, or marked licensed in its metadata) is refused: decommission
// it, or release its license first.
func (m *Manager) Delete(ctx context.Context, name string) error {
	if _, err := m.Slot(name); err != nil {
		return err
	}
	precious, err := m.Precious(name)
	if err != nil {
		return err
	}
	if precious {
		return fmt.Errorf("%s is licensed: its identity is what Windows' activation is bound to. Decommission it instead (winslot vm decommission %s), or release the license first if it is being transferred (winslot license release %s)", name, name, name)
	}
	if st, err := m.Virt.State(name); err != nil {
		return err
	} else if st != virt.ShutOff {
		return fmt.Errorf("slot %s is %s; shut it down first", name, st)
	}
	if err := m.Virt.Undefine(name); err != nil {
		return fmt.Errorf("undefine %s: %w", name, err)
	}
	if err := os.Remove(m.DiskPath(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := RemoveKnownHosts(filepath.Join(m.Cfg.Storage.Etc, "known_hosts"), m.FQDN(name)); err != nil {
		return err
	}
	os.Remove(m.SerialLog(name))
	if err := m.ReleaseIP(name); err != nil {
		return err
	}
	m.logf("deleted %s", name)
	return m.WithdrawSRV(name)
}

// Restore defines a slot from a license record (the register, or the QR
// code on its printed sheet) with exactly the recorded identity and no disk.
// "winslot vm recreate NAME" then installs it; Windows reactivates because it
// sees the same hardware.
func (m *Manager) Restore(ctx context.Context, rec license.Record) error {
	if err := rec.Validate(); err != nil {
		return err
	}
	name := rec.Slot
	if st, err := m.Virt.State(name); err != nil {
		return err
	} else if st != virt.NotFound {
		return fmt.Errorf("slot %s already exists on this host", name)
	}
	if _, err := os.Stat(m.DiskPath(name)); err == nil {
		return fmt.Errorf("%s exists; move it out of the way first", m.DiskPath(name))
	}
	reg, err := license.Load(m.RegisterPath())
	if err != nil {
		return err
	}
	old, inRegister := reg.Find(name)
	if inRegister && old.Identity() != rec.Identity() {
		return fmt.Errorf("the register already holds %s with a different identity; resolve that by hand in %s", name, m.RegisterPath())
	}
	if !inRegister {
		if err := reg.Add(rec); err != nil { // also refuses an identity recorded under another name
			return err
		}
	}
	d, err := domain.Build(domain.Spec{
		Name:        name,
		Title:       Title(name, m.Cfg.Windows.Edition),
		Description: fmt.Sprintf("%s license slot, restored %s from its license record. Never undefine: decommission keeps the identity.", m.Cfg.Windows.Edition, m.now().Format("2006-01-02")),
		Identity:    rec.Identity(),
		MemoryMiB:   rec.MemoryMiB,
		VCPUs:       rec.VCPUs,
		DiskPath:    m.DiskPath(name),
		Network:     m.Cfg.Network.Libvirt,
		SerialLog:   m.SerialLog(name),
		Nested:      rec.Nested,
		VirtFeature: m.VirtFeature,
	})
	if err != nil {
		return err
	}
	x, err := domain.XML(d)
	if err != nil {
		return err
	}
	if err := m.Virt.Define(x); err != nil {
		return fmt.Errorf("define %s: %w", name, err)
	}
	activated := rec.Activated
	meta := slot.Metadata{Edition: m.Cfg.Windows.Edition, DiskBytes: rec.DiskBytes, Created: m.now().UTC(),
		Golden: "(restored)", Nested: rec.Nested, Licensed: &activated}
	if err := m.setMetadata(name, meta); err != nil {
		return err
	}
	if !inRegister {
		if err := reg.Save(m.RegisterPath()); err != nil {
			return err
		}
	}
	if _, err := m.ReserveIP(name, netip.Addr{}); err != nil {
		return err
	}
	m.logf("restored slot %s (uuid %s); install it: winslot vm recreate %s", name, rec.UUID, name)
	return nil
}
