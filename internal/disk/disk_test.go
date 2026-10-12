package disk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winslot/internal/run"
)

// sysBlock fakes /sys/block with nbd0 busy and nbd1 free.
func sysBlock(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for dev, size := range map[string]string{"nbd0": "167772160\n", "nbd1": "0\n"} {
		os.MkdirAll(filepath.Join(dir, dev), 0o755)
		os.WriteFile(filepath.Join(dir, dev, "size"), []byte(size), 0o644)
	}
	return dir
}

func TestWithWindowsSequenceAndCleanup(t *testing.T) {
	rec := &run.Recorder{}
	tools := Tools{R: rec, SysBlock: sysBlock(t)}
	var mnt string
	err := tools.WithWindows(context.Background(), "/d/vm.qcow2", true, func(root string) error {
		mnt = root
		return errors.New("verification failed")
	})
	if err == nil || err.Error() != "verification failed" {
		t.Fatalf("err = %v", err)
	}
	want := []string{
		"modprobe nbd max_part=8",
		"qemu-nbd --read-only --connect /dev/nbd1 /d/vm.qcow2",
		"udevadm settle",
		"mount -t ntfs3 -o ro,nocase /dev/nbd1p3 " + mnt,
		"umount " + mnt, // cleanup despite fn's error
		"qemu-nbd --disconnect /dev/nbd1",
	}
	if strings.Join(rec.Commands, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(rec.Commands, "\n"), strings.Join(want, "\n"))
	}
	if _, err := os.Stat(mnt); !os.IsNotExist(err) {
		t.Error("mount point not removed")
	}
}

func TestWithWindowsMountFailureStillDetaches(t *testing.T) {
	rec := &run.Recorder{FailOn: "mount -t"}
	tools := Tools{R: rec, SysBlock: sysBlock(t)}
	called := false
	err := tools.WithWindows(context.Background(), "/d/vm.qcow2", false, func(string) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	last := rec.Commands[len(rec.Commands)-1]
	if last != "qemu-nbd --disconnect /dev/nbd1" {
		t.Errorf("last command %q, want detach", last)
	}
}

func TestWriteFile(t *testing.T) {
	rec := &run.Recorder{}
	tools := Tools{R: rec, SysBlock: sysBlock(t)}
	// The recorder doesn't mount anything, so the file lands in the temp
	// mount directory; capture it before WithWindows removes that directory.
	var written []byte
	err := tools.WithWindows(context.Background(), "/d/vm.qcow2", false, func(root string) error {
		p := filepath.Join(root, "Windows", "Panther", "Unattend", "Unattend.xml")
		if err := (Tools{}).writeAt(root, `Windows\Panther\Unattend\Unattend.xml`, []byte("<unattend/>")); err != nil {
			return err
		}
		written, _ = os.ReadFile(p)
		return os.RemoveAll(filepath.Join(root, "Windows"))
	})
	if err != nil || string(written) != "<unattend/>" {
		t.Fatalf("err=%v written=%q", err, written)
	}
}

func TestCopyGolden(t *testing.T) {
	rec := &run.Recorder{}
	dst := filepath.Join(t.TempDir(), "vm.qcow2")
	if err := (Tools{R: rec}).CopyGolden(context.Background(), "/i/win11.qcow2", dst, 80<<30); err != nil {
		t.Fatal(err)
	}
	want := "qemu-img convert -O qcow2 /i/win11.qcow2 " + dst + "\nqemu-img resize -q " + dst + " 85899345920"
	if got := strings.Join(rec.Commands, "\n"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	os.WriteFile(dst, nil, 0o644)
	if err := (Tools{R: rec}).CopyGolden(context.Background(), "/i/win11.qcow2", dst, 1); err == nil {
		t.Error("existing destination overwritten")
	}
}

func TestWriteAtRejectsEscape(t *testing.T) {
	root := t.TempDir()
	if err := (Tools{}).writeAt(root, `..\..\etc\evil`, []byte("x")); err == nil {
		t.Error("path escaping the partition was written")
	}
}

func TestFreeNBDNone(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "nbd0"), 0o755)
	os.WriteFile(filepath.Join(dir, "nbd0", "size"), []byte("42\n"), 0o644)
	if _, err := (Tools{SysBlock: dir}).freeNBD(); err == nil {
		t.Error("want error when every nbd device is busy")
	}
}
