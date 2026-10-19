package net

import (
	"encoding/hex"
	stdnet "net"
	"strconv"
	"strings"
)

// defaultRoute is where traffic goes when nothing more specific matches: the
// gateway (empty for a point-to-point link, which has none) and the interface
// that carries it. It is the answer to "is this machine on a network, and which
// one", which neither the interface list nor the resolver file can give: a VPN
// that took the default route and a laptop on the wrong wifi both have an
// address and a resolver.
type defaultRoute struct {
	gateway string
	iface   string
}

// The flags of a route in /proc/net/route and /proc/net/ipv6_route, from
// linux/route.h.
const (
	rtfUp          = 0x0001
	rtfReject      = 0x0200
	loopbackDevice = "lo"
)

// parseProcNetRoute reads Linux's IPv4 routing table and returns the default
// route in force: the one whose destination and mask are both zero and which is
// up, and of several the lowest metric, as the kernel chooses among them.
//
// The gateway is a hexadecimal word in the machine's own byte order, so 0102A8C0
// is 192.168.2.1 on the little-endian machines rta ships on (amd64 and arm64).
func parseProcNetRoute(text string) (defaultRoute, bool) {
	best, bestMetric, found := defaultRoute{}, uint64(0), false
	for line := range strings.SplitSeq(text, "\n") {
		f := strings.Fields(line)
		// Iface Destination Gateway Flags RefCnt Use Metric Mask …; the header
		// has "Iface" for its first word and no number in the second.
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&rtfUp == 0 {
			continue
		}
		metric, err := strconv.ParseUint(f[6], 10, 32)
		if err != nil {
			continue
		}
		gw, err := hex.DecodeString(f[2])
		if err != nil || len(gw) != 4 {
			continue
		}
		if found && metric >= bestMetric {
			continue
		}
		best, bestMetric, found = defaultRoute{iface: f[0]}, metric, true
		if ip := stdnet.IPv4(gw[3], gw[2], gw[1], gw[0]); !ip.Equal(stdnet.IPv4zero) {
			best.gateway = ip.String()
		}
	}
	return best, found
}

// parseProcNetIPv6Route is the same for /proc/net/ipv6_route, whose columns are
// destination and prefix length, source and prefix length, next hop, metric,
// reference count, use count, flags and device. A default route has a zero
// destination at prefix length zero. The kernel also keeps an unreachable
// default on the loopback with a reject flag, so that a lookup with no route
// fails fast, and that one is the absence of a route and not a route.
func parseProcNetIPv6Route(text string) (defaultRoute, bool) {
	const zero = "00000000000000000000000000000000"
	best, bestMetric, found := defaultRoute{}, uint64(0), false
	for line := range strings.SplitSeq(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[0] != zero || f[1] != "00" || f[9] == loopbackDevice {
			continue
		}
		flags, err := strconv.ParseUint(f[8], 16, 32)
		if err != nil || flags&rtfUp == 0 || flags&rtfReject != 0 {
			continue
		}
		metric, err := strconv.ParseUint(f[5], 16, 32)
		if err != nil {
			continue
		}
		if found && metric >= bestMetric {
			continue
		}
		best, bestMetric, found = defaultRoute{iface: f[9]}, metric, true
		if f[4] != zero && len(f[4]) == 32 {
			ip := make(stdnet.IP, 16)
			for i := range ip {
				b, err := strconv.ParseUint(f[4][2*i:2*i+2], 16, 8)
				if err != nil {
					return defaultRoute{}, false
				}
				ip[i] = byte(b)
			}
			best.gateway = ip.String()
		}
	}
	return best, found
}
