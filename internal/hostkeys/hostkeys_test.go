package hostkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func newKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func line(computer string, k ssh.PublicKey) string {
	return "HOSTKEY " + computer + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func TestParse(t *testing.T) {
	mine, other, newer := newKey(t), newKey(t), newKey(t)
	serial := strings.Join([]string{
		"GOLDEN 2026-10-11T04:21:16 provision complete",                                       // unrelated serial output
		line("TEST05", mine) + "\r",                                                           // CRLF from Windows, case differs
		line("test06", other),                                                                 // another VM
		"HOSTKEY test05 ssh-ed25519 not-base64",                                               // malformed
		"HOSTKEY test05 MISSING no-host-keys-after-3min",                                      // firstboot.ps1 failure marker
		"HOSTKEY test05 ssh-rsa " + strings.Fields(string(ssh.MarshalAuthorizedKey(mine)))[1], // type mismatch
		line("test05", newer),                                                                 // re-report replaces
	}, "\n")
	keys, err := Parse(strings.NewReader(serial), "test05")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || string(keys[0].Marshal()) != string(newer.Marshal()) {
		t.Fatalf("got %d keys, want exactly the newest ed25519 key", len(keys))
	}
	if ED25519(keys) == nil {
		t.Error("ED25519() = nil")
	}
	got := KnownHostsLines("test05.vms.example.org", keys)
	if len(got) != 1 || !strings.HasPrefix(got[0], "test05.vms.example.org ssh-ed25519 AAAA") {
		t.Errorf("KnownHostsLines = %q", got)
	}
}

func TestParseNone(t *testing.T) {
	keys, err := Parse(strings.NewReader("nothing here\n"), "x")
	if err != nil || len(keys) != 0 || ED25519(keys) != nil {
		t.Errorf("keys=%v err=%v", keys, err)
	}
}
