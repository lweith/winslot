// Package hostinit creates a winslot host's trust material: the SSH host CA
// that signs VM host keys, the management key that installs certificates, and
// the published CA trust line. It never replaces existing keys.
package hostinit

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"winslot/internal/config"
)

// Init makes sure every piece exists; it is safe to run repeatedly.
func Init(cfg config.Config, hostname string, now time.Time, out io.Writer) error {
	etc := cfg.Storage.Etc
	for _, d := range []string{etc, filepath.Join(cfg.Storage.Data, "images"), filepath.Join(cfg.Storage.Data, "disks"),
		filepath.Join(cfg.Storage.Data, "builds"), filepath.Join(cfg.Storage.Cache, "payload")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	ca, err := ensureKey(filepath.Join(etc, "host_ca"), fmt.Sprintf("winslot host CA (%s, %s)", hostname, now.Format("2006-01-02")), out)
	if err != nil {
		return err
	}
	mgmt, err := ensureKey(filepath.Join(etc, "mgmt_key"), "winslot-mgmt (root@"+hostname+")", out)
	if err != nil {
		return err
	}

	// The management key works only from this host's VM-network address.
	keys := filepath.Join(etc, "authorized_keys")
	mgmtLine := fmt.Sprintf(`from="%s" %s winslot-mgmt (root@%s)`, cfg.Network.Gateway, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(mgmt))), hostname)
	if err := ensureLine(keys, "winslot-mgmt", mgmtLine); err != nil {
		return err
	}

	// What admins put in their known_hosts: trust this CA for the VM zone only.
	trust := fmt.Sprintf("@cert-authority *.%s %s", cfg.Network.Zone, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(ca))))
	if err := os.WriteFile(filepath.Join(etc, "known_hosts.ca"), []byte(trust+"\n"), 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(etc, "known_hosts")); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(filepath.Join(etc, "known_hosts"), nil, 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "host CA: %s\n", ssh.FingerprintSHA256(ca))
	return nil
}

// ensureKey loads the ed25519 key at path, generating it if absent.
func ensureKey(path, comment string, out io.Writer) (ssh.PublicKey, error) {
	if b, err := os.ReadFile(path); err == nil {
		s, err := ssh.ParsePrivateKey(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return s.PublicKey(), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path+".pub", []byte(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))+" "+comment+"\n"), 0o644); err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "generated %s\n", path)
	return sshPub, nil
}

// ensureLine replaces the line containing marker with line, or appends it.
func ensureLine(path, marker, line string) error {
	var lines []string
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if !strings.Contains(sc.Text(), marker) {
				lines = append(lines, sc.Text())
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines = append(lines, line)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
