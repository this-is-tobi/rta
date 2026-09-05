package net

import (
	"strings"
	"syscall"
	"testing"

	psnet "github.com/shirou/gopsutil/v4/net"

	"github.com/this-is-tobi/rta/pkg/view"
)

func tcp(status, ip string, port uint32) psnet.ConnectionStat {
	return psnet.ConnectionStat{
		Family: syscall.AF_INET, Type: syscall.SOCK_STREAM, Status: status,
		Laddr: psnet.Addr{IP: ip, Port: port}, Pid: 42,
	}
}

func udp(ip string, port uint32) psnet.ConnectionStat {
	return psnet.ConnectionStat{
		Family: syscall.AF_INET, Type: syscall.SOCK_DGRAM,
		Laddr: psnet.Addr{IP: ip, Port: port}, Pid: 42,
	}
}

// The find that shaped this capability: a bound UDP socket is in no state at
// all, so filtering on Status == "LISTEN" — the obvious way to write this —
// drops every UDP service on the machine. DNS, mDNS, DHCP, WireGuard and
// syslog all disappear from a view whose entire purpose is what this box has
// open, and nothing about the output says anything is missing.
//
// The two platforms do not even leave the same thing behind: darwin reports an
// empty status, Linux reports "NONE". Both are covered here, because reading
// Status for UDP at all is the mistake.
func TestAUDPSocketIsListeningDespiteHavingNoListeningState(t *testing.T) {
	for _, status := range []string{"", "NONE"} {
		c := udp("*", 5353)
		c.Status = status
		got, ok := listening(c)
		if !ok {
			t.Fatalf("a bound UDP socket with status %q was dropped", status)
		}
		if got.proto != "udp" {
			t.Errorf("proto = %q, want udp", got.proto)
		}
		if got.port != 5353 {
			t.Errorf("port = %d, want 5353", got.port)
		}
	}
}

// The other half of the same rule: UDP is decided by having no peer, so a
// connected UDP socket is somebody's outbound conversation and not an offer.
func TestAConnectedUDPSocketIsNotAListener(t *testing.T) {
	c := udp("192.168.1.5", 51234)
	c.Raddr = psnet.Addr{IP: "8.8.8.8", Port: 53}
	if _, ok := listening(c); ok {
		t.Error("a UDP socket with a peer was reported as listening")
	}
}

// TCP does have a state, and every other state is a connection rather than an
// offer.
func TestOnlyALISTENingTCPSocketIsAListener(t *testing.T) {
	if _, ok := listening(tcp("LISTEN", "0.0.0.0", 8080)); !ok {
		t.Error("a LISTEN socket was dropped")
	}
	for _, status := range []string{"ESTABLISHED", "TIME_WAIT", "CLOSE_WAIT", "SYN_SENT", ""} {
		if _, ok := listening(tcp(status, "10.0.0.1", 8080)); ok {
			t.Errorf("a %s socket was reported as listening", status)
		}
	}
}

// AF_INET6 is 30 on darwin and 10 on Linux. A literal here would label every
// IPv6 socket wrongly on one of the two platforms rta ships for, and the
// mistake renders as a plausible table rather than as an error.
func TestTheIPv6LabelComesFromThePlatformsOwnConstant(t *testing.T) {
	c := tcp("LISTEN", "::", 443)
	c.Family = syscall.AF_INET6
	got, ok := listening(c)
	if !ok {
		t.Fatal("an IPv6 listener was dropped")
	}
	if got.proto != "tcp6" {
		t.Errorf("proto = %q, want tcp6", got.proto)
	}
	if syscall.AF_INET6 == syscall.AF_INET {
		t.Fatal("AF_INET6 and AF_INET are equal on this platform, which makes this test vacuous")
	}
}

// A socket with no port is not an endpoint anybody can reach.
func TestASocketWithNoPortIsNotAListener(t *testing.T) {
	if _, ok := listening(tcp("LISTEN", "0.0.0.0", 0)); ok {
		t.Error("a portless socket was reported as listening")
	}
}

// Raw sockets and anything else the kernel offers are not TCP or UDP and have
// no place in a table of what this machine serves.
func TestASocketOfSomeOtherTypeIsIgnored(t *testing.T) {
	c := tcp("LISTEN", "0.0.0.0", 1)
	c.Type = syscall.SOCK_RAW
	if _, ok := listening(c); ok {
		t.Error("a raw socket was reported as listening")
	}
}

