package vm

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"winslot/internal/domain"
)

// Static addresses are DHCP reservations in the libvirt network: MAC to
// address and name. A slot's MAC never changes, so its address outlives
// decommission and recreate. New addresses come from the part of the subnet
// below the dynamic DHCP range, which dnsmasq never hands out on its own.

type dhcpHost struct {
	MAC  string `xml:"mac,attr"`
	Name string `xml:"name,attr"`
	IP   string `xml:"ip,attr"`
}

type dhcpRange struct {
	Start string `xml:"start,attr"`
}

type networkXML struct {
	IPs []struct {
		Family  string `xml:"family,attr"`
		Address string `xml:"address,attr"`
		Netmask string `xml:"netmask,attr"`
		Prefix  int    `xml:"prefix,attr"`
		DHCP    *struct {
			Ranges []dhcpRange `xml:"range"`
			Hosts  []dhcpHost  `xml:"host"`
		} `xml:"dhcp"`
	} `xml:"ip"`
}

// vmNet is what reservations need from the libvirt network.
type vmNet struct {
	gateway netip.Addr
	subnet  netip.Prefix
	ranges  []netip.Addr // starts of the dynamic ranges
	hosts   []dhcpHost
}

func parseVMNet(x string) (vmNet, error) {
	var n networkXML
	if err := xml.Unmarshal([]byte(x), &n); err != nil {
		return vmNet{}, fmt.Errorf("network XML: %w", err)
	}
	for _, ip := range n.IPs {
		if ip.DHCP == nil || (ip.Family != "" && ip.Family != "ipv4") {
			continue
		}
		gw, err := netip.ParseAddr(ip.Address)
		if err != nil {
			return vmNet{}, fmt.Errorf("network address: %w", err)
		}
		bits := ip.Prefix
		if ip.Netmask != "" {
			m, err := netip.ParseAddr(ip.Netmask)
			if err != nil {
				return vmNet{}, fmt.Errorf("network netmask: %w", err)
			}
			bits, _ = net.IPMask(m.AsSlice()).Size()
		}
		subnet, err := gw.Prefix(bits)
		if err != nil {
			return vmNet{}, fmt.Errorf("network prefix: %w", err)
		}
		v := vmNet{gateway: gw, subnet: subnet, hosts: ip.DHCP.Hosts}
		for _, r := range ip.DHCP.Ranges {
			s, err := netip.ParseAddr(r.Start)
			if err != nil {
				return vmNet{}, fmt.Errorf("DHCP range: %w", err)
			}
			v.ranges = append(v.ranges, s)
		}
		return v, nil
	}
	return vmNet{}, errors.New("network has no IPv4 DHCP section")
}

func (v vmNet) inDynamicRange(a netip.Addr) bool {
	for _, s := range v.ranges {
		if !a.Less(s) {
			return true
		}
	}
	return false
}

func (v vmNet) taken(a netip.Addr) bool {
	for _, h := range v.hosts {
		if h.IP == a.String() {
			return true
		}
	}
	return false
}

// usable refuses the subnet's own addresses: network, broadcast, the host.
func (v vmNet) usable(a netip.Addr) error {
	switch {
	case !v.subnet.Contains(a):
		return fmt.Errorf("%s is outside %s", a, v.subnet)
	case a == v.subnet.Masked().Addr():
		return fmt.Errorf("%s is the network address", a)
	case !v.subnet.Contains(a.Next()):
		return fmt.Errorf("%s is the broadcast address", a)
	case a == v.gateway:
		return fmt.Errorf("%s is this host", a)
	}
	return nil
}

// next returns the lowest free address below the dynamic range.
func (v vmNet) next() (netip.Addr, error) {
	for a := v.subnet.Masked().Addr().Next(); v.subnet.Contains(a); a = a.Next() {
		if v.inDynamicRange(a) {
			break
		}
		if v.usable(a) == nil && !v.taken(a) {
			return a, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("no free static addresses in %s below the DHCP range", v.subnet)
}

func hostElement(h dhcpHost) string {
	return fmt.Sprintf(`<host mac='%s' name='%s' ip='%s'/>`, h.MAC, h.Name, h.IP)
}

func (m *Manager) vmNet() (vmNet, error) {
	x, err := m.Virt.NetworkXML(m.Cfg.Network.Libvirt)
	if err != nil {
		return vmNet{}, err
	}
	return parseVMNet(x)
}

// ReserveIP gives slot name a static address and returns it. It is idempotent:
// a slot that has one keeps it unless want names a different one. A zero want
// means any free address. A running VM moves to a new address at its next DHCP
// renewal.
func (m *Manager) ReserveIP(name string, want netip.Addr) (netip.Addr, error) {
	if _, err := m.Slot(name); err != nil {
		return netip.Addr{}, err
	}
	x, err := m.Virt.XML(name)
	if err != nil {
		return netip.Addr{}, err
	}
	cur, err := domain.Parse(x)
	if err != nil {
		return netip.Addr{}, err
	}
	v, err := m.vmNet()
	if err != nil {
		return netip.Addr{}, err
	}
	var have *dhcpHost
	for i, h := range v.hosts {
		switch {
		case strings.EqualFold(h.MAC, cur.Identity.MAC):
			have = &v.hosts[i]
		case h.Name == name:
			return netip.Addr{}, fmt.Errorf("the name %s is already reserved for MAC %s", name, h.MAC)
		}
	}
	if have != nil && have.Name == name && (!want.IsValid() || have.IP == want.String()) {
		return netip.ParseAddr(have.IP)
	}
	addr := want
	switch {
	case want.IsValid():
		if err := v.usable(want); err != nil {
			return netip.Addr{}, err
		}
		for _, h := range v.hosts {
			if h.IP == want.String() && !strings.EqualFold(h.MAC, cur.Identity.MAC) {
				return netip.Addr{}, fmt.Errorf("%s is already reserved for %s", want, h.Name)
			}
		}
	case have != nil: // same address, name corrected below
		if addr, err = netip.ParseAddr(have.IP); err != nil {
			return netip.Addr{}, err
		}
	default:
		if addr, err = v.next(); err != nil {
			return netip.Addr{}, err
		}
	}
	if have != nil {
		if err := m.Virt.DeleteDHCPHost(m.Cfg.Network.Libvirt, hostElement(*have)); err != nil {
			return netip.Addr{}, fmt.Errorf("replacing %s's reservation: %w", name, err)
		}
	}
	h := dhcpHost{MAC: cur.Identity.MAC, Name: name, IP: addr.String()}
	if err := m.Virt.AddDHCPHost(m.Cfg.Network.Libvirt, hostElement(h)); err != nil {
		return netip.Addr{}, fmt.Errorf("reserving %s for %s: %w", addr, name, err)
	}
	m.logf("== address: %s is reserved for %s", addr, name)
	return addr, nil
}

// ReleaseIP removes slot name's reservation (absent is fine).
func (m *Manager) ReleaseIP(name string) error {
	v, err := m.vmNet()
	if err != nil {
		return err
	}
	for _, h := range v.hosts {
		if h.Name == name {
			if err := m.Virt.DeleteDHCPHost(m.Cfg.Network.Libvirt, hostElement(h)); err != nil {
				return fmt.Errorf("releasing %s's address: %w", name, err)
			}
		}
	}
	return nil
}

// Reservations returns each reserved name's address.
func (m *Manager) Reservations() (map[string]string, error) {
	v, err := m.vmNet()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(v.hosts))
	for _, h := range v.hosts {
		out[h.Name] = h.IP
	}
	return out, nil
}
