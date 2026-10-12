// Package disk prepares VM disks: an independent qcow2 copy of a golden image,
// grown to the slot's size, with files written into Windows' C: offline.
package disk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"winslot/internal/run"
)

// windowsPartition is C: in the layout autounattend.xml creates (EFI, MSR, Windows).
const windowsPartition = 3

// Tools runs the disk utilities.
type Tools struct {
	R run.Runner
	// SysBlock is /sys/block (overridable in tests) for finding a free nbd device.
	SysBlock string
}

// CopyGolden makes dst an independent qcow2 copy of golden (no backing file)
// and grows it to sizeBytes. Windows extends C: on first boot.
func (t Tools) CopyGolden(ctx context.Context, golden, dst string, sizeBytes uint64) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("disk: %s already exists", dst)
	}
	if _, err := t.R.Run(ctx, "qemu-img", "convert", "-O", "qcow2", golden, dst); err != nil {
		os.Remove(dst)
		return err
	}
	// qemu-img refuses to shrink without --shrink: a size below the golden
	// image's is an error, not data loss.
	if _, err := t.R.Run(ctx, "qemu-img", "resize", "-q", dst, strconv.FormatUint(sizeBytes, 10)); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}

// WithWindows attaches image with qemu-nbd, mounts its Windows partition
// (ntfs3, case-insensitive lookups like Windows), runs fn on the mount point,
// and always unmounts and detaches, even when fn fails.
func (t Tools) WithWindows(ctx context.Context, image string, readOnly bool, fn func(root string) error) (err error) {
	if _, err := t.R.Run(ctx, "modprobe", "nbd", "max_part=8"); err != nil {
		return err
	}
	dev, err := t.freeNBD()
	if err != nil {
		return err
	}
	attach := []string{"--connect", dev, image}
	opts := "nocase"
	if readOnly {
		attach = append([]string{"--read-only"}, attach...)
		opts = "ro,nocase"
	}
	if _, err := t.R.Run(ctx, "qemu-nbd", attach...); err != nil {
		return err
	}
	defer func() {
		if _, derr := t.R.Run(context.WithoutCancel(ctx), "qemu-nbd", "--disconnect", dev); derr != nil && err == nil {
			err = derr
		}
	}()
	if _, err := t.R.Run(ctx, "udevadm", "settle"); err != nil {
		return err
	}
	mnt, err := os.MkdirTemp("", "winslot-mnt-")
	if err != nil {
		return err
	}
	defer os.Remove(mnt)
	part := fmt.Sprintf("%sp%d", dev, windowsPartition)
	if _, err := t.R.Run(ctx, "mount", "-t", "ntfs3", "-o", opts, part, mnt); err != nil {
		return err
	}
	defer func() {
		if _, uerr := t.R.Run(context.WithoutCancel(ctx), "umount", mnt); uerr != nil && err == nil {
			err = uerr
		}
	}()
	return fn(mnt)
}

// WriteFile writes content to winPath (e.g. `Windows\Panther\Unattend\Unattend.xml`)
// inside image's C:, creating directories as needed.
func (t Tools) WriteFile(ctx context.Context, image, winPath string, content []byte) error {
	return t.WithWindows(ctx, image, false, func(root string) error {
		return t.writeAt(root, winPath, content)
	})
}

// writeAt writes content to the Windows path winPath under the mounted root.
func (Tools) writeAt(root, winPath string, content []byte) error {
	p := filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(winPath, `\`, "/")))
	if !strings.HasPrefix(p, filepath.Clean(root)+string(filepath.Separator)) {
		return fmt.Errorf("disk: %q escapes the Windows partition", winPath)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, content, 0o600)
}

// freeNBD returns the first /dev/nbdN not in use (its size in sysfs is 0).
func (t Tools) freeNBD() (string, error) {
	sys := t.SysBlock
	if sys == "" {
		sys = "/sys/block"
	}
	for i := 0; i < 16; i++ {
		b, err := os.ReadFile(filepath.Join(sys, fmt.Sprintf("nbd%d", i), "size"))
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(string(b)) == "0" {
			return fmt.Sprintf("/dev/nbd%d", i), nil
		}
	}
	return "", errors.New("disk: no free /dev/nbd device")
}
