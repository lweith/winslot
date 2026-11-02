// Package vm implements the license-slot lifecycle: create, decommission,
// recreate, list, and certify (a priori SSH host keys and host certificate).
//
// A slot is a libvirt domain carrying winslot metadata. Its identity is never
// changed and the domain is never undefined: decommissioning deletes only the
// disk, recreating gives the same identity a fresh disk from the newest golden
// image. Windows therefore sees the same virtual hardware, which is what lets
// a license activated on the slot stay with it.
package vm

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"time"

	"winslot/internal/config"
	"winslot/internal/domain"
	"winslot/internal/image"
	"winslot/internal/license"
	"winslot/internal/slot"
	"winslot/internal/unattend"
	"winslot/internal/virt"
)

// SerialLogDir holds each VM's COM1 output (libvirt's log directory, which its
// AppArmor profile lets QEMU write).
const SerialLogDir = "/var/log/libvirt/qemu"

// UnattendPath is where Windows looks first for an answer file.
const UnattendPath = `Windows\Panther\Unattend\Unattend.xml`

// Disks prepares VM disks (disk.Tools).
type Disks interface {
	CopyGolden(ctx context.Context, golden, dst string, sizeBytes uint64) error
	WriteFile(ctx context.Context, image, winPath string, content []byte) error
}

// Manager runs slot operations.
type Manager struct {
	Cfg         config.Config
	Virt        virt.Hypervisor
	Disks       Disks
	Certifier   Certifier
	Out         io.Writer
	Now         func() time.Time
	VirtFeature string // host CPU virtualization flag: vmx or svm
	LogDir      string // serial logs; default SerialLogDir
	Version     string // printed on license sheets
}

