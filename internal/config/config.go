// Package config loads /etc/winslot/winslot.toml.
//
// Parsing is strict: unknown keys are an error, so a typo in the site config
// fails loudly instead of silently falling back to a default.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultPath is where winslot looks for its configuration.
const DefaultPath = "/etc/winslot/winslot.toml"

// Config is the site configuration of one winslot host.
type Config struct {
	Windows Windows `toml:"windows"`
	Storage Storage `toml:"storage"`
	Network Network `toml:"network"`
}

// Windows describes the install media and edition used for golden images.
type Windows struct {
	ISO      string `toml:"iso"`      // Windows install ISO
	Edition  string `toml:"edition"`  // e.g. "Windows 11 Pro"; recorded in each slot
	TimeZone string `toml:"timezone"` // Windows time zone ID for new VMs, e.g. "Eastern Standard Time"
}

// Storage locates winslot's data on the host.
type Storage struct {
	Data  string `toml:"data"`  // ZFS dataset mountpoint: images/, disks/, builds/
	Cache string `toml:"cache"` // payload downloads
	Etc   string `toml:"etc"`   // keys, CA, known_hosts
}

// Network describes the VM network.
type Network struct {
	Libvirt string     `toml:"libvirt"` // libvirt network name
	Zone    string     `toml:"zone"`    // DNS zone VMs register in
	Gateway netip.Addr `toml:"gateway"` // this host's address on the VM network
}

// Defaults returns the configuration used for any key the file omits.
func Defaults() Config {
	return Config{
		Windows: Windows{Edition: "Windows 11 Pro", TimeZone: "UTC"},
		Storage: Storage{Data: "/var/lib/winslot", Cache: "/var/cache/winslot", Etc: "/etc/winslot"},
		Network: Network{Libvirt: "default"},
	}
}

// Load reads and validates the configuration file at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(string(data))
}

// Parse decodes and validates TOML configuration text over the defaults.
func Parse(text string) (Config, error) {
	cfg := Defaults()
	md, err := toml.Decode(text, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return Config{}, fmt.Errorf("config: unknown keys: %s", strings.Join(keys, ", "))
	}
	return cfg, cfg.Validate()
}

// Validate reports the first missing or inconsistent setting.
func (c Config) Validate() error {
	var errs []error
	if c.Windows.ISO == "" {
		errs = append(errs, errors.New("windows.iso is required"))
	}
	if c.Network.Zone == "" {
		errs = append(errs, errors.New("network.zone is required"))
	}
	if !c.Network.Gateway.IsValid() {
		errs = append(errs, errors.New("network.gateway is required"))
	}
	for name, p := range map[string]string{"storage.data": c.Storage.Data, "storage.cache": c.Storage.Cache, "storage.etc": c.Storage.Etc} {
		if !strings.HasPrefix(p, "/") {
			errs = append(errs, fmt.Errorf("%s must be an absolute path, got %q", name, p))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}
