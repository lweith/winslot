// Package console keeps the host's break-glass information on the local
// console login prompt: a random root password, the SSH host key fingerprints
// and the VM host CA fingerprint. Whoever can see the console (the screen,
// IPMI, a serial line) owns the box anyway; showing them root's password
// costs nothing and gives a way in when SSH or the network is broken. SSH
// itself never accepts root or passwords (install/bootstrap).
//
// The password is generated once and kept in a root-only state file together
// with the shadow hash it produced. The issue file is rewritten at every boot
// from the live system, so it follows regenerated host keys, and a root
// password changed by hand is reported as unknown instead of shown wrong.
package console

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/crypto/ssh"
)

// Paths are the files Update reads and writes.
type Paths struct {
	State  string // generated password and its shadow hash (0600)
	Shadow string // /etc/shadow
	SSHDir string // directory holding ssh_host_*_key.pub
	HostCA string // the VM host CA's public key; may be absent
	Issue  string // agetty issue file written (0600; agetty runs as root)
}

// DefaultPaths are the installed locations. /etc/issue.d (not /run/issue.d):
// agetty ignores /run while /etc/issue exists, which it does on Ubuntu.
func DefaultPaths(etc string) Paths {
	return Paths{
		State:  filepath.Join(etc, "console-root.toml"),
		Shadow: "/etc/shadow",
		SSHDir: "/etc/ssh",
		HostCA: filepath.Join(etc, "host_ca.pub"),
		Issue:  "/etc/issue.d/40-winslot.issue",
	}
}

// State is the state file's content.
type State struct {
	Password string    `toml:"password"`
	Shadow   string    `toml:"shadow"` // root's hash right after winslot set it
	Set      time.Time `toml:"set"`
}

// SetPassword sets root's password (chpasswd in production).
type SetPassword func(password string) error

// Result says what Update did.
type Result struct {
	Generated bool // a new password was set
	Known     bool // the shown password is root's current one
	Keys      int  // host keys listed
}

// alphabet: no 0/O, 1/l/I, no backslash (agetty escapes) or shell quotes.
const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewPassword returns 20 random characters in dash-separated groups of four
// (about 117 bits; the dashes are part of the password).
func NewPassword() (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(alphabet)))
	for i := 0; i < 20; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[n.Int64()])
	}
	return b.String(), nil
}

// Update makes sure root has a winslot-generated password (a new one when
// regenerate is set or none exists yet) and rewrites the issue file.
func Update(p Paths, regenerate bool, now time.Time, set SetPassword) (Result, error) {
	var res Result
	st, err := loadState(p.State)
	if err != nil {
		return res, err
	}
	if st.Password == "" || regenerate {
		pw, err := NewPassword()
		if err != nil {
			return res, err
		}
		if err := set(pw); err != nil {
			return res, fmt.Errorf("set root password: %w", err)
		}
		hash, err := rootHash(p.Shadow)
		if err != nil {
			return res, err
		}
		st = State{Password: pw, Shadow: hash, Set: now.UTC().Truncate(time.Second)}
		if err := saveState(p.State, st); err != nil {
			return res, err
		}
		res.Generated = true
	}
	hash, err := rootHash(p.Shadow)
	if err != nil {
		return res, err
	}
	res.Known = hash == st.Shadow

	keys, err := hostKeys(p.SSHDir)
	if err != nil {
		return res, err
	}
	res.Keys = len(keys)
	var ca ssh.PublicKey
	if b, err := os.ReadFile(p.HostCA); err == nil {
		if ca, _, _, _, err = ssh.ParseAuthorizedKey(b); err != nil {
			return res, fmt.Errorf("%s: %w", p.HostCA, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return res, err
	}
	text := Render(st, res.Known, keys, ca)
	return res, writeFile(p.Issue, []byte(text), 0o600)
}

// Render is the issue file text. agetty expands \n (the hostname); anything
// else that could hold a backslash is escaped.
func Render(st State, known bool, keys []ssh.PublicKey, ca ssh.PublicKey) string {
	var b strings.Builder
	b.WriteString("Break glass, console only (SSH takes keys only, never root):\n")
	if known {
		fmt.Fprintf(&b, "  root password  %s\n", esc(st.Password))
	} else {
		b.WriteString("  root password  changed by hand since winslot set it: not known here.\n")
		b.WriteString("                 A new one: winslot host console --new-password\n")
	}
	b.WriteString("\nSSH host keys of \\n:\n")
	if len(keys) == 0 {
		b.WriteString("  (none found in /etc/ssh)\n")
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "  %-8s %s\n", keyName(k), esc(ssh.FingerprintSHA256(k)))
	}
	if ca != nil {
		fmt.Fprintf(&b, "VM host CA:\n  %-8s %s\n", keyName(ca), esc(ssh.FingerprintSHA256(ca)))
	}
	b.WriteString("\n")
	return b.String()
}

// keyName is ssh-keygen -l's label: ED25519, ECDSA, RSA.
func keyName(k ssh.PublicKey) string {
	switch t := k.Type(); {
	case t == ssh.KeyAlgoED25519:
		return "ED25519"
	case strings.HasPrefix(t, "ecdsa-"):
		return "ECDSA"
	case t == ssh.KeyAlgoRSA:
		return "RSA"
	default:
		return t
	}
}

func esc(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }

// hostKeys reads ssh_host_*_key.pub in dir, sorted by file name.
func hostKeys(dir string) ([]ssh.PublicKey, error) {
	files, err := filepath.Glob(filepath.Join(dir, "ssh_host_*_key.pub"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var keys []ssh.PublicKey
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		k, _, _, _, err := ssh.ParseAuthorizedKey(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// rootHash returns root's password field from a shadow file.
func rootHash(shadow string) (string, error) {
	f, err := os.Open(shadow)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) > 1 && fields[0] == "root" {
			return fields[1], nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s: no root entry", shadow)
}

func loadState(path string) (State, error) {
	var st State
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	md, err := toml.Decode(string(b), &st)
	if err != nil {
		return st, fmt.Errorf("%s: %w", path, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return st, fmt.Errorf("%s: unknown key %s", path, u[0])
	}
	return st, nil
}

func saveState(path string, st State) error {
	var b bytes.Buffer
	b.WriteString("# winslot break-glass root password (shown on the console login prompt).\n")
	b.WriteString("# shadow: root's hash when winslot set it; a different one means changed by hand.\n")
	if err := toml.NewEncoder(&b).Encode(st); err != nil {
		return err
	}
	return writeFile(path, b.Bytes(), 0o600)
}

// writeFile replaces path atomically with the given mode.
func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