func (m *Manager) logf(format string, args ...any) {
	if m.Out != nil {
		fmt.Fprintf(m.Out, format+"\n", args...)
	}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// DiskPath is the slot's disk.
func (m *Manager) DiskPath(name string) string {
	return filepath.Join(m.Cfg.Storage.Data, "disks", name+".qcow2")
}

// SerialLog is the slot's COM1 log.
func (m *Manager) SerialLog(name string) string {
	dir := m.LogDir
	if dir == "" {
		dir = SerialLogDir
	}
	return filepath.Join(dir, name+".serial.log")
}

// FQDN is the slot's DNS name.
func (m *Manager) FQDN(name string) string { return name + "." + m.Cfg.Network.Zone }

// CreateOptions size a new slot.
type CreateOptions struct {
	DiskBytes uint64
	MemoryMiB uint
	VCPUs     uint
	Nested    bool
}

// Create makes a new slot and its VM, then certifies it. It returns the VM's
// initial administrator password (shown once, stored nowhere).
func (m *Manager) Create(ctx context.Context, name string, o CreateOptions) (string, error) {
	if err := slot.ValidateName(name); err != nil {
		return "", err
	}
	if st, err := m.Virt.State(name); err != nil {
		return "", err
	} else if st != virt.NotFound {
		return "", fmt.Errorf("slot %s already exists (to give it a new disk: winslot vm recreate %s)", name, name)
	}
	golden, goldenPath, err := image.Latest(filepath.Join(m.Cfg.Storage.Data, "images"))
	if err != nil {
		return "", err
	}
	id, err := slot.NewIdentity()
	if err != nil {
		return "", err
	}
	m.logf("== slot %s: uuid %s, mac %s, disk serial %s, wwn %s, %s, %s", name, id.UUID, id.MAC, id.DiskSerial, id.DiskWWN, id.Machine, id.CPU)

	pw, err := m.freshDisk(ctx, name, goldenPath, golden, o.DiskBytes)
	if err != nil {
		return "", err
	}

	m.logf("== define")
	d, err := domain.Build(domain.Spec{
		Name:        name,
		Title:       Title(name, m.Cfg.Windows.Edition),
		Description: fmt.Sprintf("%s license slot, created %s. Never undefine: decommission keeps the identity.", m.Cfg.Windows.Edition, m.now().Format("2006-01-02")),
		Identity:    id,
		MemoryMiB:   o.MemoryMiB,
		VCPUs:       o.VCPUs,
		DiskPath:    m.DiskPath(name),
		Network:     m.Cfg.Network.Libvirt,
		SerialLog:   m.SerialLog(name),
		Nested:      o.Nested,
		VirtFeature: m.VirtFeature,
	})
	if err != nil {
		return "", err
	}
	x, err := domain.XML(d)
	if err != nil {
		return "", err
	}
	if err := m.Virt.Define(x); err != nil {
		return "", fmt.Errorf("define %s: %w", name, err)
	}
	meta := slot.Metadata{Edition: m.Cfg.Windows.Edition, DiskBytes: o.DiskBytes, Created: m.now().UTC(), Golden: golden, Nested: o.Nested}
	if err := m.setMetadata(name, meta); err != nil {
		return "", err
	}
	if _, err := m.ReserveIP(name, netip.Addr{}); err != nil {
		return "", err
	}
	return pw, m.boot(ctx, name)
}

// Recreate gives a decommissioned slot a fresh disk from the newest golden
// image under its unchanged identity. diskBytes 0 keeps the slot's size.
func (m *Manager) Recreate(ctx context.Context, name string, diskBytes uint64) (string, error) {
	meta, err := m.Slot(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(m.DiskPath(name)); err == nil {
		return "", fmt.Errorf("slot %s still has a disk; decommission it first", name)
	}
	if st, err := m.Virt.State(name); err != nil {
		return "", err
	} else if st != virt.ShutOff {
		return "", fmt.Errorf("slot %s is %s, not shut off", name, st)
	}
	if diskBytes == 0 {
		diskBytes = meta.DiskBytes
	}
	golden, goldenPath, err := image.Latest(filepath.Join(m.Cfg.Storage.Data, "images"))
	if err != nil {
		return "", err
	}
	pw, err := m.freshDisk(ctx, name, goldenPath, golden, diskBytes)
	if err != nil {
		return "", err
	}
	now := m.now().UTC()
	meta.Golden, meta.DiskBytes, meta.Recreated = golden, diskBytes, &now
	if err := m.setMetadata(name, meta); err != nil {
		return "", err
	}
	if err := m.Virt.SetDescription(name, fmt.Sprintf("%s license slot, recreated %s from %s. Never undefine: decommission keeps the identity.", m.Cfg.Windows.Edition, now.Format("2006-01-02"), golden)); err != nil {
		return "", err
	}
	// A fresh install also gets the current hardware template (identity kept).
	if err := m.Update(ctx, name); err != nil {
		return "", err
	}
	if _, err := m.ReserveIP(name, netip.Addr{}); err != nil { // keeps an existing reservation
		return "", err
	}
	return pw, m.boot(ctx, name)
}

// freshDisk copies the golden image and writes the first-boot answer file.
func (m *Manager) freshDisk(ctx context.Context, name, goldenPath, golden string, diskBytes uint64) (string, error) {
	m.logf("== disk: %s -> %s (%d GiB)", golden, m.DiskPath(name), diskBytes>>30)
	if err := os.MkdirAll(filepath.Dir(m.DiskPath(name)), 0o755); err != nil {
		return "", err
	}
	if err := m.Disks.CopyGolden(ctx, goldenPath, m.DiskPath(name), diskBytes); err != nil {
		return "", err
	}
	pw, err := unattend.NewPassword(20)
	if err != nil {
		return "", err
	}
	answer, err := unattend.Render(unattend.Params{ComputerName: name, TimeZone: m.Cfg.Windows.TimeZone, AdminPassword: pw})
	if err != nil {
		return "", err
	}
	m.logf("== disk: first-boot answer file (hostname %s, new admin password)", name)
	if err := m.Disks.WriteFile(ctx, m.DiskPath(name), UnattendPath, answer); err != nil {
		os.Remove(m.DiskPath(name))
		return "", err
	}
	return pw, nil
}

// boot starts the VM and certifies it.
func (m *Manager) boot(ctx context.Context, name string) error {
	m.logf("== start")
	os.Remove(m.SerialLog(name)) // no stale HOSTKEY lines from a previous life
	if err := m.Virt.Start(name); err != nil {
		return err
	}
	if err := m.Virt.SetAutostart(name, true); err != nil {
		return err
	}
	if err := m.Certify(ctx, name); err != nil {
		return err
	}
	return m.PublishSRV(name)
}

// Decommission deletes a slot's disk and snapshots and keeps its identity.
// The VM must already be shut off (the CLI asks Windows to shut down first).
func (m *Manager) Decommission(ctx context.Context, name string) error {
	meta, err := m.Slot(name)
	if err != nil {
		return err
	}
	if st, err := m.Virt.State(name); err != nil {
		return err
	} else if st != virt.ShutOff {
		return fmt.Errorf("slot %s is %s; shut it down first", name, st)
	}
	m.logf("== delete disk and snapshots")
	if err := m.Virt.DeleteSnapshotMetadata(name); err != nil {
		return err
	}
	if err := os.Remove(m.DiskPath(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := m.Virt.SetAutostart(name, false); err != nil {
		return err
	}
	m.logf("== forget host keys, serial log, discovery record")
	if err := RemoveKnownHosts(filepath.Join(m.Cfg.Storage.Etc, "known_hosts"), m.FQDN(name)); err != nil {
		return err
	}
	os.Remove(m.SerialLog(name))
	if err := m.WithdrawSRV(name); err != nil {
		return err
	}
	return m.Virt.SetDescription(name, fmt.Sprintf("%s license slot. DECOMMISSIONED %s: no disk. Recreate with: winslot vm recreate %s",
		meta.Edition, m.now().Format("2006-01-02"), name))
}

// Slot returns name's winslot metadata, or an error if it is not a slot.
func (m *Manager) Slot(name string) (slot.Metadata, error) {
	x, ok, err := m.Virt.SlotMetadata(name)
	if err != nil {
		return slot.Metadata{}, err
	}
	if !ok {
		return slot.Metadata{}, fmt.Errorf("%s is not a winslot license slot", name)
	}
	return slot.ParseMetadata(x)
}

func (m *Manager) setMetadata(name string, meta slot.Metadata) error {
	x, err := meta.Marshal()
	if err != nil {
		return err
	}
	return m.Virt.SetSlotMetadata(name, x)
}

// Entry is one row of List.
type Entry struct {
	Name    string
	State   string // libvirt state, or "decommissioned" when the slot has no disk
	Meta    slot.Metadata
	License string // key summary when licensed, empty when disposable
}

// List returns every slot, sorted by name. Domains without winslot metadata
// (other VMs on the host) are left out.
func (m *Manager) List() ([]Entry, error) {
	names, err := m.Virt.ListNames()
	if err != nil {
		return nil, err
	}
	// The register is root-only; a libvirt-group user still gets the list.
	reg, regErr := license.Load(m.RegisterPath())
	if regErr != nil {
		reg = &license.Register{}
	}
	var out []Entry
	for _, n := range names {
		x, ok, err := m.Virt.SlotMetadata(n)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		meta, err := slot.ParseMetadata(x)
		if err != nil {
			// One unreadable slot must not hide the others.
			out = append(out, Entry{Name: n, State: "invalid metadata: " + err.Error()})
			continue
		}
		state := "decommissioned"
		if _, err := os.Stat(m.DiskPath(n)); err == nil {
			st, err := m.Virt.State(n)
			if err != nil {
				return nil, err
			}
			state = string(st)
		}
		lic := ""
		if regErr != nil {
			lic = "? (register unreadable)"
		} else if r, ok := reg.Find(n); ok {
			lic = keyText(*r)
		} else if meta.Licensed != nil {
			lic = "licensed (not in register!)"
		}
		out = append(out, Entry{Name: n, State: state, Meta: meta, License: lic})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
