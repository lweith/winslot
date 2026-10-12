package vm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"winslot/internal/hostkeys"
	"winslot/internal/sshca"
)

// Certifier is the network side of certification (SSH and DNS), separated so
// the sequence can be tested without a VM.
type Certifier interface {
	// Resolve waits until host resolves.
	Resolve(ctx context.Context, host string) error
	// Run executes command on host:22 as admin with the management key.
	Run(host string, mgmt ssh.Signer, hostKey ssh.HostKeyCallback, algos []string, command, stdin string) (string, error)
}

// NetCertifier is the real Certifier.
type NetCertifier struct{}

// Resolve polls DNS (DHCP registers the VM's name a few seconds after boot).
func (NetCertifier) Resolve(ctx context.Context, host string) error {
	for {
		if _, err := net.DefaultResolver.LookupHost(ctx, host); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s does not resolve: %w", host, ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
}

// Run runs over SSH.
func (NetCertifier) Run(host string, mgmt ssh.Signer, hostKey ssh.HostKeyCallback, algos []string, command, stdin string) (string, error) {
	return sshca.Run(net.JoinHostPort(host, "22"), "admin", mgmt, hostKey, algos, command, stdin)
}

// HostKeyWait bounds how long Certify waits for first boot to report host keys.
var HostKeyWait = 20 * time.Minute

// pollInterval is how often the serial log is re-read.
var pollInterval = 5 * time.Second

// installCert runs in the VM (PowerShell 7 is the SSH shell): write the
// certificate, point sshd at it above the trailing "Match Group
// administrators" block (sshd ignores it below), restart sshd.
const installCert = `$d = "C:\ProgramData\ssh"
[IO.File]::WriteAllText("$d\ssh_host_ed25519_key-cert.pub", (@($input) -join "` + "`n" + `") + "` + "`n" + `")
$cfg = Get-Content "$d\sshd_config"
if (-not ($cfg -match "^HostCertificate")) {
  [IO.File]::WriteAllLines("$d\sshd_config", @("HostCertificate __PROGRAMDATA__/ssh/ssh_host_ed25519_key-cert.pub") + $cfg) }
Write-Output "CERT-INSTALLED"
Restart-Service sshd`

// Certify records the VM's host keys from its serial port, gives it a host
// certificate, and checks it is reachable the way admins will reach it.
func (m *Manager) Certify(ctx context.Context, name string) error {
	fqdn := m.FQDN(name)
	etc := m.Cfg.Storage.Etc

	m.logf("== host keys: waiting for %s's first boot to report them on COM1", name)
	keys, err := waitHostKeys(ctx, m.SerialLog(name), name)
	if err != nil {
		return err
	}
	if err := ReplaceKnownHosts(filepath.Join(etc, "known_hosts"), fqdn, keys); err != nil {
		return err
	}
	for _, k := range keys {
		m.logf("   %s %s", k.Type(), ssh.FingerprintSHA256(k))
	}
	hostKey := hostkeys.ED25519(keys)
	if hostKey == nil {
		return fmt.Errorf("%s reported no ed25519 host key", name)
	}

	m.logf("== certificate: sign and install")
	ca, err := sshca.LoadSigner(filepath.Join(etc, "host_ca"))
	if err != nil {
		return err
	}
	mgmt, err := sshca.LoadSigner(filepath.Join(etc, "mgmt_key"))
	if err != nil {
		return err
	}
	cert, err := sshca.SignHost(ca, hostKey, name+"-"+m.now().Format("2006-01-02"), fqdn)
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := m.Certifier.Resolve(rctx, fqdn); err != nil {
		return err
	}
	out, err := m.Certifier.Run(fqdn, mgmt, sshca.PinnedHostKey(hostKey), sshca.PlainAlgos, installCert, string(ssh.MarshalAuthorizedKey(cert)))
	// Restarting sshd may end the session before a clean exit status arrives;
	// the marker shows the certificate was written.
	if !strings.Contains(out, "CERT-INSTALLED") {
		return fmt.Errorf("installing the host certificate on %s: %v: %s", name, err, strings.TrimSpace(out))
	}

	m.logf("== readiness check: connect trusting only the CA")
	var got string
	for i := 0; i < 10; i++ { // sshd is restarting
		got, err = m.Certifier.Run(fqdn, mgmt, sshca.CAHostKey(ca.PublicKey()), sshca.CertAlgos, "hostname", "")
		if err == nil {
			break
		}
		time.Sleep(pollInterval / 2)
	}
	if err != nil || !strings.EqualFold(strings.TrimSpace(got), name) {
		return fmt.Errorf("NOT READY: readiness check for %s failed: %v %q", fqdn, err, got)
	}
	m.logf("READY: %s (CA-verified, sshd up, keys in place)", fqdn)
	return nil
}

func waitHostKeys(ctx context.Context, logPath, name string) ([]ssh.PublicKey, error) {
	deadline := time.Now().Add(HostKeyWait)
	for {
		if f, err := os.Open(logPath); err == nil {
			keys, perr := hostkeys.Parse(f, name)
			f.Close()
			if perr != nil {
				return nil, perr
			}
			if len(keys) > 0 {
				return keys, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no HOSTKEY lines from %s after %v; check its console (virsh vncdisplay %s)", name, HostKeyWait, name)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// ReplaceKnownHosts sets host's lines in a known_hosts file to keys.
func ReplaceKnownHosts(path, host string, keys []ssh.PublicKey) error {
	lines, err := otherHosts(path, host)
	if err != nil {
		return err
	}
	lines = append(lines, hostkeys.KnownHostsLines(host, keys)...)
	return writeLines(path, lines)
}

// RemoveKnownHosts deletes host's lines from a known_hosts file.
func RemoveKnownHosts(path, host string) error {
	lines, err := otherHosts(path, host)
	if err != nil {
		return err
	}
	return writeLines(path, lines)
}

func otherHosts(path, host string) ([]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var keep []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); strings.TrimSpace(line) != "" && strings.Fields(line)[0] != host {
			keep = append(keep, line)
		}
	}
	return keep, sc.Err()
}

func writeLines(path string, lines []string) error {
	tmp := path + ".tmp"
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SRVRecord is the libvirt <srv> element that advertises name's
// windows_exporter for Prometheus DNS service discovery:
//
//	dns_sd_configs: [{names: ['_windows-exporter._tcp.<zone>']}]
func (m *Manager) SRVRecord(name string) string {
	return fmt.Sprintf(`<srv service='windows-exporter' protocol='tcp' domain='%s' target='%s' port='9182' priority='10' weight='10'/>`,
		m.Cfg.Network.Zone, m.FQDN(name))
}

// PublishSRV adds name's SRV record (idempotent).
func (m *Manager) PublishSRV(name string) error {
	_ = m.Virt.DeleteDNSSRV(m.Cfg.Network.Libvirt, m.SRVRecord(name)) // absent is fine
	if err := m.Virt.AddDNSSRV(m.Cfg.Network.Libvirt, m.SRVRecord(name)); err != nil {
		return fmt.Errorf("publishing %s for Prometheus: %w", name, err)
	}
	m.logf("== prometheus: _windows-exporter._tcp.%s -> %s:9182", m.Cfg.Network.Zone, m.FQDN(name))
	return nil
}

// WithdrawSRV removes name's SRV record (absent is fine).
func (m *Manager) WithdrawSRV(name string) error {
	_ = m.Virt.DeleteDNSSRV(m.Cfg.Network.Libvirt, m.SRVRecord(name))
	return nil
}
