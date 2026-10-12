// Package image builds, verifies and stores golden Windows images.
//
// A golden image is the output of an unattended, offline install: Windows
// Setup, Audit Mode, answer/provision.ps1, Sysprep, power off. It is verified
// offline before it is stored, stored read-only, and never modified: newer
// software means a new image.
package image

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// MustExist are files a correctly sealed image contains (paths relative to C:).
var MustExist = []string{
	"Windows/System32/Sysprep/Sysprep_succeeded.tag",
	"ProgramData/golden/BUILD",
	"ProgramData/ssh/administrators_authorized_keys",
	"Windows/Setup/Scripts/SetupComplete.cmd",
	"Windows/Setup/Scripts/firstboot.ps1",
	"Windows/Web/Wallpaper/TMRA/tmra.jpg",
	"Windows/Resources/Themes/tmra.theme",
	"Program Files/Mozilla Firefox/firefox.exe",
	"Program Files/teraterm5/ttermpro.exe",
	"Program Files/7-Zip/7z.exe",
	"Program Files/Notepad++/notepad++.exe",
	"Program Files/PowerShell/7/pwsh.exe",
	"Program Files/Git/cmd/git.exe",
	"Program Files/windows_exporter/windows_exporter.exe",
	"Program Files/OpenSSH/sshd.exe",
	"Program Files/Qemu-ga/qemu-ga.exe",
	"Tools/Sysinternals/procexp64.exe",
}

// MustNotExist are files whose presence means the image is unsafe to clone.
var MustNotExist = []string{
	"Windows/Panther/unattend.xml",         // the build answer file (Reseal=Audit) would send clones back to Audit Mode
	"ProgramData/ssh/ssh_host_ed25519_key", // host keys must be generated per VM
}

// Verify checks a mounted image root (case-insensitive mount) and returns
// every problem found, or nil.
func Verify(root string) error {
	var errs []error
	for _, p := range MustExist {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			errs = append(errs, fmt.Errorf("missing %s", p))
		}
	}
	for _, p := range MustNotExist {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			errs = append(errs, fmt.Errorf("present %s", p))
		}
	}
	return errors.Join(errs...)
}

// nameRE matches golden image names: win11-YYYYMMDD-HHMM.
var nameRE = regexp.MustCompile(`^win11-\d{8}-\d{4}$`)

// ValidName reports whether name is a golden image name.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// Latest returns the newest golden image in dir (names sort chronologically).
func Latest(dir string) (name, path string, err error) {
	matches, err := filepath.Glob(filepath.Join(dir, "win11-*.qcow2"))
	if err != nil {
		return "", "", err
	}
	var names []string
	for _, m := range matches {
		if n := strings.TrimSuffix(filepath.Base(m), ".qcow2"); ValidName(n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return "", "", fmt.Errorf("no golden image in %s (run: winslot image build)", dir)
	}
	sort.Strings(names)
	name = names[len(names)-1]
	return name, filepath.Join(dir, name+".qcow2"), nil
}
