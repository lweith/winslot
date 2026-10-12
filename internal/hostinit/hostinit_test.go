package hostinit

import (
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"winslot/internal/config"
)

func cfg(t *testing.T) config.Config {
	base := t.TempDir()
	c := config.Defaults()
	c.Storage = config.Storage{Data: filepath.Join(base, "data"), Cache: filepath.Join(base, "cache"), Etc: filepath.Join(base, "etc")}
	c.Network.Zone = "vms.example.org"
	c.Network.Gateway = netip.MustParseAddr("10.0.3.1")
	return c
}

func TestInitCreatesAndIsIdempotent(t *testing.T) {
	c := cfg(t)
	os.MkdirAll(c.Storage.Etc, 0o755)
	os.WriteFile(filepath.Join(c.Storage.Etc, "authorized_keys"), []byte("ssh-ed25519 AAAAadmin jon\n"), 0o644)
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)

	if err := Init(c, "vmhost", now, io.Discard); err != nil {
		t.Fatal(err)
	}
	caBytes, _ := os.ReadFile(filepath.Join(c.Storage.Etc, "host_ca"))
	if st, _ := os.Stat(filepath.Join(c.Storage.Etc, "host_ca")); st.Mode().Perm() != 0o600 {
		t.Errorf("host_ca mode %v", st.Mode().Perm())
	}
	ca, err := ssh.ParsePrivateKey(caBytes)
	if err != nil {
		t.Fatal(err)
	}
	trust, _ := os.ReadFile(filepath.Join(c.Storage.Etc, "known_hosts.ca"))
	wantTrust := "@cert-authority *.vms.example.org " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(ca.PublicKey())))
	if strings.TrimSpace(string(trust)) != wantTrust {
		t.Errorf("known_hosts.ca = %q, want the CA's own key", trust)
	}
	keys, _ := os.ReadFile(filepath.Join(c.Storage.Etc, "authorized_keys"))
	if !strings.Contains(string(keys), "AAAAadmin jon") || !strings.Contains(string(keys), `from="10.0.3.1" ssh-ed25519 `) {
		t.Errorf("authorized_keys:\n%s", keys)
	}

	// Second run (e.g. after a gateway change): same CA, one management line.
	c.Network.Gateway = netip.MustParseAddr("172.16.30.1")
	if err := Init(c, "vmhost", now.Add(time.Hour), io.Discard); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(c.Storage.Etc, "host_ca"))
	if string(again) != string(caBytes) {
		t.Error("host CA was replaced")
	}
	keys, _ = os.ReadFile(filepath.Join(c.Storage.Etc, "authorized_keys"))
	if strings.Count(string(keys), "winslot-mgmt") != 1 || !strings.Contains(string(keys), `from="172.16.30.1"`) {
		t.Errorf("authorized_keys after re-init:\n%s", keys)
	}
}

func TestInitRejectsCorruptKey(t *testing.T) {
	c := cfg(t)
	os.MkdirAll(c.Storage.Etc, 0o755)
	os.WriteFile(filepath.Join(c.Storage.Etc, "host_ca"), []byte("garbage"), 0o600)
	if err := Init(c, "h", time.Now(), io.Discard); err == nil {
		t.Error("corrupt CA key accepted (and would have been silently replaced)")
	}
}
