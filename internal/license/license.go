// Package license is winslot's license register: the slots whose Windows is
// activated, with the key (when it can be known) and everything needed to
// recreate the slot's identity. A slot in the register is precious: its
// identity is what Microsoft's activation is bound to. Slots not in it are
// disposable.
//
// The register is a TOML file readable by root only (it holds product keys).
// Each record also stands alone, as the text in the QR code on the slot's
// printed sheet, which "winslot vm restore" reads back.
package license

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/BurntSushi/toml"

	"winslot/internal/slot"
	"winslot/internal/winkey"
)

// How a record's key is known.
const (
	KeyDecoded   = "decoded"           // read from the VM, confirmed by the licensing service
	KeyEntered   = "entered"           // typed in, confirmed by the licensing service
	KeyUnchecked = "entered-unchecked" // typed in while the VM ran on a digital license (nothing to check against)
	KeyNone      = "none"              // digital license, or not recoverable: record it with "winslot license record"
)

// Record is one licensed slot.
type Record struct {
	Slot      string    `toml:"slot"`
	Edition   string    `toml:"edition"` // as Windows names it
	Channel   string    `toml:"channel"` // Retail, OEM:DM, ...
	Key       string    `toml:"key,omitempty"`
	KeySource string    `toml:"key_source"`
	Partial   string    `toml:"partial_key"` // last five characters Windows reported when recorded
	Activated time.Time `toml:"activated"`   // first seen activated
	Purchase  string    `toml:"purchase,omitempty"`
	Host      string    `toml:"host"`

	// Identity: what activation is bound to. Never edited.
	UUID       string `toml:"uuid"`
	MAC        string `toml:"mac"`
	DiskSerial string `toml:"disk_serial"`
	DiskWWN    string `toml:"disk_wwn"`
	Machine    string `toml:"machine"`

	// Sizing, to recreate the VM as it was.
	VCPUs     uint   `toml:"vcpus"`
	MemoryMiB uint   `toml:"memory_mib"`
	DiskBytes uint64 `toml:"disk_bytes"`
	Nested    bool   `toml:"nested,omitempty"`
}

// Identity returns the record's slot identity.
func (r Record) Identity() slot.Identity {
	return slot.Identity{UUID: r.UUID, MAC: r.MAC, DiskSerial: r.DiskSerial, DiskWWN: r.DiskWWN, Machine: r.Machine}
}

// SetIdentity copies id into the record.
func (r *Record) SetIdentity(id slot.Identity) {
	r.UUID, r.MAC, r.DiskSerial, r.DiskWWN, r.Machine = id.UUID, id.MAC, id.DiskSerial, id.DiskWWN, id.Machine
}

// Validate checks a record is complete and self-consistent.
func (r Record) Validate() error {
	if err := slot.ValidateName(r.Slot); err != nil {
		return err
	}
	id := r.Identity()
	if id.UUID == "" || id.MAC == "" || id.DiskSerial == "" || id.DiskWWN == "" || id.Machine == "" {
		return fmt.Errorf("license record %s: identity incomplete", r.Slot)
	}
	if r.VCPUs == 0 || r.MemoryMiB == 0 || r.DiskBytes == 0 {
		return fmt.Errorf("license record %s: sizing incomplete", r.Slot)
	}
	switch r.KeySource {
	case KeyDecoded, KeyEntered, KeyUnchecked:
		k, err := winkey.Normalize(r.Key)
		if err != nil {
			return fmt.Errorf("license record %s: %w", r.Slot, err)
		}
		if r.KeySource != KeyUnchecked && !winkey.Matches(k, r.Partial) {
			return fmt.Errorf("license record %s: key does not end in %s, the key Windows reported", r.Slot, r.Partial)
		}
	case KeyNone:
		if r.Key != "" {
			return fmt.Errorf("license record %s: key_source none with a key", r.Slot)
		}
	default:
		return fmt.Errorf("license record %s: unknown key_source %q", r.Slot, r.KeySource)
	}
	return nil
}

// Marshal encodes a single record (the sheet's QR code; "vm restore" input).
func (r Record) Marshal() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("# winslot license record: winslot vm restore FILE\n")
	if err := toml.NewEncoder(&b).Encode(r); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// ParseRecord decodes and validates a single record.
func ParseRecord(data []byte) (Record, error) {
	var r Record
	md, err := toml.Decode(string(data), &r)
	if err != nil {
		return Record{}, fmt.Errorf("license record: %w", err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		return Record{}, fmt.Errorf("license record: unknown key %s", un[0])
	}
	return r, r.Validate()
}

// Register is the set of licensed slots.
type Register struct {
	Licenses []Record `toml:"license"`
}

// Load reads the register; a missing file is an empty register.
func Load(path string) (*Register, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Register{}, nil
	}
	if err != nil {
		return nil, err
	}
	var reg Register
	md, err := toml.Decode(string(data), &reg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		return nil, fmt.Errorf("%s: unknown key %s", path, un[0])
	}
	seen := map[string]bool{}
	for _, r := range reg.Licenses {
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if seen[r.Slot] || seen[r.UUID] {
			return nil, fmt.Errorf("%s: slot %s recorded twice", path, r.Slot)
		}
		seen[r.Slot], seen[r.UUID] = true, true
	}
	return &reg, nil
}

// Save writes the register atomically, root-only.
func (reg *Register) Save(path string) error {
	sort.Slice(reg.Licenses, func(i, j int) bool { return reg.Licenses[i].Slot < reg.Licenses[j].Slot })
	for _, r := range reg.Licenses {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	var b bytes.Buffer
	b.WriteString("# winslot license register: activated slots. Their identities are precious.\n" +
		"# Managed by winslot (license scan/record/release); back it up with the host.\n\n")
	if err := toml.NewEncoder(&b).Encode(reg); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Find returns the record for slot name.
func (reg *Register) Find(name string) (*Record, bool) {
	for i := range reg.Licenses {
		if reg.Licenses[i].Slot == name {
			return &reg.Licenses[i], true
		}
	}
	return nil, false
}

// Add records a slot; it must not already be recorded.
func (reg *Register) Add(r Record) error {
	if err := r.Validate(); err != nil {
		return err
	}
	for _, o := range reg.Licenses {
		if o.Slot == r.Slot || o.UUID == r.UUID {
			return fmt.Errorf("slot %s is already in the license register", r.Slot)
		}
	}
	reg.Licenses = append(reg.Licenses, r)
	return nil
}

// Remove deletes slot name's record.
func (reg *Register) Remove(name string) bool {
	for i, r := range reg.Licenses {
		if r.Slot == name {
			reg.Licenses = append(reg.Licenses[:i], reg.Licenses[i+1:]...)
			return true
		}
	}
	return false
}
