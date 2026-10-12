package image

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"winslot/assets"
	"winslot/internal/config"
	"winslot/internal/payload"
	"winslot/internal/run"
)

// virtio-win.iso paths Windows needs: Setup loads the storage/network drivers
// in windowsPE; provision.ps1 installs the full driver set and the guest agent.
var virtioDrivers = map[string]string{ // ISO path -> WINCFG drivers/ subdirectory
	"vioscsi/w11/amd64": "vioscsi",
	"viostor/w11/amd64": "viostor",
	"NetKVM/w11/amd64":  "netkvm",
}
var virtioMSIs = []string{"virtio-win-gt-x64.msi", "guest-agent/qemu-ga-x86_64.msi"}

const virtioISO = "virtio-win.iso"

// Mounter gives access to an image's Windows partition (disk.Tools).
type Mounter interface {
	WithWindows(ctx context.Context, image string, readOnly bool, fn func(root string) error) error
}

// Builder builds and imports golden images.
type Builder struct {
	Cfg   config.Config
	R     run.Runner
	Disk  Mounter
	Out   io.Writer
	Now   func() time.Time // defaults to time.Now
	Items []payload.Item   // defaults to the embedded payload.list
}

func (b Builder) logf(format string, args ...any) {
	if b.Out != nil {
		fmt.Fprintf(b.Out, format+"\n", args...)
	}
}

func (b Builder) items() ([]payload.Item, error) {
	if b.Items != nil {
		return b.Items, nil
	}
	f, err := assets.FS.Open("packer/payload.list")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return payload.ParseList(f)
}

// Build runs an unattended image build and returns the path of the new qcow2.
func (b Builder) Build(ctx context.Context) (string, error) {
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	name := "win11-" + now().Format("20060102-1504")
	dir := filepath.Join(b.Cfg.Storage.Data, "builds", name)
	stage := filepath.Join(dir, "wincfg")
	cache := filepath.Join(b.Cfg.Storage.Cache, "payload")
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("image: build directory %s already exists", dir)
	}

	b.logf("== 1. payload (%s)", cache)
	items, err := b.items()
	if err != nil {
		return "", err
	}
	if err := (&payload.Fetcher{Dir: cache, Log: b.logf}).Ensure(ctx, items); err != nil {
		return "", err
	}

	b.logf("== 2. stage WINCFG (%s)", stage)
	if err := b.stage(ctx, name, dir, stage, cache, items); err != nil {
		return "", err
	}

	b.logf("== 3. packer build (guest progress: tail -f %s)", filepath.Join(dir, "out", "serial.log"))
	isoSHA, err := payload.FileSHA256(b.Cfg.Windows.ISO)
	if err != nil {
		return "", err
	}
	tmpl, err := assets.FS.ReadFile("packer/windows11.pkr.hcl")
	if err != nil {
		return "", err
	}
	tmplPath := filepath.Join(dir, "windows11.pkr.hcl")
	if err := os.WriteFile(tmplPath, tmpl, 0o644); err != nil {
		return "", err
	}
	if err := b.R.Stream(ctx, b.Out, "packer", "init", tmplPath); err != nil {
		return "", err
	}
	if err := b.R.Stream(ctx, b.Out, "packer", "build",
		"-var", "iso="+b.Cfg.Windows.ISO, "-var", "iso_sha256="+isoSHA,
		"-var", "wincfg="+stage, "-var", "output_dir="+filepath.Join(dir, "out"),
		"-var", "name="+name, tmplPath); err != nil {
		return "", err
	}
	return filepath.Join(dir, "out", name+".qcow2"), nil
}

func (b Builder) stage(ctx context.Context, name, dir, stage, cache string, items []payload.Item) error {
	pay := filepath.Join(stage, "payload")
	if err := os.MkdirAll(pay, 0o755); err != nil {
		return err
	}
	for _, f := range assets.WINCFG {
		data, err := assets.FS.ReadFile("answer/" + f)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, f), data, 0o644); err != nil {
			return err
		}
	}
	keys, err := os.ReadFile(filepath.Join(b.Cfg.Storage.Etc, "authorized_keys"))
	if err != nil {
		return fmt.Errorf("image: admin keys: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stage, "authorized_keys"), keys, 0o644); err != nil {
		return err
	}
	stamp := fmt.Sprintf("name=%s\nbuilt=%s\niso=%s\nedition=%s\n", name, time.Now().Format(time.RFC3339), filepath.Base(b.Cfg.Windows.ISO), b.Cfg.Windows.Edition)
	if err := os.WriteFile(filepath.Join(stage, "BUILD"), []byte(stamp), 0o644); err != nil {
		return err
	}
	var manifest bytes.Buffer
	for _, it := range items {
		fmt.Fprintf(&manifest, "%s %s %s\n", it.Name, it.SHA256, it.URL)
		if it.Name == virtioISO {
			continue
		}
		if err := copyFile(filepath.Join(cache, it.Name), filepath.Join(pay, it.Name)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(pay, "MANIFEST"), manifest.Bytes(), 0o644); err != nil {
		return err
	}

	// Drivers and guest tools come from the pinned virtio-win ISO.
	virtio := filepath.Join(dir, "virtio")
	args := []string{"x", "-y", "-bso0", "-o" + virtio, filepath.Join(cache, virtioISO)}
	for src := range virtioDrivers {
		args = append(args, src)
	}
	args = append(args, virtioMSIs...)
	if _, err := b.R.Run(ctx, "7z", args...); err != nil {
		return err
	}
	for src, dst := range virtioDrivers {
		if err := os.CopyFS(filepath.Join(stage, "drivers", dst), os.DirFS(filepath.Join(virtio, filepath.FromSlash(src)))); err != nil {
			return fmt.Errorf("image: virtio %s: %w", src, err)
		}
	}
	for _, msi := range virtioMSIs {
		if err := copyFile(filepath.Join(virtio, filepath.FromSlash(msi)), filepath.Join(pay, filepath.Base(msi))); err != nil {
			return err
		}
	}
	return nil
}

// Import verifies a built image offline and stores it as a read-only golden
// image. It returns the image name.
func (b Builder) Import(ctx context.Context, img string) (string, error) {
	name := strings.TrimSuffix(filepath.Base(img), ".qcow2")
	if !ValidName(name) {
		return "", fmt.Errorf("image: %q is not a golden image name (win11-YYYYMMDD-HHMM)", name)
	}
	images := filepath.Join(b.Cfg.Storage.Data, "images")
	dest := filepath.Join(images, name+".qcow2")
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("image: %s already exists", dest)
	}
	if err := os.MkdirAll(images, 0o755); err != nil {
		return "", err
	}
	b.logf("== verify %s (read-only)", img)
	err := b.Disk.WithWindows(ctx, img, true, func(root string) error {
		if err := Verify(root); err != nil {
			return fmt.Errorf("image NOT imported:\n%w", err)
		}
		for src, dst := range map[string]string{"ProgramData/golden/BUILD": ".build.txt", "ProgramData/golden/provision.log": ".provision.log"} {
			if err := copyFile(filepath.Join(root, filepath.FromSlash(src)), filepath.Join(images, name+dst)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if err := os.Rename(img, dest); err != nil {
		return "", err
	}
	if err := os.Chmod(dest, 0o444); err != nil {
		return "", err
	}
	b.logf("imported %s: new VMs and recreates now use it", name)
	return name, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
