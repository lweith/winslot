package vm

import (
	"context"
	"fmt"

	"winslot/internal/domain"
)

// Title is what virt-manager shows for a slot in place of its name, so it
// starts with the name.
func Title(name, edition string) string {
	return name + " · " + edition + " license slot"
}

// Update re-renders a slot's definition from the current hardware template,
// carrying over its identity and sizing unchanged (read back from libvirt).
// It brings slots created by older winslot versions up to date. libvirt
// applies the new definition at the slot's next cold boot.
func (m *Manager) Update(ctx context.Context, name string) error {
	meta, err := m.Slot(name)
	if err != nil {
		return err
	}
	x, err := m.Virt.XML(name)
	if err != nil {
		return err
	}
	cur, err := domain.Parse(x)
	if err != nil {
		return err
	}
	d, err := domain.Build(domain.Spec{
		Name:        name,
		Title:       Title(name, meta.Edition),
		Description: cur.Description,
		Identity:    cur.Identity,
		MemoryMiB:   cur.MemoryMiB,
		VCPUs:       cur.VCPUs,
		DiskPath:    cur.DiskPath,
		Network:     cur.Network,
		SerialLog:   m.SerialLog(name),
		Nested:      meta.Nested,
		VirtFeature: m.VirtFeature,
	})
	if err != nil {
		return err
	}
	nx, err := domain.XML(d)
	if err != nil {
		return err
	}
	// Re-check: the new definition must carry exactly the identity we read.
	if again, err := domain.Parse(nx); err != nil || again.Identity != cur.Identity {
		return fmt.Errorf("update %s: re-rendered definition would change the identity; not applied", name)
	}
	if err := m.Virt.Define(nx); err != nil {
		return err
	}
	// Defining replaces <metadata>; put winslot's back.
	if err := m.setMetadata(name, meta); err != nil {
		return err
	}
	m.logf("updated %s (identity unchanged); takes effect at its next cold boot (shut down, then start)", name)
	return nil
}
