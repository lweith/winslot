package vm

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"winslot/internal/config"
	"winslot/internal/slot"
	"winslot/internal/virt"
)

// fakeVirt is an in-memory hypervisor. Starting a domain simulates first
// boot: firstboot.ps1 reporting the VM's host key on COM1.
type fakeVirt struct {
	t       *testing.T
	doms    map[string]*fakeDom
	srv     map[string]bool
	logDir  string
	hostKey ssh.Signer
}

type fakeDom struct {
	xml, meta, desc string
	state           virt.State
	autostart       bool
}

func (f *fakeVirt) get(name string) (*fakeDom, error) {
	d, ok := f.doms[name]
	if !ok {
		return nil, fmt.Errorf("domain %s: not found", name)
	}
	return d, nil
}

func (f *fakeVirt) Define(x string) error {
	name := between(x, "<name>", "</name>")
	if d, ok := f.doms[name]; ok {
		d.xml = x
		return nil
	}
	f.doms[name] = &fakeDom{xml: x, state: virt.ShutOff}
	return nil
}
func (f *fakeVirt) State(name string) (virt.State, error) {
	if d, ok := f.doms[name]; ok {
		return d.state, nil
	}
	return virt.NotFound, nil
}
func (f *fakeVirt) Start(name string) error {
	d, err := f.get(name)
	if err != nil {
		return err
	}
	d.state = virt.Running
	line := "HOSTKEY " + strings.ToUpper(name) + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(f.hostKey.PublicKey())))
	return os.WriteFile(filepath.Join(f.logDir, name+".serial.log"), []byte("booting\r\n"+line+"\r\n"), 0o644)
}
func (f *fakeVirt) Shutdown(name string) error {
	d, err := f.get(name)
	if err == nil {
		d.state = virt.ShutOff
	}
	return err
}
func (f *fakeVirt) SetAutostart(name string, on bool) error {
	d, err := f.get(name)
	if err == nil {
		d.autostart = on
	}
	return err
}
func (f *fakeVirt) ListNames() ([]string, error) {
	var n []string
	for k := range f.doms {
		n = append(n, k)
	}
	return n, nil
}
func (f *fakeVirt) XML(name string) (string, error) {
	d, err := f.get(name)
	if err != nil {
		return "", err
	}
	return d.xml, nil
}
func (f *fakeVirt) SlotMetadata(name string) (string, bool, error) {
	d, err := f.get(name)
	if err != nil {
		return "", false, err
	}
	return d.meta, d.meta != "", nil
}
func (f *fakeVirt) SetSlotMetadata(name, x string) error {
	d, err := f.get(name)
	if err == nil {
		d.meta = x
	}
	return err
}
func (f *fakeVirt) SetDescription(name, desc string) error {
	d, err := f.get(name)
	if err == nil {
		d.desc = desc
	}
	return err
}
func (f *fakeVirt) DeleteSnapshotMetadata(string) error { return nil }
func (f *fakeVirt) Undefine(name string) error {
	d, err := f.get(name)
	if err != nil {
		return err
	}
	if d.state != virt.ShutOff {
		return errors.New("undefine: domain is running")
	}
	delete(f.doms, name)
	return nil
}
func (f *fakeVirt) AddDNSSRV(_, x string) error    { f.srv[x] = true; return nil }
func (f *fakeVirt) DeleteDNSSRV(_, x string) error { delete(f.srv, x); return nil }

func between(s, a, b string) string {
	i := strings.Index(s, a)
	j := strings.Index(s, b)
	if i < 0 || j < i {
		return ""
	}
	return s[i+len(a) : j]
}

// fakeDisks creates the disk file and remembers what was written into C:.
type fakeDisks struct{ files map[string][]byte }

func (f *fakeDisks) CopyGolden(_ context.Context, _, dst string, _ uint64) error {
	return os.WriteFile(dst, []byte("qcow2"), 0o644)
}
func (f *fakeDisks) WriteFile(_ context.Context, img, winPath string, content []byte) error {
	f.files[img+"|"+winPath] = content
	return nil
}

// fakeCertifier stands in for the VM's sshd. It runs the client's host-key
// callback against what a real VM would present (the plain key while pinned,
// the CA-signed certificate after installation), so the pinning and CA checks
// under test are the real ones.
type fakeCertifier struct {
	hostKey    ssh.Signer
	installed  *ssh.Certificate
	commands   []string
	pushedKeys string
	activation string // the VM's answer to queryActivation
}

