package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseVirtFeature(t *testing.T) {
	for in, want := range map[string]string{
		"processor\t: 0\nflags\t\t: fpu vme vmx sse2\n":         "vmx",
		"flags\t\t: fpu svm sse2\nflags\t\t: fpu svm\n":         "svm",
		"model name\t: x\nflags\t\t: fpu vme sse2 hypervisor\n": "",
	} {
		got, err := parseVirtFeature(strings.NewReader(in))
		if got != want || (want == "") != (err != nil) {
			t.Errorf("parseVirtFeature(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func execCLI(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestCommandTree(t *testing.T) {
	out, err := execCLI(t, "", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"vm", "image", "payload", "completion", "version"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("help lacks %s", cmd)
		}
	}
	out, _ = execCLI(t, "", "vm", "--help")
	for _, cmd := range []string{"create", "recreate", "decommission", "list", "certify"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("vm help lacks %s", cmd)
		}
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out, err := execCLI(t, "", "completion", shell)
		if err != nil || !strings.Contains(out, "winslot") {
			t.Errorf("%s completion: err=%v len=%d", shell, err, len(out))
		}
	}
}

func TestArgumentValidation(t *testing.T) {
	for _, args := range [][]string{
		{"vm", "create"},                        // NAME missing
		{"vm", "create", "a", "b"},              // too many
		{"vm", "create", "x", "--disk", "lots"}, // bad size
		{"vm", "list", "extra"},
		{"image", "build", "extra"},
	} {
		if _, err := execCLI(t, "", args...); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
}

func TestConfigErrorsSurface(t *testing.T) {
	_, err := execCLI(t, "", "--config", "/nonexistent/winslot.toml", "vm", "list")
	if err == nil || !strings.Contains(err.Error(), "/nonexistent/winslot.toml") {
		t.Errorf("err = %v", err)
	}
}
