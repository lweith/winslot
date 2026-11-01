# winslot

Licensed Windows VMs on a plain libvirt + ZFS host, for the Toledo Mobile Radio
Association (W8HHF). It gives app admins (WIRES-X sysops, Winlink gateway
operators and the like) Windows machines with the basic plumbing already done,
and lets the club rebuild those machines without ever moving a Windows license.

Procedures are in [RUNBOOK.md](RUNBOOK.md). Run `winslot --help` for the command reference.

## Why winslot exists: licenses

Windows activation is bound to a **hardware ID** that Windows computes from the
machine it runs on. A VM's "hardware" is whatever the hypervisor presents: the
SMBIOS UUID, the NIC's MAC, the disk's serial number and WWN, the machine type.
Create a new VM the usual way and all of those are new. To Windows and to
Microsoft's activation servers it is a different computer, and its license would
have to be moved to it.

winslot turns the hardware identity into a durable thing, a **license slot**:

- A slot is a libvirt domain whose identity (UUID, MAC, disk serial/WWN,
  `pc-q35-8.2`) is generated once and **never changed**.
- A slot's VM can be thrown away and rebuilt from a fresh golden image
  (`winslot vm decommission`, then `winslot vm recreate`). The new install sees
  exactly the same hardware, so Microsoft recognises the machine and a digital
  license reactivates by itself. Nothing is transferred.
- Decommissioned slots stay defined, with no disk. `winslot vm list` therefore
  is also the list of licensed machines the club can bring back, with their
  edition and sizing.
- libvirt refuses two domains with the same UUID, so one slot can never be
  running twice, and one license never ends up on two machines.

**Never `virsh undefine` a slot.** The identity is the license.

### Why retail licenses

winslot makes it so we probably never *have* to transfer a license; retail
licenses make it so we *can* if we ever must. A retail license may be moved to
another device, one device at a time. OEM licenses die with their first device,
and volume licensing brings agreements and a KMS server we don't want to run. So
the club buys retail, binds each license to a slot, and keeps the right to move
it if a slot is ever lost.

One license per slot, one running VM per slot. This is a summary of how the
pieces fit, not legal advice; Microsoft's license terms govern.

## How it works

```
 Windows ISO ─┐
 pinned payload (installers, virtio drivers, sha256) ─┼─ winslot image build ──► golden image (qcow2, read-only)
 answer files, admin keys (embedded / /etc/winslot) ──┘   Packer: Setup → Audit Mode →
                                                           provision.ps1 → Sysprep (offline)
 golden image ── winslot vm create NAME ──► license slot NAME
                   new identity, independent disk copy, first-boot answer file
                   (hostname, admin password) written into C: offline, boot,
                   SSH host keys reported on COM1 → host CA certificate,
                   readiness check, Prometheus SRV record
```

- **Golden images are built, never patched.** A build is unattended and offline:
  the build VM's network is restricted, so nothing but the pinned payload goes
  in. Each image is verified offline (Sysprep succeeded, every component
  present, no build answer file, no SSH host keys) before it is stored read-only.
  Newer software means a new image, and VMs move onto it by being recreated.
- **Identity, not disks, is what lasts.** Disks are independent qcow2 copies on a
  ZFS dataset. That's simple, and ZFS's block sharing wasn't worth the
  bookkeeping, since Windows Update diverges clones within days.
- **SSH host keys are known a priori.** Each VM generates its host keys at first
  boot and reports the public keys on its serial port, a hypervisor-local
  channel, before anyone has connected. winslot records them, signs the ed25519
  key with the site's **host CA**, and installs the certificate over SSH pinned
  to the reported key. Admins trust the CA with one line (`winslot ssh
  known-hosts`); there is never trust on first use.
- **Access:** one admin key list per site (`/etc/winslot/authorized_keys`), baked
  into images and pushed to running VMs (`winslot vm push-keys`). The management
  key, used by winslot itself, works only from the host's VM-network address.
- **Network:** VMs sit on a routed subnet (no NAT) advertised by OSPF. DHCP
  registers each VM as `<name>.<zone>`, at a static address reserved for its MAC. libvirt's `clean-traffic` filter pins
  each VM to its own MAC and DHCP address, so VMs can't impersonate each other
  or the host.
- **Monitoring:** every VM runs windows_exporter on TCP 9182 and is published as a
  DNS SRV record. Prometheus finds all of them, current and future, with one job:
  `dns_sd_configs: [{names: ['_windows-exporter._tcp.<zone>']}]`.
- **Nested virtualization** is hidden from guests by default (it widens the
  host's attack surface); `--nested` exposes it per slot for WSL2, Docker or VBS.

## What's in the golden image

Windows 11 Pro; Firefox and SumatraPDF (the default browser and PDF viewer for
new users; SumatraPDF's update check is off), 7-Zip, Notepad++, Tera Term, PowerShell 7, Git,
Sysinternals (`C:\Tools\Sysinternals`); windows_exporter (TCP 9182); QEMU guest
agent and virtio drivers; OpenSSH server (Win32-OpenSSH MSI: offline-installable,
Microsoft-signed; patched by building a new image); RDP with NLA; the club
wallpaper; Eastern time. Firewall rules for 22, 3389 and 9182 cover all
profiles. BitLocker device encryption, hibernation and consumer-app auto-installs
are off.

## Repository

```
cmd/winslot, internal/   the winslot command (Go): cli, vm lifecycle, image build/import,
                          libvirt (go-libvirt), domain XML, SSH CA, payload, disk tools
assets/answer/           Windows answer files and in-guest scripts (embedded in the binary)
assets/packer/           Packer template and the pinned payload list (embedded)
etc/winslot.toml         configuration template
install/                 server installer: autoinstall template, bootstrap, make-installer
sites/<site>.*           per-site settings and admin key list
debian/                  packaging
```

`make check` runs vet, staticcheck and the tests with the race detector. `make
integration` builds the libvirt integration tests, to run as root on a host.
`make deb` builds the package.