// The wildcard has three spellings across the platforms and they all mean the
// same exposure. Getting one wrong reports a socket reachable from anywhere as
// bound to a single address, which is the exact reading somebody opens this
// to check.
func TestEveryWildcardSpellingReadsAsAllInterfaces(t *testing.T) {
	for _, ip := range []string{"*", "0.0.0.0", "::", ""} {
		if got := reachOf(ip); got != reachAll {
			t.Errorf("reachOf(%q) = %q, want %q", ip, got, reachAll)
		}
	}
}

func TestLoopbackIsToldApartFromABoundAddress(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "127.0.0.53", "::1"} {
		if got := reachOf(ip); got != reachLoopback {
			t.Errorf("reachOf(%q) = %q, want %q", ip, got, reachLoopback)
		}
	}
	for _, ip := range []string{"192.168.1.5", "10.0.0.1", "fe80::1"} {
		if got := reachOf(ip); got != reachOne {
			t.Errorf("reachOf(%q) = %q, want %q", ip, got, reachOne)
		}
	}
}

// An address the parser cannot read is still bound to something, and calling
// it "all interfaces" would overstate what is exposed. Understating is the
// safe direction for a value nothing could interpret.
func TestAnUnparseableAddressIsNotCalledExposed(t *testing.T) {
	if got := reachOf("not-an-address"); got != reachOne {
		t.Errorf("reachOf(garbage) = %q, want %q", got, reachOne)
	}
}

// The empty answer has to answer the question that was asked. An empty table
// is the same answer in a shape nobody reads.
func TestTheEmptyAnswerRepeatsWhatWasAsked(t *testing.T) {
	if got := nothingListening(9, "udp"); !strings.Contains(got, "udp port 9") {
		t.Errorf("got %q, want it to name both filters", got)
	}
	if got := nothingListening(9, ""); !strings.Contains(got, "port 9") {
		t.Errorf("got %q, want it to name the port", got)
	}
	if got := nothingListening(0, "tcp"); !strings.Contains(got, "tcp") {
		t.Errorf("got %q, want it to name the protocol", got)
	}
	// The unfiltered case: a machine with nothing listening is rare enough
	// that the caller's own visibility is the likelier explanation, and this
	// is where somebody would otherwise conclude the box is quiet.
	got := nothingListening(0, "")
	if !strings.Contains(got, "other users") {
		t.Errorf("got %q, want it to doubt itself out loud", got)
	}
}

// A smoke test against the real socket table: it must not error, and what it
// returns must be one of the two shapes. Deliberately asserts nothing about
// the contents — a CI runner's listeners are its own business, and an
// assertion about them would fail for a property of the box.
func TestListingThisMachineReturnsATableOrSaysWhyNot(t *testing.T) {
	v, err := runListen(t.Context(), req(map[string]any{}))
	if err != nil {
		t.Fatalf("listing sockets: %v", err)
	}
	switch got := v.(type) {
	case view.Table:
		want := []string{"Proto", "Address", "Port", "Reach", "PID", "Process"}
		if len(got.Columns) != len(want) {
			t.Fatalf("columns = %d, want %d", len(got.Columns), len(want))
		}
		for i, c := range got.Columns {
			if c.Name != want[i] {
				t.Errorf("column %d = %q, want %q", i, c.Name, want[i])
			}
		}
		if got.Total != len(got.Rows) {
			t.Errorf("Total = %d, rows = %d", got.Total, len(got.Rows))
		}
		for _, row := range got.Rows {
			if len(row) != len(want) {
				t.Fatalf("row %v has %d cells, want %d", row, len(row), len(want))
			}
		}
	case view.Text:
		if got.Body == "" {
			t.Error("the empty answer said nothing")
		}
	default:
		t.Fatalf("view is %T, want a Table or a Text", v)
	}
}

// The port filter is the "who has 8080" question, and it has to actually
// narrow. Run against whatever the machine has: if it has no listeners at all
// there is nothing to filter and the test says so rather than passing hollow.
func TestThePortFilterNarrowsToOnePort(t *testing.T) {
	v, err := runListen(t.Context(), req(map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	all, ok := v.(view.Table)
	if !ok || len(all.Rows) == 0 {
		t.Skip("nothing is listening on this machine, so there is no port to filter to")
	}
	port := all.Rows[0][2]
	filtered, err := runListen(t.Context(), req(map[string]any{"port": atoi(t, port)}))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := filtered.(view.Table)
	if !ok {
		t.Fatalf("filtering to port %s found nothing, but it was just listed", port)
	}
	for _, row := range tbl.Rows {
		if row[2] != port {
			t.Errorf("port filter %s returned a row on port %s", port, row[2])
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("%q is not a number", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}
