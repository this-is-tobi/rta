package net

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"syscall"

	psnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Reach words. Plain text, deliberately not a KindStatus column: the theme
// grades "open" green, which is precisely backwards here, and no honest rule
// makes a wildcard bind amber on its own — a database bound to 0.0.0.0 behind
// a firewall is fine and a debug server bound to it on a laptop is not, and
// this capability cannot tell them apart. Grading exposure against a named
// control is what builtin/audit is for; this is the reading it would grade.
const (
	reachAll      = "all interfaces"
	reachLoopback = "loopback"
	reachOne      = "one address"
)

// socket is one listening endpoint, after the several file descriptors that
// can describe it have been collapsed into it.
type socket struct {
	proto string
	addr  string
	port  uint32
	reach string
	pid   int32
	owner string
}

func runListen(ctx context.Context, req plugin.Request) (view.View, error) {
	conns, err := psnet.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		return nil, view.Errorf("net.listen.enumerate", "reading the socket table: %v", err).
			WithHint("on macOS this reads through lsof, which ships at /usr/sbin/lsof — " +
				"on Linux it reads /proc/net directly and needs nothing")
	}

	wantPort := req.Int("port")
	wantProto := req.String("proto")

	seen := map[string]bool{}
	var rows []socket
	for _, c := range conns {
		s, ok := listening(c)
		if !ok {
			continue
		}
		if wantPort != 0 && int(s.port) != wantPort {
			continue
		}
		// tcp6 answers --proto tcp: the family is a column, not a protocol.
		if wantProto != "" && s.proto != wantProto && s.proto != wantProto+"6" {
			continue
		}
		key := s.proto + "|" + s.addr + "|" + strconv.Itoa(int(s.port)) + "|" + strconv.Itoa(int(s.pid))
		if seen[key] {
			continue
		}
		seen[key] = true
		rows = append(rows, s)
	}

	// Named once each, not once per socket: a process with eight listeners is
	// eight lookups of the same pid, and on Linux each one is a /proc read.
	names := map[int32]string{}
	for i, s := range rows {
		if s.pid == 0 {
			continue
		}
		name, ok := names[s.pid]
		if !ok {
			name = processName(ctx, s.pid)
			names[s.pid] = name
		}
		rows[i].owner = name
	}

	// By port, which is how somebody scans for the one they came about. Not by
	// reach, though putting the exposed ones on top is tempting: "is 8080
	// taken" is the other half of what this answers, and a table that reorders
	// itself by judgement is worse at it.
	slices.SortFunc(rows, func(a, b socket) int {
		if c := cmp.Compare(a.port, b.port); c != 0 {
			return c
		}
		if c := cmp.Compare(a.proto, b.proto); c != 0 {
			return c
		}
		return cmp.Compare(a.addr, b.addr)
	})

	t := view.Table{Columns: []view.Column{
		{Name: "Proto"},
		{Name: "Address"},
		{Name: "Port", Kind: view.KindNumber},
		{Name: "Reach"},
		{Name: "PID", Kind: view.KindNumber},
		{Name: "Process"},
	}}
	for _, s := range rows {
		pid := ""
		if s.pid != 0 {
			pid = strconv.Itoa(int(s.pid))
		}
		t.Rows = append(t.Rows, []string{
			s.proto, s.addr, strconv.Itoa(int(s.port)), s.reach, pid, s.owner,
		})
	}
	t.Total = len(t.Rows)
	if len(t.Rows) == 0 {
		return view.Text{Body: nothingListening(wantPort, wantProto)}, nil
	}
	return t, nil
}

// nothingListening answers the question that was asked rather than printing an
// empty table, which is the same answer in a shape nobody reads.
//
// The unfiltered case is the one worth wording carefully: a machine with no
// listening socket at all is rare enough that "none" is more likely to mean
// the caller could not see them than that there are none — which is exactly
// the platform limit the description names, arriving where somebody would
// otherwise conclude the box is quiet.
func nothingListening(port int, proto string) string {
	switch {
	case port != 0 && proto != "":
		return fmt.Sprintf("Nothing is listening on %s port %d.", proto, port)
	case port != 0:
		return fmt.Sprintf("Nothing is listening on port %d.", port)
	case proto != "":
		return fmt.Sprintf("Nothing is listening on %s.", proto)
	}
	return "Nothing is listening on this machine, which is unusual enough to be worth doubting: " +
		"sockets belonging to other users are not always visible to the caller."
}

// listening decides whether one socket is something this machine is offering,
// and describes it if so.
//
// The two protocols are decided differently, and the difference is not a
// shortcut. TCP has a listening state and says so. **UDP has none** — a bound
// UDP socket is in no state at all — and the field it leaves behind is not
// even consistently empty: darwin reports "", Linux reports "NONE". So the
// UDP answer never reads Status, and asks the only portable question there
// is: it is bound to a port and has no peer.
//
// That question has a real cost, stated in the capability's description
// rather than hidden here: an unconnected UDP socket looks identical whether
// it is a server waiting for datagrams or a resolver with a query in flight.
// The kernel does not distinguish them, netstat does not either, and the
// alternative — dropping UDP — would lose DNS, mDNS, DHCP, WireGuard and
// syslog from a view whose entire purpose is what this box has open.
func listening(c psnet.ConnectionStat) (socket, bool) {
	s := socket{addr: c.Laddr.IP, port: c.Laddr.Port, pid: c.Pid}
	if s.port == 0 {
		return socket{}, false
	}
	switch c.Type {
	case syscall.SOCK_STREAM:
		if c.Status != "LISTEN" {
			return socket{}, false
		}
		s.proto = "tcp"
	case syscall.SOCK_DGRAM:
		if c.Raddr.IP != "" {
			return socket{}, false
		}
		s.proto = "udp"
	default:
		return socket{}, false
	}
	// syscall.AF_INET6, never the number: it is 30 on darwin and 10 on Linux,
	// so a literal here would mislabel every IPv6 socket on one of them.
	if c.Family == syscall.AF_INET6 {
		s.proto += "6"
	}
	s.reach = reachOf(s.addr)
	return s, true
}

// reachOf says how far a bound address can be reached from.
//
// The wildcard has three spellings between the platforms — darwin's lsof
// prints "*", Linux prints 0.0.0.0 or :: — and they all mean the same thing,
// so all three are answered the same way.
func reachOf(ip string) string {
	switch ip {
	case "", "*":
		return reachAll
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return reachOne
	}
	switch {
	case a.IsUnspecified():
		return reachAll
	case a.IsLoopback():
		return reachLoopback
	default:
		return reachOne
	}
}

// processName resolves a pid to a command name, or to "" when it cannot.
//
// Empty rather than a placeholder or an error: the two platforms fail here in
// opposite directions and both are ordinary. On Linux the socket table is
// complete but the pid behind another user's socket needs a walk of their
// /proc entries, so the owner is what goes missing; on macOS lsof shows only
// what the caller may see, so the whole row does. Neither is a fault worth
// interrupting a table for.
func processName(ctx context.Context, pid int32) string {
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return ""
	}
	name, err := p.NameWithContext(ctx)
	if err != nil {
		return ""
	}
	return name
}