func (f *fakeCertifier) Resolve(context.Context, string) error { return nil }

func (f *fakeCertifier) Run(host string, _ ssh.Signer, cb ssh.HostKeyCallback, algos []string, command, stdin string) (string, error) {
	f.commands = append(f.commands, strings.SplitN(command, "\n", 2)[0])
	var presented ssh.PublicKey = f.hostKey.PublicKey()
	if algos[0] == ssh.CertAlgoED25519v01 {
		if f.installed == nil {
			return "", errors.New("no certificate installed yet")
		}
		presented = f.installed
	}
	if err := cb(host+":22", &net.TCPAddr{}, presented); err != nil {
		return "", err
	}
	if strings.Contains(command, "SoftwareLicensingProduct") {
		return f.activation, nil
	}
	if strings.Contains(command, "administrators_authorized_keys") {
		f.pushedKeys = stdin
		return "KEYS-INSTALLED 3\r\n", nil
	}
	if strings.Contains(command, "HostCertificate") {
		k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(stdin))
		if err != nil {
			return "", err
		}
		f.installed = k.(*ssh.Certificate)
		return "CERT-INSTALLED\n", errors.New("connection closed: sshd restarted") // what a real restart looks like
	}
	return strings.ToUpper(strings.SplitN(host, ".", 2)[0]) + "\r\n", nil
}

func newSigner(t *testing.T) (ssh.Signer, []byte) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	return s, pem.EncodeToMemory(block)
}

type env struct {
	m    *Manager
	virt *fakeVirt
	disk *fakeDisks
	cert *fakeCertifier
	out  *bytes.Buffer
}

func setup(t *testing.T) env {
	t.Helper()
	pollInterval = time.Millisecond
	base := t.TempDir()
	cfg := config.Defaults()
	cfg.Storage = config.Storage{Data: filepath.Join(base, "data"), Cache: filepath.Join(base, "cache"), Etc: filepath.Join(base, "etc")}
	cfg.Network.Zone = "vms.example.org"
	cfg.Windows.TimeZone = "Eastern Standard Time"
	for _, d := range []string{"images", "disks"} {
		os.MkdirAll(filepath.Join(cfg.Storage.Data, d), 0o755)
	}
	os.WriteFile(filepath.Join(cfg.Storage.Data, "images", "win11-20261011-0353.qcow2"), []byte("golden"), 0o444)
	os.MkdirAll(cfg.Storage.Etc, 0o755)
	_, caPEM := newSigner(t)
	_, mgmtPEM := newSigner(t)
	os.WriteFile(filepath.Join(cfg.Storage.Etc, "host_ca"), caPEM, 0o600)
	os.WriteFile(filepath.Join(cfg.Storage.Etc, "mgmt_key"), mgmtPEM, 0o600)
	logDir := filepath.Join(base, "log")
	os.MkdirAll(logDir, 0o755)

	hostKey, _ := newSigner(t)
	fv := &fakeVirt{t: t, doms: map[string]*fakeDom{}, srv: map[string]bool{}, logDir: logDir, hostKey: hostKey}
	fd := &fakeDisks{files: map[string][]byte{}}
	fc := &fakeCertifier{hostKey: hostKey}
	out := &bytes.Buffer{}
	return env{
		m: &Manager{Cfg: cfg, Virt: fv, Disks: fd, Certifier: fc, Out: out, VirtFeature: "vmx", LogDir: logDir,
			Now: func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }},
		virt: fv, disk: fd, cert: fc, out: out,
	}
}

