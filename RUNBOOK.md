# winslot runbook

Commands run as root on the winslot host unless marked otherwise. Every
command has `--help`, and tab completion completes slot names.

## 1. A new host

**From the installer stick** (the normal way): see `install/` and
`make installer SITE=<site>`. Autoinstall plus `install/bootstrap` set up
everything below and build the first golden image unattended. The console
login prompt shows the SSH host key fingerprints and a break-glass root
password (root never logs in over SSH). Day-to-day login is `tmra-admin` with
the site's keys, then `sudo`.

**Rebuilding a site's host** with the same stick recipe needs a few things in
hand first:

- `winslot payload fetch`: `make installer` refuses a payload that doesn't match
  `payload.list`.
- A host CA backup, passed as `CA_BACKUP=`. Without it the site gets a new CA, and
  every admin must replace their trust line. Make it on the old host, on its
  console, and keep it and its passphrase off the stick:

  ```
  sudo apt install age
  sudo tar -C /etc/winslot -cf /root/host-ca.tar host_ca host_ca.pub mgmt_key mgmt_key.pub
  sudo age -p -o host-ca.tar.age /root/host-ca.tar
  sudo rm /root/host-ca.tar
  ```

  With a backup on the stick, the bootstrap stops before installing winslot, and
  the console says how to restore it and carry on. That step isn't unattended.
- `sites/<site>.lan-hosts` and `.lan-records`, copied from `/etc/winslot/lan-hosts`
  and `lan-records` if they've changed.
- Each licensed slot's sheet or `license show` output, for `winslot vm restore` (see
  4a). It keeps the slot's address. USB and PCI passthrough devices are not kept:
  attach them again.

**By hand** on Ubuntu 24.04: install the package (`apt install ./winslot_*.deb`),
then:

1. Copy `/usr/share/doc/winslot/winslot.toml.example` to
   `/etc/winslot/winslot.toml` and fill it in (ISO path, zone, VM-network gateway).
2. Put the site's admin public keys in `/etc/winslot/authorized_keys`.
3. `winslot host init` creates the host CA, the management key and `known_hosts.ca`.
4. Set up the VM network, DNS and OSPF as `install/bootstrap` does (steps
   `vm-network` and `ospf`).
5. `winslot image build`.

## 2. Admins: reaching the VMs (on your own machine, once)

On the host, print the two pieces and copy them to your machine:

```
winslot ssh known-hosts      # -> ~/.ssh/known_hosts.winslot
winslot ssh config           # -> append to ~/.ssh/config
```

Then `ssh <vm>.<zone>` checks the VM's host certificate against the CA, with no
prompts. Before trusting the CA line, check its fingerprint against the one the
tech committee publishes: `ssh-keygen -lf ~/.ssh/known_hosts.winslot`.

## 3. Golden images

```
winslot image build          # ~30 min, unattended, offline; verifies and imports
winslot image list           # the newest is used by create/recreate
```

To update a component: change its URL in `assets/packer/payload.list`, set its
sha256 to the new file's (check it), rebuild the package, then build an image. A
failed build leaves `/var/lib/winslot/builds/<name>/` for inspection; guest
progress is in `out/serial.log`.

## 4. VMs (license slots)

```
winslot vm create wiresx [--disk 120G] [--memory 6G] [--cpus 2] [--nested]
winslot vm list
```

`create` prints the VM's `admin` password **once**: put it in the password
manager. It finishes only when the VM passes the readiness check (CA-verified
SSH works). Then, in the VM: activate Windows (see 4a), install the
application, and snapshot before you change anything.

**Addresses**: each slot gets a static address, a DHCP reservation on its MAC,
from the part of the VM subnet below the dynamic range. It is kept through
decommission and recreate, and freed by delete. For a slot made before 0.2.2, or
to choose the address:

```
winslot vm reserve wiresx --ip 10.0.3.161
```

**Decommission** (VM gone, license slot kept):

```
winslot vm decommission wiresx
```

**Recreate** (same slot, fresh disk from the newest golden image; Windows should
reactivate by itself, untested so far):

