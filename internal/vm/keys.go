package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"winslot/internal/sshca"
)

// installKeys runs in the VM: replace administrators_authorized_keys (from
// stdin) and keep the ACL OpenSSH requires (Administrators and SYSTEM only).
// The file may carry the ReadOnly attribute from the image's CD.
const installKeys = `$k = "C:\ProgramData\ssh\administrators_authorized_keys"
if (Test-Path $k) { Set-ItemProperty $k -Name IsReadOnly -Value $false }
[IO.File]::WriteAllLines($k, @($input))
icacls.exe $k /inheritance:r /grant "*S-1-5-32-544:F" /grant "*S-1-5-18:F" | Out-Null
Write-Output ("KEYS-INSTALLED " + (Get-Content $k | Measure-Object).Count)`

// PushKeys installs the host's admin key list (/etc/winslot/authorized_keys,
// the same list new images are built with) on a running VM, over SSH
// verified with the host CA.
func (m *Manager) PushKeys(ctx context.Context, name string) error {
	if _, err := m.Slot(name); err != nil {
		return err
	}
	etc := m.Cfg.Storage.Etc
	keys, err := os.ReadFile(filepath.Join(etc, "authorized_keys"))
	if err != nil {
		return err
	}
	if !strings.Contains(string(keys), "winslot-mgmt") {
		// Pushing a list without the management key would lock winslot out.
		return fmt.Errorf("%s lacks the winslot-mgmt line; run: winslot host init", filepath.Join(etc, "authorized_keys"))
	}
	ca, err := sshca.LoadSigner(filepath.Join(etc, "host_ca"))
	if err != nil {
		return err
	}
	mgmt, err := sshca.LoadSigner(filepath.Join(etc, "mgmt_key"))
	if err != nil {
		return err
	}
	out, err := m.Certifier.Run(m.FQDN(name), mgmt, sshca.CAHostKey(ca.PublicKey()), sshca.CertAlgos, installKeys, string(keys))
	if err != nil || !strings.Contains(out, "KEYS-INSTALLED") {
		return fmt.Errorf("pushing keys to %s: %v: %s", name, err, strings.TrimSpace(out))
	}
	m.logf("%s: %s", name, strings.TrimSpace(out[strings.Index(out, "KEYS-INSTALLED"):]))
	return nil
}
