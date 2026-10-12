package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T, files []string) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	return root
}

func TestVerifyGoodImage(t *testing.T) {
	if err := Verify(tree(t, MustExist)); err != nil {
		t.Errorf("good image: %v", err)
	}
}

func TestVerifyReportsEveryProblem(t *testing.T) {
	files := append([]string{}, MustExist[1:]...) // no Sysprep_succeeded.tag
	files = append(files, MustNotExist...)        // leftover answer file and host key
	err := Verify(tree(t, files))
	if err == nil {
		t.Fatal("bad image passed")
	}
	for _, want := range []string{"missing Windows/System32/Sysprep/Sysprep_succeeded.tag", "present Windows/Panther/unattend.xml", "present ProgramData/ssh/ssh_host_ed25519_key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestLatest(t *testing.T) {
	dir := tree(t, []string{"win11-20261010-1615.qcow2", "win11-20261011-0353.qcow2", "win11-20261009-2359.qcow2", "notes.qcow2", "win11-20261011-0353.build.txt"})
	name, path, err := Latest(dir)
	if err != nil || name != "win11-20261011-0353" || path != filepath.Join(dir, "win11-20261011-0353.qcow2") {
		t.Errorf("Latest = %q %q %v", name, path, err)
	}
	if _, _, err := Latest(t.TempDir()); err == nil {
		t.Error("empty dir: want error")
	}
}

func TestValidName(t *testing.T) {
	for n, ok := range map[string]bool{"win11-20261011-0353": true, "win11-2026101-0353": false, "win10-20261011-0353": false, "win11-20261011-0353.qcow2": false} {
		if ValidName(n) != ok {
			t.Errorf("ValidName(%q) != %v", n, ok)
		}
	}
}
