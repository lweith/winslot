// Package hostkeys reads the SSH host keys a VM reports on its serial port at
// first boot (answer/firstboot.ps1 writes "HOSTKEY <computername> <type> <base64>").
//
// That report arrives over a hypervisor-local channel before anyone has
// connected to the VM, so the keys are known a priori: no trust on first use.
package hostkeys

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Parse returns the public keys reported for computer (case-insensitive) in
// serial output r. Lines for other computers, malformed lines and
// non-HOSTKEY output are ignored; a later report for the same key type
// replaces an earlier one.
func Parse(r io.Reader, computer string) ([]ssh.PublicKey, error) {
	byType := map[string]ssh.PublicKey{}
	var order []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Fields(strings.TrimRight(sc.Text(), "\r"))
		if len(f) != 4 || f[0] != "HOSTKEY" || !strings.EqualFold(f[1], computer) {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(f[2] + " " + f[3]))
		if err != nil || key.Type() != f[2] {
			continue
		}
		if _, seen := byType[key.Type()]; !seen {
			order = append(order, key.Type())
		}
		byType[key.Type()] = key
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("hostkeys: %w", err)
	}
	keys := make([]ssh.PublicKey, len(order))
	for i, t := range order {
		keys[i] = byType[t]
	}
	return keys, nil
}

// ED25519 returns the ed25519 key among keys, or nil.
func ED25519(keys []ssh.PublicKey) ssh.PublicKey {
	for _, k := range keys {
		if k.Type() == ssh.KeyAlgoED25519 {
			return k
		}
	}
	return nil
}

// KnownHostsLines renders keys as known_hosts lines for host.
func KnownHostsLines(host string, keys []ssh.PublicKey) []string {
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = host + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
	}
	return lines
}