func TestCreate(t *testing.T) {
	e := setup(t)
	pw, err := e.m.Create(context.Background(), "wiresx", CreateOptions{DiskBytes: 100 << 30, MemoryMiB: 4096, VCPUs: 2})
	if err != nil {
		t.Fatalf("Create: %v\n%s", err, e.out)
	}
	d := e.virt.doms["wiresx"]
	if d.state != virt.Running || !d.autostart {
		t.Errorf("state=%v autostart=%v", d.state, d.autostart)
	}
	for _, want := range []string{"<memory unit=\"MiB\">4096</memory>", `cores="2"`, `name="vmx"`, "wiresx.serial.log", `machine="pc-q35-8.2"`} {
		if !strings.Contains(d.xml, want) {
			t.Errorf("domain XML lacks %s", want)
		}
	}
	meta, err := slot.ParseMetadata(d.meta)
	if err != nil || meta.Golden != "win11-20261011-0353" || meta.DiskBytes != 100<<30 || meta.Nested {
		t.Errorf("metadata %+v %v", meta, err)
	}
	answer := string(e.disk.files[e.m.DiskPath("wiresx")+"|"+UnattendPath])
	if !strings.Contains(answer, "<ComputerName>wiresx</ComputerName>") || !strings.Contains(answer, "Eastern Standard Time") || !strings.Contains(answer, pw) {
		t.Errorf("answer file:\n%s", answer)
	}
	kh, _ := os.ReadFile(filepath.Join(e.m.Cfg.Storage.Etc, "known_hosts"))
	if !strings.HasPrefix(string(kh), "wiresx.vms.example.org ssh-ed25519 ") {
		t.Errorf("known_hosts = %q", kh)
	}
	if e.cert.installed == nil || e.cert.installed.ValidPrincipals[0] != "wiresx.vms.example.org" {
		t.Errorf("certificate not installed for the FQDN: %+v", e.cert.installed)
	}
	if !e.virt.srv[e.m.SRVRecord("wiresx")] {
		t.Error("SRV record not published")
	}
	if !strings.Contains(e.out.String(), "READY: wiresx.vms.example.org") {
		t.Errorf("output:\n%s", e.out)
	}
	if _, err := e.m.Create(context.Background(), "wiresx", CreateOptions{DiskBytes: 1, MemoryMiB: 1, VCPUs: 1}); err == nil {
		t.Error("second Create of the same slot succeeded")
	}
}

func TestCreateRefusesHostKeyMismatch(t *testing.T) {
	e := setup(t)
	impostor, _ := newSigner(t)
	e.cert.hostKey = impostor // the machine answering is not the one that reported over COM1
	HostKeyWait = time.Second
	_, err := e.m.Create(context.Background(), "wiresx", CreateOptions{DiskBytes: 80 << 30, MemoryMiB: 4096, VCPUs: 2})
	if err == nil || e.cert.installed != nil {
		t.Fatalf("certificate installed on an impostor: err=%v", err)
	}
}

func TestDecommissionAndRecreate(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.m.Create(ctx, "wiresx", CreateOptions{DiskBytes: 80 << 30, MemoryMiB: 4096, VCPUs: 2}); err != nil {
		t.Fatal(err)
	}
	uuid := between(e.virt.doms["wiresx"].xml, "<uuid>", "</uuid>")

	if err := e.m.Decommission(ctx, "wiresx"); err == nil {
		t.Error("decommissioned a running VM")
	}
	e.virt.Shutdown("wiresx")
	if err := e.m.Decommission(ctx, "wiresx"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.m.DiskPath("wiresx")); !os.IsNotExist(err) {
		t.Error("disk not deleted")
	}
	if _, ok := e.virt.doms["wiresx"]; !ok {
		t.Fatal("domain undefined: the identity is lost")
	}
	if e.virt.srv[e.m.SRVRecord("wiresx")] || e.virt.doms["wiresx"].autostart {
		t.Error("SRV record or autostart left behind")
	}
	list, _ := e.m.List()
	if len(list) != 1 || list[0].State != "decommissioned" {
		t.Errorf("List = %+v", list)
	}

	if _, err := e.m.Recreate(ctx, "wiresx", 0); err != nil {
		t.Fatalf("Recreate: %v", err)
	}
	d := e.virt.doms["wiresx"]
	if between(d.xml, "<uuid>", "</uuid>") != uuid {
		t.Error("recreate changed the identity")
	}
	meta, _ := slot.ParseMetadata(d.meta)
	if meta.Recreated == nil || meta.DiskBytes != 80<<30 {
		t.Errorf("metadata after recreate: %+v", meta)
	}
	if _, err := e.m.Recreate(ctx, "wiresx", 0); err == nil {
		t.Error("recreate over an existing disk succeeded")
	}
}

