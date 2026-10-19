package net

import (
	"fmt"
	"syscall"
	"testing"

	xroute "golang.org/x/net/route"
)

func inet4(a, b, c, d byte) *xroute.Inet4Addr { return &xroute.Inet4Addr{IP: [4]byte{a, b, c, d}} }

func inet6(ip [16]byte) *xroute.Inet6Addr { return &xroute.Inet6Addr{IP: ip} }

func message(flags, index int, dst, gateway, mask xroute.Addr) *xroute.RouteMessage {
	addrs := make([]xroute.Addr, syscall.RTAX_MAX)
	addrs[syscall.RTAX_DST], addrs[syscall.RTAX_GATEWAY], addrs[syscall.RTAX_NETMASK] = dst, gateway, mask
	return &xroute.RouteMessage{Flags: flags, Index: index, Addrs: addrs}
}

func names(index int) string { return fmt.Sprintf("if%d", index) }

const (
	up      = syscall.RTF_UP
	viaGate = syscall.RTF_GATEWAY
)

// A Mac keeps a scoped default beside the real one for every interface that has
// a gateway, and the table lists them in no order worth trusting: the one that
// is the default route is the one that is not scoped.
func TestTheScopedDefaultsOfAMacAreNotTheDefaultRoute(t *testing.T) {
	msgs := []xroute.Message{
		message(up|viaGate|rtfIfScope, 17, inet4(0, 0, 0, 0), inet4(10, 8, 0, 1), inet4(0, 0, 0, 0)),
		message(up|viaGate, 14, inet4(0, 0, 0, 0), inet4(192, 168, 1, 254), inet4(0, 0, 0, 0)),
		message(up, 14, inet4(192, 168, 1, 0), inet4(192, 168, 1, 79), inet4(255, 255, 255, 0)),
	}
	got, ok := defaultOf(msgs, names)
	if !ok || got.gateway != "192.168.1.254" || got.iface != "if14" {
		t.Errorf("default route = %+v, %v; want 192.168.1.254 on if14", got, ok)
	}
}

func TestOnlyScopedOrDownOrRejectedDefaultsMeansNoDefaultRoute(t *testing.T) {
	msgs := []xroute.Message{
		message(up|viaGate|rtfIfScope, 17, inet4(0, 0, 0, 0), inet4(10, 8, 0, 1), inet4(0, 0, 0, 0)),
		message(viaGate, 14, inet4(0, 0, 0, 0), inet4(192, 168, 1, 254), inet4(0, 0, 0, 0)),
		message(up|viaGate|syscall.RTF_REJECT, 14, inet4(0, 0, 0, 0), inet4(192, 168, 1, 254), inet4(0, 0, 0, 0)),
		message(up|viaGate|syscall.RTF_BLACKHOLE, 14, inet4(0, 0, 0, 0), inet4(192, 168, 1, 254), inet4(0, 0, 0, 0)),
		message(up|viaGate, 14, inet4(0, 0, 0, 0), inet4(192, 168, 1, 254), inet4(128, 0, 0, 0)),
	}
	if got, ok := defaultOf(msgs, names); ok {
		t.Errorf("found %+v", got)
	}
	if got, ok := defaultOf(nil, names); ok {
		t.Errorf("an empty table has the route %+v", got)
	}
}

// IPv6 is the answer only where IPv4 has none, the order the resolver tries them
// in; a message with no mask at all is a default too, as the kernel may send it.
func TestAnIPv6DefaultIsTheRouteOnlyWhereThereIsNoIPv4OneAndAMaskMayBeAbsent(t *testing.T) {
	gw := [16]byte{0xfe, 0x80, 15: 1}
	v6 := message(up|viaGate, 14, inet6([16]byte{}), inet6(gw), nil)
	v4 := message(up|viaGate, 15, inet4(0, 0, 0, 0), inet4(192, 168, 1, 254), nil)

	got, ok := defaultOf([]xroute.Message{v6, v4}, names)
	if !ok || got.gateway != "192.168.1.254" || got.iface != "if15" {
		t.Errorf("default route = %+v, %v; want the IPv4 one", got, ok)
	}
	got, ok = defaultOf([]xroute.Message{v6}, names)
	if !ok || got.gateway != "fe80::1" || got.iface != "if14" {
		t.Errorf("default route = %+v, %v; want fe80::1 on if14", got, ok)
	}
}

// A default route that is a link and not a gateway (the kernel says "link#N")
// has an interface and no address to name.
func TestADefaultThroughALinkHasAnInterfaceAndNoGateway(t *testing.T) {
	m := message(up, 18, inet4(0, 0, 0, 0), &xroute.LinkAddr{Index: 18}, inet4(0, 0, 0, 0))
	got, ok := defaultOf([]xroute.Message{m}, names)
	if !ok || got.gateway != "" || got.iface != "if18" {
		t.Errorf("default route = %+v, %v; want if18 with no gateway", got, ok)
	}
}

// The live read, against this Mac's own table: shape only.
func TestTheLiveDefaultRouteIsReadWithoutAnError(t *testing.T) {
	if _, _, err := readDefaultRoute(); err != nil {
		t.Fatal(err)
	}
}
