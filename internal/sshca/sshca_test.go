package sshca

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// server is a minimal SSH server: it presents hostSigners, accepts clientKey,
// and answers every exec request with "<command>|<stdin>".
func server(t *testing.T, clientKey ssh.PublicKey, hostSigners ...ssh.Signer) string {
	t.Helper()
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if string(k.Marshal()) == string(clientKey.Marshal()) {
				return nil, nil
			}
			return nil, io.EOF
		},
	}
	for _, s := range hostSigners {
		cfg.AddHostKey(s)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(c, cfg)
		}
	}()
	return ln.Addr().String()
}

func serve(c net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for r := range creqs {
				if r.Type != "exec" {
					r.Reply(false, nil)
					continue
				}
				var p struct{ Command string }
				ssh.Unmarshal(r.Payload, &p)
				r.Reply(true, nil)
				in, _ := io.ReadAll(ch)
				io.WriteString(ch, p.Command+"|"+string(in))
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				return
			}
		}()
	}
}

func TestSignHost(t *testing.T) {
	ca, host := signer(t), signer(t)
	cert, err := SignHost(ca, host.PublicKey(), "test05-2026-10-11", "test05.vms.example.org")
	if err != nil {
		t.Fatal(err)
	}
	if cert.CertType != ssh.HostCert || cert.ValidBefore != ssh.CertTimeInfinity || cert.KeyId != "test05-2026-10-11" {
		t.Errorf("cert fields: %+v", cert)
	}
	checker := ssh.CertChecker{}
	if err := checker.CheckCert("test05.vms.example.org", cert); err != nil {
		t.Errorf("CheckCert: %v", err)
	}
	if err := checker.CheckCert("test06.vms.example.org", cert); err == nil {
		t.Error("cert valid for another principal")
	}
}

func TestRunPinned(t *testing.T) {
	client, host, impostor := signer(t), signer(t), signer(t)
	addr := server(t, client.PublicKey(), host)

	out, err := Run(addr, "admin", client, PinnedHostKey(host.PublicKey()), PlainAlgos, "hostname", "cert-bytes")
	if err != nil || out != "hostname|cert-bytes" {
		t.Fatalf("pinned to the right key: out=%q err=%v", out, err)
	}
	if _, err := Run(addr, "admin", client, PinnedHostKey(impostor.PublicKey()), PlainAlgos, "hostname", ""); err == nil {
		t.Fatal("pinned to a different key: connection was accepted")
	}
}

func TestRunCAVerified(t *testing.T) {
	client, host, ca, rogueCA := signer(t), signer(t), signer(t), signer(t)
	const fqdn = "test05.vms.example.org"
	cert, err := SignHost(ca, host.PublicKey(), "test05", fqdn)
	if err != nil {
		t.Fatal(err)
	}
	certSigner, err := ssh.NewCertSigner(cert, host)
	if err != nil {
		t.Fatal(err)
	}
	addr := server(t, client.PublicKey(), certSigner)
	port := addr[strings.LastIndex(addr, ":"):]

	// The CertChecker validates principals against the dialled host name, so
	// dial by name (resolves to 127.0.0.1 via the test resolver below).
	dial := func(caKey ssh.PublicKey, name string) error {
		cb := CAHostKey(caKey)
		_, err := Run(addr, "admin", client, func(_ string, remote net.Addr, key ssh.PublicKey) error {
			return cb(name+port, remote, key)
		}, CertAlgos, "hostname", "")
		return err
	}
	if err := dial(ca.PublicKey(), fqdn); err != nil {
		t.Errorf("our CA, right name: %v", err)
	}
	if err := dial(rogueCA.PublicKey(), fqdn); err == nil {
		t.Error("rogue CA accepted")
	}
	if err := dial(ca.PublicKey(), "test06.vms.example.org"); err == nil {
		t.Error("certificate accepted for the wrong host name")
	}
}