```
winslot vm recreate wiresx [--disk 160G]
```

Moving a VM onto a new golden image is decommission + recreate. Back up the
application's data first.

**Update** (slot made by an older winslot: new hardware template, identity
kept; takes effect at the next cold boot, `recreate` does it automatically):

```
winslot vm update wiresx
```

It also pins the CPU model of an unlicensed slot from before 0.2.3, and refuses
if the slot is already licensed. Passthrough devices added with `virsh` aren't
kept: attach them again.

**Delete** (a slot that was never activated: test VMs, mistakes):

```
winslot vm delete scratch
```

Licensed slots are refused. **Never `virsh undefine` a slot**: for a licensed
one, its identity is the license.

## 4a. Licenses

A slot whose Windows is activated is **licensed**: it is in the license
register (`/etc/winslot/licenses.toml`, root only) and its identity is
precious. Every other slot is disposable.

Activate Windows in the VM the normal way (Settings > System > Activation,
enter the retail key). Within six hours the `winslot-license-scan` timer finds
it, or run the scan now:

```
winslot license scan wiresx
```

The scan records the slot: its identity, and its key, read back from the VM
and confirmed against the last five characters Windows reports. It also writes
the slot's sheet to `/etc/winslot/sheets/wiresx.pdf`. Then:

```
winslot license record wiresx --purchase "Microsoft Store order 1234, treasurer, 2026-10-01"
```

and print the rewritten sheet twice, one for the board's file and one for the
safe. It holds the key: copy it off the host (`sudo cat
/etc/winslot/sheets/wiresx.pdf`) and don't leave copies lying around.

If the scan says the key could not be recovered (or the VM was activated by
digital license, e.g. after a recreate), record it from the box or receipt:

```
winslot license record wiresx --key-stdin
```

Other commands: `license list` (keys by their last five), `license show NAME`
(the full record), `license print [NAME...]` (rewrite sheets).

The scan never changes a record; it warns if a registered slot is no longer
activated, or carries a different key. The timer's unit then shows as failed
(`systemctl --failed`, `journalctl -u winslot-license-scan`).

**Transferring a retail license** to other hardware:

```
winslot vm decommission wiresx
winslot license release wiresx      # the slot becomes disposable
```

then enter the key on the new machine. Keep the old sheet until it's activated
there: it's the only other copy of the key.

**Restoring a licensed slot on a new host**, from the sheet: scan its QR code
into a text file (any phone QR app, then copy the text), or use
`winslot license show NAME` output from the old host:

```
winslot vm restore wiresx.toml
winslot vm recreate wiresx
```

Windows should see the same virtual hardware and reactivate from its digital
license (untested so far); if not, enter the key from the sheet. A new host must
be able to run the slot's CPU model, or the VM refuses to start: check with
`virsh domcapabilities --machine pc-q35-8.2`. An Intel-to-AMD move changes the
CPU vendor Windows sees, so expect to reactivate.

## 5. Admin keys

The list is `/etc/winslot/authorized_keys` (one key per line, with a comment
naming the holder or role). New images get it at build time; running VMs with:

```
winslot vm push-keys            # every running slot
winslot vm push-keys wiresx     # one
```

The `winslot-mgmt` line must stay: it's how winslot reaches the VMs, and it only
works from this host. `winslot vm push-keys` refuses a list without it.

Role tokens: keep access on hardware keys (YubiKey `ed25519-sk`, resident,
`verify-required`) held by roles, not people. Handover means passing the token
and changing its PIN.

## 6. Host CA

`/etc/winslot/host_ca` signs every VM's host certificate. If it may be
compromised, or root on this host ever is:

1. Move the old `host_ca*` away, run `winslot host init` (new CA, new `known_hosts.ca`).
2. `winslot vm certify NAME` for each slot (re-signs from the recorded host keys).
3. Every admin replaces their `known_hosts.winslot` line (`winslot ssh known-hosts`).

## 7. Monitoring

Each VM serves windows_exporter on TCP 9182 and has an SRV record. One
Prometheus job finds them all:

