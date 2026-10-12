package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winslot/internal/config"
	"winslot/internal/payload"
	"winslot/internal/run"
)

var fixedNow = func() time.Time { return time.Date(2026, 10, 11, 4, 0, 0, 0, time.UTC) }

func put(t *testing.T, path, content string) string {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

func testConfig(t *testing.T) config.Config {
	base := t.TempDir()
	cfg := config.Defaults()
	cfg.Storage = config.Storage{Data: filepath.Join(base, "data"), Cache: filepath.Join(base, "cache"), Etc: filepath.Join(base, "etc")}
	cfg.Windows.ISO = filepath.Join(base, "win11.iso")
	put(t, cfg.Windows.ISO, "windows iso")
	put(t, filepath.Join(cfg.Storage.Etc, "authorized_keys"), "ssh-ed25519 AAAA admin\n")
	return cfg
}

func TestBuildStagesAndRunsPacker(t *testing.T) {
	cfg := testConfig(t)
	cache := filepath.Join(cfg.Storage.Cache, "payload")
	items := []payload.Item{
		{Name: "firefox.msi", SHA256: put(t, filepath.Join(cache, "firefox.msi"), "ff"), URL: "https://example.org/ff"},
		{Name: virtioISO, SHA256: put(t, filepath.Join(cache, virtioISO), "iso"), URL: "https://example.org/virtio"},
	}
	virtio := filepath.Join(cfg.Storage.Data, "builds", "win11-20261011-0400", "virtio")
	rec := &sevenZip{t: t}
	var out bytes.Buffer
	img, err := Builder{Cfg: cfg, R: rec, Out: &out, Now: fixedNow, Items: items}.Build(context.Background())
	if err != nil {
		t.Fatalf("Build: %v\n%s", err, out.String())
	}
	dir := filepath.Join(cfg.Storage.Data, "builds", "win11-20261011-0400")
	if img != filepath.Join(dir, "out", "win11-20261011-0400.qcow2") {
		t.Errorf("image path %s", img)
	}
	stage := filepath.Join(dir, "wincfg")
	for _, f := range []string{"autounattend.xml", "provision.ps1", "firstboot.ps1", "SetupComplete.cmd", "wallpaper.jpg",
		"authorized_keys", "BUILD", "payload/firefox.msi", "payload/MANIFEST", "payload/virtio-win-gt-x64.msi",
		"payload/qemu-ga-x86_64.msi", "drivers/vioscsi/driver.inf", "drivers/viostor/driver.inf", "drivers/netkvm/driver.inf"} {
		if _, err := os.Stat(filepath.Join(stage, f)); err != nil {
			t.Errorf("WINCFG lacks %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(stage, "payload", virtioISO)); err == nil {
		t.Error("the virtio ISO itself was staged (only its drivers belong on WINCFG)")
	}
	cmds := strings.Join(rec.Commands, "\n")
	for _, want := range []string{"7z x -y -bso0 -o" + virtio, "packer init " + dir + "/windows11.pkr.hcl",
		"packer build -var iso=" + cfg.Windows.ISO, "-var name=win11-20261011-0400"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("commands lack %q:\n%s", want, cmds)
		}
	}
}

func TestBuildRefusesUnverifiedPayload(t *testing.T) {
	cfg := testConfig(t)
	cache := filepath.Join(cfg.Storage.Cache, "payload")
	put(t, filepath.Join(cache, "firefox.msi"), "tampered")
	items := []payload.Item{{Name: "firefox.msi", SHA256: strings.Repeat("0", 64), URL: "https://example.org/ff"}}
	rec := &run.Recorder{}
	if _, err := (Builder{Cfg: cfg, R: rec, Now: fixedNow, Items: items}).Build(context.Background()); err == nil {
		t.Fatal("build with a tampered payload succeeded")
	}
	if len(rec.Commands) != 0 {
		t.Errorf("commands ran after verification failed: %v", rec.Commands)
	}
}

// sevenZip records commands and, when 7z runs, produces what it would extract.
type sevenZip struct {
	run.Recorder
	t *testing.T
}

func (s *sevenZip) Run(ctx context.Context, name string, args ...string) (string, error) {
	if name == "7z" {
		out := strings.TrimPrefix(args[3], "-o")
		for src := range virtioDrivers {
			put(s.t, filepath.Join(out, src, "driver.inf"), src)
		}
		for _, msi := range virtioMSIs {
			put(s.t, filepath.Join(out, msi), msi)
		}
	}
	return s.Recorder.Run(ctx, name, args...)
}

// fakeMount presents a prepared directory as the image's Windows partition.
type fakeMount struct{ root string }

func (f fakeMount) WithWindows(_ context.Context, _ string, readOnly bool, fn func(string) error) error {
	if !readOnly {
		return errors.New("import must mount read-only")
	}
	return fn(f.root)
}

func TestImport(t *testing.T) {
	cfg := testConfig(t)
	built := filepath.Join(t.TempDir(), "win11-20261011-0400.qcow2")
	put(t, built, "qcow2")

	good := tree(t, MustExist)
	put(t, filepath.Join(good, "ProgramData/golden/BUILD"), "name=win11-20261011-0400\n")
	name, err := Builder{Cfg: cfg, Disk: fakeMount{good}}.Import(context.Background(), built)
	if err != nil || name != "win11-20261011-0400" {
		t.Fatalf("Import = %q, %v", name, err)
	}
	dest := filepath.Join(cfg.Storage.Data, "images", name+".qcow2")
	if st, err := os.Stat(dest); err != nil || st.Mode().Perm() != 0o444 {
		t.Errorf("stored image: %v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(cfg.Storage.Data, "images", name+".build.txt")); !strings.Contains(string(b), "win11-20261011-0400") {
		t.Error("BUILD stamp not kept")
	}
}

func TestImportRejectsBadImage(t *testing.T) {
	cfg := testConfig(t)
	built := filepath.Join(t.TempDir(), "win11-20261011-0400.qcow2")
	put(t, built, "qcow2")
	bad := tree(t, append(MustExist, "Windows/Panther/unattend.xml"))
	if _, err := (Builder{Cfg: cfg, Disk: fakeMount{bad}}).Import(context.Background(), built); err == nil || !strings.Contains(err.Error(), "NOT imported") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(built); err != nil {
		t.Error("rejected image was moved")
	}
	if _, err := (Builder{Cfg: cfg, Disk: fakeMount{bad}}).Import(context.Background(), "/x/notes.qcow2"); err == nil {
		t.Error("bad image name accepted")
	}
}
