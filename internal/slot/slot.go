// Package slot defines a Windows license slot: the fixed virtual-hardware
// identity a Windows license is bound to, and the metadata winslot keeps in
// the libvirt domain.
//
// Windows activation computes a hardware ID from what the VM presents. A slot
// pins every identity-bearing value, so a VM recreated from the slot on a
// fresh disk is, to Windows and to Microsoft's activation servers, the same
// machine. Nothing in Identity may change once Windows has been activated.
package slot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Machine is the QEMU machine type every slot gets. It is the upstream
// versioned type, not a distro alias like pc-q35-noble, so it means the same
// thing on any host with QEMU >= 8.2, now and after upgrades.
const Machine = "pc-q35-8.2"

// MetadataNamespace is the XML namespace of winslot's <slot> element in
// libvirt domain metadata.
const MetadataNamespace = "https://w8hhf.org/xmlns/winslot"

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,14}$`)

// ValidateName checks that name can be both a libvirt domain name and the
// Windows hostname (NetBIOS: at most 15 characters).
func ValidateName(name string) error {
	if !nameRE.MatchString(name) || strings.HasSuffix(name, "-") {
		return fmt.Errorf("invalid slot name %q: lowercase letters, digits and '-', at most 15 characters, not ending in '-' (it becomes the Windows hostname)", name)
	}
	return nil
}

// Identity is the virtual hardware Windows activation is bound to.
type Identity struct {
	UUID       string // SMBIOS system UUID
	MAC        string // NIC MAC, QEMU's locally administered 52:54:00 prefix
	DiskSerial string // SCSI disk serial
	DiskWWN    string // SCSI disk WWN (NAA 5), 16 hex digits
	Machine    string // QEMU machine type
}

// NewIdentity generates a fresh identity from crypto/rand.
func NewIdentity() (Identity, error) { return newIdentity(rand.Reader) }

func newIdentity(r io.Reader) (Identity, error) {
	b := make([]byte, 16+3+7+5)
	if _, err := io.ReadFull(r, b); err != nil {
		return Identity{}, fmt.Errorf("slot: random: %w", err)
	}
	u, mac, serial, wwn := b[:16], b[16:19], b[19:26], b[26:31]
	u[6] = u[6]&0x0f | 0x40 // RFC 4122 version 4
	u[8] = u[8]&0x3f | 0x80 // RFC 4122 variant
	return Identity{
		UUID:       fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16]),
		MAC:        fmt.Sprintf("52:54:00:%02x:%02x:%02x", mac[0], mac[1], mac[2]),
		DiskSerial: "WS" + strings.ToUpper(hex.EncodeToString(serial)),
		// NAA 5 (IEEE registered), then 36 random bits of vendor-specific ID.
		DiskWWN: "5525400" + hex.EncodeToString(wwn)[:9],
		Machine: Machine,
	}, nil
}

// Metadata is winslot's record inside the libvirt domain
// (<metadata><winslot:slot .../></metadata>).
type Metadata struct {
	XMLName   xml.Name   `xml:"slot"` // libvirt binds MetadataNamespace on write and returns the element without it
	Edition   string     `xml:"edition,attr"`
	DiskBytes uint64     `xml:"disk_bytes,attr"`
	Created   time.Time  `xml:"created,attr"`
	Golden    string     `xml:"golden,attr"`
	Recreated *time.Time `xml:"recreated,attr,omitempty"`
	Nested    bool       `xml:"nested,attr,omitempty"`   // VT-x exposed to the guest (WSL2, Docker, VBS)
	Licensed  *time.Time `xml:"licensed,attr,omitempty"` // first seen activated: the identity is precious (also in the license register)
}

// ParseMetadata decodes the <slot> element libvirt returns for our namespace.
func ParseMetadata(s string) (Metadata, error) {
	var m Metadata
	if err := xml.Unmarshal([]byte(s), &m); err != nil {
		return Metadata{}, fmt.Errorf("slot metadata: %w", err)
	}
	if m.Edition == "" || m.Golden == "" || m.DiskBytes == 0 {
		return Metadata{}, errors.New("slot metadata: incomplete <slot> element")
	}
	return m, nil
}

// Marshal encodes m for libvirt's metadata API (namespace bound by libvirt).
func (m Metadata) Marshal() (string, error) {
	b, err := xml.Marshal(m)
	return string(b), err
}