```yaml
- job_name: windows
  dns_sd_configs:
    - names: ['_windows-exporter._tcp.vms.example.org']
```

Prometheus's resolver must resolve the zone: the host answers for it on its
static uplink address (see 7a).

## 7a. Site DNS and LAN DHCP

The host answers DNS on its static uplink address. Off-site clients come in over
WireGuard and send only the club's internal zone there, not all their lookups:

| Client | Setting |
|---|---|
| Linux with `systemd-resolved` | `DNS=<address>` and `Domains=~<internal zone>` on the WireGuard link |
| Linux with dnsmasq or OpenWrt | `server=/<internal zone>/<address>` |
| Windows | `Add-DnsClientNrptRule -Namespace ".<internal zone>" -NameServers "<address>"` |
| macOS | File `/etc/resolver/<internal zone>` containing `nameserver <address>` |

Browsers using DNS over HTTPS skip all of this and get the public wildcard
instead of the VM. Turn it off by policy. For Firefox on Linux, in
`/etc/firefox/policies/policies.json`:
`{"policies":{"DNSOverHTTPS":{"Enabled":false,"Locked":true}}}`

A site with `LAN_DHCP_START` set also runs `winslot-lan-dns`, a dnsmasq on the
uplink, separate from libvirt's. It leases the pool, registers devices as
`<name>.<LAN_ZONE>`, forwards the VM zone to libvirt's dnsmasq, and sends other
peers' zones to `PEER_ZONES`.

| File | Holds | Format |
|---|---|---|
| `/etc/winslot/lan-dns.conf` | Service settings | dnsmasq |
| `/etc/winslot/lan-hosts` | DHCP reservations | `mac,address,name` |
| `/etc/winslot/lan-records` | Names of statically addressed devices | hosts file |

After changing `lan-hosts` or `lan-records`: `systemctl reload winslot-lan-dns`.
Copy changes to `sites/<site>.lan-hosts` and `.lan-records` too, or a rebuild
loses them. A rebuild restores those files, and `LAN_DHCP_AUTHORITATIVE=yes` in the
site file adds `dhcp-authoritative`.

**Router as last resort.** DHCP has no priority, so the router must delay its
replies. It keeps a small pool that doesn't overlap the server's, and ignores a
client until its `secs` field reaches a few seconds (EdgeOS, in the LAN's shared
network: `shared-network-parameters "min-secs 5;"`). A client that leaves `secs`
at 0 never gets an answer from a delayed router, so it has no fallback: give it
a static address if it must survive the host being down.

**Moving a LAN over.** Reserve every current lease at its current address first,
and list those names in `lan-records` too: a device the router has a static
mapping for keeps renewing with the router, and dnsmasq only publishes names for
its own leases. Then add `dhcp-authoritative` to `lan-dns.conf` and move the
router's pool. Clients with old leases rebind by broadcast, and an authoritative
dnsmasq confirms their reserved address; without it they lose the address when
the lease expires. It also NAKs any address it doesn't know, so the reservations
must be complete first.

Activation: images built with winslot 0.2.0 or later export
`windows_license_status{state="genuine"}` (1 when Windows' genuine check
passes). It is a supplement to `winslot license scan`, not a replacement; what
an activated VM reports there has not been checked yet.

## 8. Backups

VM disks are files in the `tank/winslot` dataset (`/var/lib/winslot/disks`). A
consistent backup of a VM is: shut it down (or accept crash consistency), `zfs
snapshot tank/winslot@<date>`, `zfs send` it off the host, and keep the domain
definition (`virsh dumpxml --inactive --security-info NAME`), its UEFI variables
(`/var/lib/libvirt/qemu/nvram/NAME_VARS.fd`) and vTPM state
(`/var/lib/libvirt/swtpm/<uuid>/`). The slot's identity lives in the domain
definition: keep it, and a recreate rebuilds the VM on the same identity even
without the disk.

Back up `/etc/winslot` with the host (it holds the license register, the
sheets and the host CA; root only). The printed sheets are the backup of last
resort for licensed slots: `winslot vm restore` rebuilds a slot from one.
