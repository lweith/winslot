package console

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeHost is a shadow file whose root hash changes when the password is set.
type fakeHost struct {
	t      *testing.T
	p      Paths
	set    []string
	failOn bool
}

func newHost(t *testing.T) *fakeHost {
	dir := t.TempDir()
	h := &fakeHost{t: t, p: Paths{
		State:  filepath.Join(dir, "etc/winslot/console-root.toml"),
		Shadow: filepath.Join(dir, "shadow"),
		SSHDir: filepath.Join(dir, "ssh"),
		HostCA: filepath.Join(dir, "etc/winslot/host_ca.pub"),
		Issue:  filepath.Join(dir, "issue.d/40-winslot.issue"),
	}}
	h.shadow("!")
	os.MkdirAll(h.p.SSHDir, 0o755)
	h.key(filepath.Join(h.p.SSHDir, "ssh_host_ed25519_key.pub"))
	return h
}

func (h *fakeHost) shadow(hash string) {
	data := "daemon:*:20000:0:99999:7:::\nroot:" + hash + ":20000:0:99999:7:::\n"
	if err := os.WriteFile(h.p.Shadow, []byte(data), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fakeHost) key(path string) ssh.PublicKey {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k, _ := ssh.NewPublicKey(pub)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, ssh.MarshalAuthorizedKey(k), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return k
}

func (h *fakeHost) setPassword(pw string) error {
	if h.failOn {
		return errors.New("chpasswd failed")
	}
	h.set = append(h.set, pw)
	h.shadow("$6$salt$hash-of-" + pw)
	return nil
}

func (h *fakeHost) issue() string {
	b, err := os.ReadFile(h.p.Issue)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

var now = time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)

func TestNewPassword(t *testing.T) {
	re := regexp.MustCompile(`^[` + alphabet + `]{4}(-[` + alphabet + `]{4}){4}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw, err := NewPassword()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(pw) {
			t.Fatalf("password %q", pw)
		}
		if seen[pw] {
			t.Fatalf("repeated password %q", pw)
		}
		seen[pw] = true
	}
}

func TestFirstRunSetsPasswordAndShowsIt(t *testing.T) {
	h := newHost(t)
	ca := h.key(h.p.HostCA)
	res, err := Update(h.p, false, now, h.setPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Generated || !res.Known || res.Keys != 1 || len(h.set) != 1 {
		t.Fatalf("result %+v, set %v", res, h.set)
	}
	issue := h.issue()
	for _, want := range []string{"root password  " + h.set[0], `SSH host keys of \n:`, "ED25519", ssh.FingerprintSHA256(ca)} {
		if !strings.Contains(issue, want) {
			t.Errorf("issue lacks %q:\n%s", want, issue)
		}
	}
	for _, f := range []string{h.p.State, h.p.Issue} {
		if st, _ := os.Stat(f); st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", f, st.Mode().Perm())
		}
	}
}

func TestLaterRunsKeepPasswordAndFollowKeys(t *testing.T) {
	h := newHost(t)
	if _, err := Update(h.p, false, now, h.setPassword); err != nil {
		t.Fatal(err)
	}
	ecdsa := h.key(filepath.Join(h.p.SSHDir, "ssh_host_ecdsa_key.pub")) // stands in for a regenerated key
	res, err := Update(h.p, false, now.Add(time.Hour), h.setPassword)
	if err != nil {
		t.Fatal(err)
	}
	if res.Generated || !res.Known || res.Keys != 2 || len(h.set) != 1 {
		t.Fatalf("result %+v, set %v", res, h.set)
	}
	if issue := h.issue(); !strings.Contains(issue, h.set[0]) || !strings.Contains(issue, ssh.FingerprintSHA256(ecdsa)) {
		t.Errorf("issue:\n%s", issue)
	}
}

func TestPasswordChangedByHandIsNotShown(t *testing.T) {
	h := newHost(t)
	if _, err := Update(h.p, false, now, h.setPassword); err != nil {
		t.Fatal(err)
	}
	h.shadow("$y$someone$else")
	res, err := Update(h.p, false, now, h.setPassword)
	if err != nil {
		t.Fatal(err)
	}
	issue := h.issue()
	if res.Known || strings.Contains(issue, h.set[0]) || !strings.Contains(issue, "not known here") {
		t.Fatalf("result %+v, issue:\n%s", res, issue)
	}

	res, err = Update(h.p, true, now, h.setPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Generated || !res.Known || len(h.set) != 2 || !strings.Contains(h.issue(), h.set[1]) {
		t.Fatalf("regenerate: result %+v, set %v", res, h.set)
	}
}

func TestFailedChpasswdWritesNothing(t *testing.T) {
	h := newHost(t)
	h.failOn = true
	if _, err := Update(h.p, false, now, h.setPassword); err == nil {
		t.Fatal("expected an error")
	}
	for _, f := range []string{h.p.State, h.p.Issue} {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after a failed chpasswd", f)
		}
	}
}

func TestRenderEscapesBackslash(t *testing.T) {
	out := Render(State{Password: `a\b`}, true, nil, nil)
	if !strings.Contains(out, `a\\b`) || !strings.Contains(out, "(none found") {
		t.Errorf("render:\n%s", out)
	}
}
