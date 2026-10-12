// Package sshca signs VM host keys with the site's SSH host CA and talks to
// VMs over SSH pinned to the host key captured at first boot.
package sshca

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// LoadSigner reads an unencrypted OpenSSH private key (the host CA or the
// management key).
func LoadSigner(path string) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("sshca: %s: %w", path, err)
	}
	return s, nil
}

// SignHost issues a host certificate for hostKey, valid for principal (the
// VM's FQDN) forever: the host key lives as long as the VM, and revocation is
// done by rotating the CA.
func SignHost(ca ssh.Signer, hostKey ssh.PublicKey, keyID, principal string) (*ssh.Certificate, error) {
	cert := &ssh.Certificate{
		Key:             hostKey,
		CertType:        ssh.HostCert,
		KeyId:           keyID,
		ValidPrincipals: []string{principal},
		ValidAfter:      0,
		ValidBefore:     ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		return nil, fmt.Errorf("sshca: sign: %w", err)
	}
	return cert, nil
}

// PinnedHostKey accepts exactly one host key: the one learned over the serial
// port. Any other key, including a valid certificate, is rejected.
func PinnedHostKey(want ssh.PublicKey) ssh.HostKeyCallback {
	return func(host string, _ net.Addr, got ssh.PublicKey) error {
		if !bytes.Equal(got.Marshal(), want.Marshal()) {
			return fmt.Errorf("sshca: %s presented host key %s, expected %s", host, ssh.FingerprintSHA256(got), ssh.FingerprintSHA256(want))
		}
		return nil
	}
}

// CAHostKey accepts only host certificates signed by ca for the dialled name,
// the way an admin's client with the @cert-authority line does.
func CAHostKey(ca ssh.PublicKey) ssh.HostKeyCallback {
	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, _ string) bool { return bytes.Equal(auth.Marshal(), ca.Marshal()) },
	}
	return checker.CheckHostKey
}

// Run executes command on addr (host:port) as user, authenticating with
// signer, verifying the server with hostKey, feeding stdin, and returns the
// command's output.
func Run(addr, user string, signer ssh.Signer, hostKey ssh.HostKeyCallback, algos []string, command, stdin string) (string, error) {
	cfg := &ssh.ClientConfig{
		User:              user,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   hostKey,
		HostKeyAlgorithms: algos,
		Timeout:           15 * time.Second,
	}
	c, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return "", err
	}
	defer c.Close()
	s, err := c.NewSession()
	if err != nil {
		return "", err
	}
	defer s.Close()
	s.Stdin = strings.NewReader(stdin)
	out, err := s.CombinedOutput(command)
	return string(out), err
}

// CertAlgos makes the client ask for the ed25519 host certificate.
var CertAlgos = []string{ssh.CertAlgoED25519v01}

// PlainAlgos makes the client ask for the plain ed25519 host key.
var PlainAlgos = []string{ssh.KeyAlgoED25519}
