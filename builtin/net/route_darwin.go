package net

import (
	stdnet "net"
	"syscall"

	xroute "golang.org/x/net/route"
)

// rtfIfScope marks a route that is only for traffic bound to one interface.
// macOS keeps a scoped default beside the real one for each interface that has
// a gateway, so that a socket bound to a VPN's interface still finds its way;
// they are in the table whether or not the interface carries the machine's
// traffic, and none of them is the default route.
const rtfIfScope = 0x1000000

// readDefaultRoute is the default route in force, asked of the kernel's routing
// table by a sysctl rather than by running netstat, and found false when the
// table holds none.
func readDefaultRoute() (defaultRoute, bool, error) {
	rib, err := xroute.FetchRIB(syscall.AF_UNSPEC, xroute.RIBTypeRoute, 0)
	if err != nil {
		return defaultRoute{}, false, err
	}
	msgs, err := xroute.ParseRIB(xroute.RIBTypeRoute, rib)
	if err != nil {
		return defaultRoute{}, false, err
	}
	r, ok := defaultOf(msgs, interfaceName)
	return r, ok, nil
}

func interfaceName(index int) string {
	if in, err := stdnet.InterfaceByIndex(index); err == nil {
		return in.Name
	}
	return ""
}

// defaultOf picks the default route out of the table's messages: up, not scoped
// to an interface, not a reject or a blackhole, with a zero destination and
// mask. The first IPv4 one wins, and an IPv6 one only where there is none, as
// on Linux.
func defaultOf(msgs []xroute.Message, nameOf func(index int) string) (defaultRoute, bool) {
	var v6 *defaultRoute
	for _, m := range msgs {
		rm, ok := m.(*xroute.RouteMessage)
		if !ok || len(rm.Addrs) <= syscall.RTAX_NETMASK {
			continue
		}
		if rm.Flags&syscall.RTF_UP == 0 || rm.Flags&(rtfIfScope|syscall.RTF_REJECT|syscall.RTF_BLACKHOLE) != 0 {
			continue
		}
		family, ok := zeroAddr(rm.Addrs[syscall.RTAX_DST])
		if !ok {
			continue
		}
		// No mask at all is a default too: the kernel may send none for a
		// destination of zero.
		mask := rm.Addrs[syscall.RTAX_NETMASK]
		if _, zero := zeroAddr(mask); mask != nil && !zero {
			continue
		}
		r := defaultRoute{iface: nameOf(rm.Index)}
		switch gw := rm.Addrs[syscall.RTAX_GATEWAY].(type) {
		case *xroute.Inet4Addr:
			r.gateway = stdnet.IP(gw.IP[:]).String()
		case *xroute.Inet6Addr:
			r.gateway = stdnet.IP(gw.IP[:]).String()
		}
		if family == syscall.AF_INET {
			return r, true
		}
		if v6 == nil {
			v6 = &r
		}
	}
	if v6 != nil {
		return *v6, true
	}
	return defaultRoute{}, false
}

// zeroAddr reports the family of an all-zero IPv4 or IPv6 address, which is
// what both the destination and the mask of a default route are.
func zeroAddr(a xroute.Addr) (family int, zero bool) {
	switch a := a.(type) {
	case *xroute.Inet4Addr:
		return syscall.AF_INET, a.IP == [4]byte{}
	case *xroute.Inet6Addr:
		return syscall.AF_INET6, a.IP == [16]byte{}
	}
	return 0, false
}