func TestUpdateKeepsIdentity(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.m.Create(ctx, "wiresx", CreateOptions{DiskBytes: 80 << 30, MemoryMiB: 4096, VCPUs: 2}); err != nil {
		t.Fatal(err)
	}
	d := e.virt.doms["wiresx"]
	before := d.xml
	// Simulate a slot defined by an older template: cirrus video, old title.
	d.xml = strings.Replace(strings.Replace(before, `<model type="virtio" heads="1" primary="yes">`, `<model type="cirrus">`, 1),
		"<title>wiresx · Windows 11 Pro license slot</title>", "<title>Windows 11 Pro (license slot)</title>", 1)
	meta := d.meta

	if err := e.m.Update(ctx, "wiresx"); err != nil {
		t.Fatal(err)
	}
	if d.xml != before {
		t.Errorf("updated definition differs from the current template:\n%s", d.xml)
	}
	if d.meta != meta {
		t.Error("slot metadata lost")
	}
	if err := e.m.Update(ctx, "not-a-slot"); err == nil {
		t.Error("updated a domain that is not a slot")
	}
}

func TestListSkipsForeignDomains(t *testing.T) {
	e := setup(t)
	e.virt.doms["ubuntu-vm"] = &fakeDom{state: virt.Running}
	list, err := e.m.List()
	if err != nil || len(list) != 0 {
		t.Errorf("List = %+v, %v", list, err)
	}
	e.virt.doms["legacy"] = &fakeDom{state: virt.Running, meta: `<slot edition="Windows 11 Pro" disk_size="80G"/>`}
	list, err = e.m.List()
	if err != nil || len(list) != 1 || !strings.HasPrefix(list[0].State, "invalid metadata") {
		t.Errorf("List with a legacy slot = %+v, %v", list, err)
	}
	if _, err := e.m.Slot("ubuntu-vm"); err == nil {
		t.Error("non-slot domain treated as a slot")
	}
}

func TestPushKeys(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	keysPath := filepath.Join(e.m.Cfg.Storage.Etc, "authorized_keys")
	if _, err := e.m.Create(ctx, "wiresx", CreateOptions{DiskBytes: 80 << 30, MemoryMiB: 4096, VCPUs: 2}); err != nil {
		t.Fatal(err)
	}

	os.WriteFile(keysPath, []byte("ssh-ed25519 AAAAjon jon\n"), 0o644)
	if err := e.m.PushKeys(ctx, "wiresx"); err == nil {
		t.Error("pushed a key list without the management key (would lock winslot out)")
	}

	list := "ssh-ed25519 AAAAjon jon\nfrom=\"10.0.3.1\" ssh-ed25519 AAAAm winslot-mgmt (root@h)\n"
	os.WriteFile(keysPath, []byte(list), 0o644)
	if err := e.m.PushKeys(ctx, "wiresx"); err != nil {
		t.Fatal(err)
	}
	if e.cert.pushedKeys != list {
		t.Errorf("pushed %q", e.cert.pushedKeys)
	}

	// The push is CA-verified: a VM presenting a certificate from another CA is refused.
	rogue, _ := newSigner(t)
	cert := &ssh.Certificate{Key: e.cert.hostKey.PublicKey(), CertType: ssh.HostCert,
		ValidPrincipals: []string{"wiresx.vms.example.org"}, ValidBefore: ssh.CertTimeInfinity}
	if err := cert.SignCert(rand.Reader, rogue); err != nil {
		t.Fatal(err)
	}
	e.cert.installed = cert
	if err := e.m.PushKeys(ctx, "wiresx"); err == nil {
		t.Error("pushed keys to a VM whose certificate is not from our CA")
	}
}

func TestKnownHostsHelpers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	os.WriteFile(path, []byte("a.example ssh-ed25519 AAAAold\nb.example ssh-ed25519 AAAAb\n"), 0o644)
	k, _ := newSigner(t)
	if err := ReplaceKnownHosts(path, "a.example", []ssh.PublicKey{k.PublicKey()}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "AAAAold") || !strings.Contains(string(got), "b.example") || strings.Count(string(got), "a.example") != 1 {
		t.Errorf("after replace:\n%s", got)
	}
	RemoveKnownHosts(path, "a.example")
	if got, _ := os.ReadFile(path); string(got) != "b.example ssh-ed25519 AAAAb\n" {
		t.Errorf("after remove: %q", got)
	}
}
