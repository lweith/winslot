package config

import (
	"strings"
	"testing"
)

const minimal = `
[windows]
iso = "/var/lib/libvirt/iso/win11.iso"
[network]
zone = "vms.example.org"
gateway = "10.0.3.1"
`

func TestParseMinimalUsesDefaults(t *testing.T) {
	cfg, err := Parse(minimal)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.Data != "/var/lib/winslot" || cfg.Network.Libvirt != "default" || cfg.Windows.Edition != "Windows 11 Pro" {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if cfg.Network.Gateway.String() != "10.0.3.1" {
		t.Errorf("gateway = %v", cfg.Network.Gateway)
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"unknown table", minimal + "\n[network.extra]\nfoo = 1\n", "unknown keys: network.extra"},
		{"typo in key", strings.Replace(minimal, "zone", "zoen", 1), "unknown keys: network.zoen"},
		{"missing iso", strings.Replace(minimal, `iso = "/var/lib/libvirt/iso/win11.iso"`, "", 1), "windows.iso is required"},
		{"bad gateway", strings.Replace(minimal, "10.0.3.1", "not-an-ip", 1), "config:"},
		{"relative path", minimal + "\n[storage]\ndata = \"var/lib\"\n", "storage.data must be an absolute path"},
		{"not toml", "[[[", "config:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
