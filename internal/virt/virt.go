// Package virt is winslot's view of libvirt: the handful of operations the
// commands need, behind an interface so command logic can be tested without
// a hypervisor. The implementation talks libvirt's RPC protocol directly over
// the local socket (go-libvirt, no cgo).
package virt

import (
	"errors"
	"fmt"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/socket/dialers"

	"winslot/internal/slot"
)

// State is a domain's run state.
type State string

const (
	Running  State = "running"
	ShutOff  State = "shut off"
	Paused   State = "paused"
	Other    State = "other"
	NotFound State = "not found"
)

// Hypervisor is everything winslot asks of libvirt.
type Hypervisor interface {
	Define(xml string) error
	State(name string) (State, error)
	Start(name string) error
	Shutdown(name string) error
	SetAutostart(name string, on bool) error
	ListNames() ([]string, error)
	XML(name string) (string, error)

	// SlotMetadata returns winslot's <slot> element, ok=false if the domain
	// has none (it is not a winslot slot).
	SlotMetadata(name string) (xml string, ok bool, err error)
	SetSlotMetadata(name, xml string) error
	SetDescription(name, desc string) error

	// DeleteSnapshotMetadata drops libvirt's records of the domain's
	// snapshots (the data lived in the disk, which the caller deletes).
	DeleteSnapshotMetadata(name string) error

	// Undefine removes a shut-off domain with its NVRAM and vTPM. Only for
	// slots that are not precious (never activated).
	Undefine(name string) error

	// AddDNSSRV / DeleteDNSSRV edit a network's DNS SRV records, live and persistent.
	AddDNSSRV(network, srvXML string) error
	DeleteDNSSRV(network, srvXML string) error
}

// Conn implements Hypervisor over the local libvirtd socket.
type Conn struct{ l *libvirt.Libvirt }

// Connect opens qemu:///system on the local socket.
func Connect() (*Conn, error) {
	l := libvirt.NewWithDialer(dialers.NewLocal(dialers.WithLocalTimeout(5 * time.Second)))
	if err := l.ConnectToURI(libvirt.QEMUSystem); err != nil {
		return nil, fmt.Errorf("libvirt: %w", err)
	}
	return &Conn{l: l}, nil
}

// Close disconnects.
func (c *Conn) Close() error { return c.l.Disconnect() }

func (c *Conn) dom(name string) (libvirt.Domain, error) {
	d, err := c.l.DomainLookupByName(name)
	if err != nil {
		return d, fmt.Errorf("domain %s: %w", name, err)
	}
	return d, nil
}

func isNoDomain(err error) bool {
	return libvirt.IsNotFound(err)
}

func (c *Conn) Define(xml string) error {
	_, err := c.l.DomainDefineXML(xml)
	return err
}

func (c *Conn) State(name string) (State, error) {
	d, err := c.l.DomainLookupByName(name)
	if isNoDomain(err) {
		return NotFound, nil
	}
	if err != nil {
		return "", err
	}
	st, _, err := c.l.DomainGetState(d, 0)
	if err != nil {
		return "", err
	}
	switch libvirt.DomainState(st) {
	case libvirt.DomainRunning:
		return Running, nil
	case libvirt.DomainShutoff:
		return ShutOff, nil
	case libvirt.DomainPaused:
		return Paused, nil
	}
	return Other, nil
}

func (c *Conn) Start(name string) error {
	d, err := c.dom(name)
	if err != nil {
		return err
	}
	return c.l.DomainCreate(d)
}

func (c *Conn) Shutdown(name string) error {
	d, err := c.dom(name)
	if err != nil {
		return err
	}
	return c.l.DomainShutdown(d)
}

func (c *Conn) SetAutostart(name string, on bool) error {
	d, err := c.dom(name)
	if err != nil {
		return err
	}
	v := int32(0)
	if on {
		v = 1
	}
	return c.l.DomainSetAutostart(d, v)
}

func (c *Conn) ListNames() ([]string, error) {
	doms, _, err := c.l.ConnectListAllDomains(1, 0)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(doms))
	for i, d := range doms {
		names[i] = d.Name
	}
	return names, nil
}

func (c *Conn) XML(name string) (string, error) {
	d, err := c.dom(name)
	if err != nil {
		return "", err
	}
	return c.l.DomainGetXMLDesc(d, libvirt.DomainXMLInactive)
}

func (c *Conn) SlotMetadata(name string) (string, bool, error) {
	d, err := c.dom(name)
	if err != nil {
		return "", false, err
	}
	m, err := c.l.DomainGetMetadata(d, int32(libvirt.DomainMetadataElement),
		libvirt.OptString{slot.MetadataNamespace}, libvirt.DomainAffectConfig)
	var lerr libvirt.Error
	if errors.As(err, &lerr) && lerr.Code == uint32(libvirt.ErrNoDomainMetadata) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return m, true, nil
}

func (c *Conn) SetSlotMetadata(name, xml string) error {
	d, err := c.dom(name)
	if err != nil {
		return err
	}
	return c.l.DomainSetMetadata(d, int32(libvirt.DomainMetadataElement),
		libvirt.OptString{xml}, libvirt.OptString{"winslot"}, libvirt.OptString{slot.MetadataNamespace},
		libvirt.DomainAffectConfig)
}

func (c *Conn) SetDescription(name, desc string) error {
	d, err := c.dom(name)
	if err != nil {
		return err
	}
	return c.l.DomainSetMetadata(d, int32(libvirt.DomainMetadataDescription),
		libvirt.OptString{desc}, nil, nil, libvirt.DomainAffectConfig)
}

func (c *Conn) DeleteSnapshotMetadata(name string) error {
	d, err := c.dom(name)
	if err != nil {
		return err
	}
	snaps, _, err := c.l.DomainListAllSnapshots(d, 1, 0)
	if err != nil {
		return err
	}
	for _, s := range snaps {
		if err := c.l.DomainSnapshotDelete(s, libvirt.DomainSnapshotDeleteMetadataOnly); err != nil {
			return fmt.Errorf("snapshot %s: %w", s.Name, err)
		}
	}
	return nil
}

func (c *Conn) updateSRV(network, srvXML string, cmd libvirt.NetworkUpdateCommand) error {
	n, err := c.l.NetworkLookupByName(network)
	if err != nil {
		return fmt.Errorf("network %s: %w", network, err)
	}
	return c.l.NetworkUpdate(n, uint32(cmd), uint32(libvirt.NetworkSectionDNSSrv), -1, srvXML,
		libvirt.NetworkUpdateAffectLive|libvirt.NetworkUpdateAffectConfig)
}

func (c *Conn) AddDNSSRV(network, srvXML string) error {
	return c.updateSRV(network, srvXML, libvirt.NetworkUpdateCommandAddLast)
}

func (c *Conn) DeleteDNSSRV(network, srvXML string) error {
	return c.updateSRV(network, srvXML, libvirt.NetworkUpdateCommandDelete)
}

// Undefine removes a shut-off domain for good, with its UEFI variables,
// vTPM state and snapshot/checkpoint records.
func (c *Conn) Undefine(name string) error {
	d, err := c.dom(name)
	if err != nil {
		return err
	}
	return c.l.DomainUndefineFlags(d, libvirt.DomainUndefineManagedSave|libvirt.DomainUndefineSnapshotsMetadata|
		libvirt.DomainUndefineNvram|libvirt.DomainUndefineCheckpointsMetadata|libvirt.DomainUndefineTpm)
}
