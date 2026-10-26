// Package net is the built-in network diagnostics plugin: overview, ping,
// DNS lookups, TCP port scans and hosts-file listing. Zero configuration.
package net

import (
	"context"
	"errors"
	"fmt"
	stdnet "net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	probing "github.com/prometheus-community/pro-bing"
	gnet "github.com/shirou/gopsutil/v4/net"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// hostsFile, resolvConf and sysClassNet are vars so tests can point them at
// fixtures.
var (
	hostsFile  = "/etc/hosts"
	resolvConf = "/etc/resolv.conf"
	// sysClassNet is where Linux says what each interface is.
	sysClassNet = "/sys/class/net"
)

// Plugin returns the net plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "net",
		Summary: "Network diagnostics: ping, DNS, ports, hosts file",
		Capabilities: []plugin.Capability{
			{
				ID:           "net.overview",
				Summary:      "Local network overview: default route, traffic, DNS, hosts file, addresses",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Detailed:     true,
				Description: "Reports local facts only — it never calls external services, so no " +
					"public-IP lookup. A verdict first (`status`: ok, or no default route or no resolver), " +
					"then the default route, the traffic on it, the first resolver and how many fall back, " +
					"how many names the hosts file adds and how many are parked, and the IPv4 addresses, " +
					"the default route's interface first. Traffic is sampled over 500ms and leaves out the " +
					"loopback and, on Linux, the bridges and veth pairs of a container host. A proxy shows " +
					"when one is set, with its credentials masked. A line that cannot be read says so, and " +
					"so does the status. `numbers` carries the figures behind the lines for a program. " +
					"With `detail`: every interface with MAC, MTU, flags and addresses of both families, " +
					"per-interface traffic counters, the full resolver list, the proxy environment and " +
					"the hosts file.",
				Agent: "Local network state, read from this host alone: nothing leaves the machine. `status` is " +
					"ok, or fail for no default route or no resolver; then `route` (gateway and interface), " +
					"`traffic` (rx and tx per second over 500ms, leaving out the loopback and a container " +
					"host's bridges and veth pairs), `dns` (the first resolver and how many fall back), " +
					"`hosts` (names the hosts file adds, and how many are parked), `ip` (IPv4 addresses, the " +
					"default route's interface first) and `proxy` when one is set, credentials masked. A line " +
					"that cannot be read says so, and so does the status. `numbers` has the figures. `detail` " +
					"adds every interface, the IPv6 addresses, every resolver and the hosts file.",
				Run: runOverview,
			},
			{
				ID:      "net.listen",
				Summary: "What this machine is listening on, and which process owns it",
				// The other half of net.port, pointed inward: that one asks
				// what a host answers from outside, this one asks what this
				// host is offering — and nothing in rta joined a port to the
				// process holding it. sys.ps knows the processes and net.overview
				// knows the interfaces; the socket in between had no reader.
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Description: "Every TCP and UDP socket this machine has open for business, with the address it is " +
					"bound to, how far that reaches (loopback, one address, or all interfaces) and the " +
					"process behind it. Two limits. UDP has no listening state, so a socket with a port and " +
					"no peer is shown whether it is a server waiting or a resolver with a query in flight; " +
					"the kernel does not tell them apart. And the answer is only as complete as the caller's " +
					"privileges: Linux reads the whole socket table but can only name the process behind " +
					"sockets it may look into, so the Process column empties; macOS reads through lsof, which " +
					"shows only what the caller may see, so the row is absent entirely.",
				Inputs: []plugin.Field{
					{Name: "port", Type: plugin.Int, Min: 1, Max: 65535,
						Help: "only this port — the \"who has 8080\" question"},
					{Name: "proto", Type: plugin.String, Options: []string{"tcp", "udp"},
						Help: "only this protocol (tcp covers tcp6, udp covers udp6)"},
				},
				Run: runListen,
			},
			{
				ID:         "net.ping",
				Summary:    "Ping a host and report latency statistics",
				Safety:     plugin.Read,
				Idempotent: true,
				// The same reasoning net.probe states for itself: the host is
				// the caller's choice, so this is reconnaissance and a
				// connect/timeout oracle for an address the caller may not
				// otherwise be able to name, not a harmless read.
				NeedsGrant: true,
				Scope:      "host",
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "host to ping"},
					{Name: "count", Type: plugin.Int, Config: "ping.count", Default: 4, Min: 1, Max: 100, Help: "number of probes"},
					{Name: "timeout", Type: plugin.Duration, Config: "ping.timeout", Default: "10s", Min: "1s", Max: "5m", Help: "overall timeout"},
					{Name: "graph", Type: plugin.Bool, Help: "plot per-probe latency instead of summary stats"},
				},
				Run: runPing,
			},
			{
				ID:         "net.dns",
				Summary:    "Resolve DNS records for a name, or a name for an address",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				// The name is the same caller-chosen destination net.probe
				// needs a grant for — the answer, including whatever a TXT
				// record holds, arrives straight into an agent's context. The
				// resolver is a second, sharper edge on top: unscoped it
				// would let a caller send an arbitrary query to a resolver of
				// their own choosing and read back the reply, which is a
				// small oracle over anything that resolver can reach — so
				// --server stays a person's flag (Local) rather than a
				// caller-settable target, the same way `dns.server` already
				// configures it.
				NeedsGrant: true,
				Scope:      "name",
				Description: "A `type` of auto (the default) asks the question you usually mean: A, AAAA and CNAME for " +
					"a name, PTR for an address. The system resolver answers unless the operator has set " +
					"another. With `detail`: the query, the resolver that answered, and how long it took.",
				Inputs: []plugin.Field{
					{Name: "name", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "name or IP address to resolve"},
					{Name: "type", Type: plugin.String, Default: "auto",
						Options: append([]string{"auto"}, dnsTypes...),
						Help:    "which record type to ask for"},
					// Declared because the fallback would file `server` under target, the
					// block for what is selected inside a service: a resolver is where the
					// query goes.
					{Name: "server", Type: plugin.String, Config: "dns.server", Local: true, Group: "network", Suggest: suggestResolvers,
						Help: "resolver to query, e.g. 1.1.1.1 or 9.9.9.9:53 (default: the system resolver)"},
					{Name: "timeout", Type: plugin.Duration, Config: "dns.timeout", Default: "5s", Min: "1s", Max: "2m", Help: "query timeout"},
				},
				Run: runDNS,
			},
			{
				ID:         "net.trace",
				Summary:    "Trace the route to a host, hop by hop",
				Safety:     plugin.Read,
				Idempotent: true,
				// Same reasoning as net.probe and net.ping: the host is the
				// caller's choice, and the hop-by-hop answer is reconnaissance
				// of whatever this machine can reach, not a harmless read.
				NeedsGrant: true,
				Scope:      "host",
				Description: "ICMP echo probes with a rising TTL: each hop that expires one reports " +
					"itself. Answers the question ping cannot — not just \"is it reachable\" but " +
					"\"where does it stop\". Silent hops show as *; that is a router declining to " +
					"reply, not necessarily a fault.",
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "host to trace"},
					{Name: "max-hops", Type: plugin.Int, Config: "trace.max-hops", Default: 30, Min: 1, Max: 64, Help: "stop after this many hops"},
					{Name: "probes", Type: plugin.Int, Config: "trace.probes", Default: 3, Min: 1, Max: 10, Help: "probes per hop"},
					{Name: "timeout", Type: plugin.Duration, Config: "trace.timeout", Default: "2s", Min: "1s", Max: "1m", Help: "timeout per probe"},
					{Name: "resolve", Type: plugin.Bool, Config: "trace.resolve", Default: true, Help: "resolve hop addresses back to names"},
				},
				Run: runTrace,
			},
			{
				ID:         "net.probe",
				Summary:    "Open a TCP connection and report what answers",
				Safety:     plugin.Read,
				Idempotent: true,
				// It only ever listens, which is what earned it Read — but the
				// host is the caller's choice and whatever answers arrives
				// straight into an agent's context, the exact shape http.get
				// carries NeedsGrant for despite also mutating nothing. Shipped
				// without it: an ungated MCP caller could dial any address this
				// machine can reach — including loopback and RFC1918/link-local,
				// unlike http.* — and read back whatever greeted it.
				NeedsGrant: true,
				Scope:      "host",
				Description: "Proves the port accepts a connection, times the handshake, and shows the " +
					"banner the service volunteers. " +
					"`tls` completes a TLS handshake first and reports the negotiated version and " +
					"cipher. It sends nothing and only waits for the banner; to speak first — the protocols that expect the " +
					"client to open — use `net.send`, which is a write. Both need a grant: the " +
					"destination is the caller's choice either way, and whatever it says back is " +
					"read as tool output.",
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "host to connect to"},
					{Name: "port", Type: plugin.Int, Positional: true, Required: true, Min: 1, Max: 65535, Help: "port to connect to"},
					{Name: "tls", Type: plugin.Bool,
						Help: "complete a TLS handshake after connecting, in whatever protocol the host speaks, TLS 1.0 on"},
					{Name: "timeout", Type: plugin.Duration, Config: "probe.timeout", Default: "5s", Min: "1s", Max: "2m", Help: "connect timeout"},
					{Name: "wait", Type: plugin.Duration, Default: "2s", Min: "1s", Max: "2m", Help: "how long to wait for the service to say something"},
				},
				Run: runProbe,
			},
			{
				ID:      "net.send",
				Summary: "Send bytes to a TCP port and report what comes back",
				// Writing attacker-chosen bytes to an arbitrary port is a
				// remote write primitive, and a strictly more capable one than
				// any request the http client makes: "FLUSHALL\r\n" to 6379
				// empties a Redis, and a hand-rolled "DELETE /orders/7
				// HTTP/1.0" performs exactly the request http.delete is
				// destructive for. A class is the widest thing a capability can
				// be asked to do, so this one is destructive as well: a write
				// beside a destructive DELETE told every client that the more
				// capable of the two only ever adds. It shipped as Read, which
				// put it on every MCP server with no flag, no grant, and a
				// readOnlyHint the client was entitled to believe.
				Safety: plugin.Destructive,
				Scope:  "host",
				Description: "Opens a TCP connection, writes what you give it, and shows the reply — " +
					"how you poke a protocol that expects the client to speak first (SMTP, Redis, " +
					"HTTP/1.0). \\r\\n and \\n in `data` are interpreted. `tls` speaks it over TLS. " +
					"What arrives is executed by whatever is listening, and FLUSHALL empties a Redis, " +
					"so it is destructive: a person is asked before it sends, and an AI agent needs an " +
					"explicit grant. `net.probe` is the read-only half — connect, handshake, listen — " +
					"and carries the same grant, since the destination is still the caller's to " +
					"choose either way.",
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "host to connect to"},
					{Name: "port", Type: plugin.Int, Positional: true, Required: true, Min: 1, Max: 65535, Help: "port to connect to"},
					{Name: "data", Type: plugin.String, Required: true, Help: `bytes to send, e.g. "GET / HTTP/1.0\r\n\r\n"`},
					{Name: "tls", Type: plugin.Bool,
						Help: "complete a TLS handshake before sending, in TLS 1.2 or newer since the payload crosses it"},
					{Name: "timeout", Type: plugin.Duration, Config: "send.timeout", Default: "5s", Min: "1s", Max: "2m", Help: "connect timeout"},
					{Name: "wait", Type: plugin.Duration, Default: "2s", Min: "1s", Max: "2m", Help: "how long to wait for a reply"},
				},
				Run: runSend,
			},
			{
				ID:         "net.port",
				Summary:    "TCP connect-scan ports on a host",
				Safety:     plugin.Read,
				Idempotent: true,
				// Same reasoning as net.probe: the host is the caller's choice,
				// and an open/closed map of an arbitrary address — including
				// one this machine can reach but the caller never should have
				// been able to name — is reconnaissance, not a harmless read.
				NeedsGrant: true,
				Scope:      "host",
				Description: "Which ports answer, not what they say — `net.probe` shows the banner " +
					"for one; this maps many at once. Needs a grant for the same reason: the host " +
					"is the caller's choice.",
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "host to scan"},
					{Name: "ports", Type: plugin.String, Default: "22,80,443",
						Help: "ports and ranges, comma-separated: 22,80,8000-8010 (up to 4096 at a time)"},
					{Name: "timeout", Type: plugin.Duration, Config: "port.timeout", Default: "2s", Min: "1s", Max: "1m", Help: "timeout per port"},
				},
				Run: runPort,
			},
			{
				ID:           "net.hosts.list",
				Summary:      "List hosts-file entries, including disabled ones",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Description: "One row per hostname, since a hostname is what you look up, disable and " +
					"remove. Entries commented out by `net.hosts.toggle` are shown as disabled rather " +
					"than hidden as comments.",
				Inputs: []plugin.Field{
					{Name: "file", Type: plugin.Path, Help: "operate on this file instead of the system one (a container's, a chroot's)"},
				},
				// The hosts file is a list you manage, not just read: park an override
				// with `t`, drop it with `x`. `t` rather than `d` — d is "done" on the
				// task lists, and a key that means two things across two screens is a key
				// you hesitate over.
				Actions: []plugin.Action{
					{Key: "a", Label: "add", Target: "net.hosts.add"},
					{Key: "t", Label: "toggle", Target: "net.hosts.toggle", Source: plugin.ActionRow},
					{Key: "x", Label: "remove", Target: "net.hosts.rm", Source: plugin.ActionRow},
				},
				Run: runHostsList,
			},
			{
				ID:           "net.hosts.add",
				Flash:        true,
				Summary:      "Point a hostname at an address in the hosts file",
				Safety:       plugin.Destructive,
				HostSpecific: true,
				Scope:        "hostname",
				Description: "A name resolves to one address, so this takes the name away from " +
					"any other active entry rather than leaving two that race. Everything else in the " +
					"file — comments, ordering, spacing — survives untouched, and the previous version " +
					"is saved before the write.\n\n" +
					"Classified destructive rather than write: this rewrites a root-owned file that " +
					"governs name resolution for every process on the machine, and a wrong entry " +
					"silently reroutes traffic instead of failing. Over MCP that means a per-capability " +
					"grant naming this capability, or the net plugin, with a deadline on it.",
				Inputs: []plugin.Field{
					{Name: "ip", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestAddresses, Help: "address to point at"},
					{Name: "hostname", Type: plugin.StringSlice, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "hostnames to map"},
					// Local here and on the three capabilities below it that also
					// write, and deliberately not on net.hosts.list or
					// net.resolver.list. Over MCP, an operator who allowlists
					// net.hosts.add and grants one hostname is consenting to a
					// hosts-file entry; with the path coming from the caller they
					// got an appended line in any file this process can write —
					// ~/.zshrc, an authorized_keys, a crontab — which is a
					// different capability wearing this one's name. Reading a
					// container's or a chroot's hosts file is the ordinary sysadmin
					// job the flag exists for and stays open to everyone.
					{Name: "file", Type: plugin.Path, Local: true, Help: "operate on this file instead of the system one (a container's, a chroot's)"},
				},
				Run: runHostsAdd,
			},
			{
				ID:           "net.hosts.toggle",
				Flash:        true,
				Summary:      "Enable or disable a hosts-file entry without deleting it",
				Safety:       plugin.Destructive,
				HostSpecific: true,
				Scope:        "hostname",
				Description: "A disabled entry stays in the file, commented, and comes back with the " +
					"same command. Same blast radius as add, same classification.",
				Inputs: []plugin.Field{
					{Name: "hostname", Type: plugin.String, Positional: true, Required: true, Help: "hostname to toggle",
						Suggest: suggestHostnames},
					{Name: "file", Type: plugin.Path, Local: true, Help: "operate on this file instead of the system one (a container's, a chroot's)"},
				},
				Run: runHostsToggle,
			},
			{
				ID:           "net.hosts.rm",
				Flash:        true,
				Summary:      "Remove hostnames from the hosts file",
				Safety:       plugin.Destructive,
				HostSpecific: true,
				Scope:        "hostname",
				Description: "Removes the names, and the line only if nothing is left on it. " +
					"`net.hosts.toggle` parks an entry instead, which is usually what you want.",
				Inputs: []plugin.Field{
					{Name: "hostname", Type: plugin.StringSlice, Positional: true, Required: true, Help: "hostnames to remove",
						Suggest: suggestHostnames},
					{Name: "file", Type: plugin.Path, Local: true, Help: "operate on this file instead of the system one (a container's, a chroot's)"},
				},
				Run: runHostsRemove,
			},
			{
				ID:      "net.resolver.list",
				Summary: "Show the system resolver, and who controls it", Keywords: []string{"dns", "nameserver", "nameservers", "servers", "resolv"},
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Description: "resolv.conf is the file people most often edit to no effect: on most modern " +
					"systems it is generated and replaced. This leads with who owns it, because that " +
					"decides whether anything else in it is worth changing.",
				Inputs: []plugin.Field{
					{Name: "file", Type: plugin.Path, Help: "operate on this file instead of the system one (a container's, a chroot's)"},
				},
				Run: runResolverList,
			},
			{
				ID:           "net.resolver.set",
				Summary:      "Set the system nameservers",
				Safety:       plugin.Destructive,
				HostSpecific: true,
				Scope:        "server",
				Description: "Replaces the nameserver lines, leaving search domains, options and comments " +
					"where they are, after saving the previous version. Refuses when the file is " +
					"generated by something else — silently editing a generated file is worse than " +
					"refusing, since the change works and then vanishes with nothing to point at. " +
					"`force` overrides that, with the warning repeated in the result.",
				Inputs: []plugin.Field{
					{Name: "server", Type: plugin.StringSlice, Positional: true, Required: true, Suggest: suggestResolvers,
						Help: "nameserver addresses, in failover order"},
					{Name: "force", Type: plugin.Bool, Help: "write even when the file is machine-generated"},
					{Name: "file", Type: plugin.Path, Local: true, Help: "operate on this file instead of the system one (a container's, a chroot's)"},
				},
				Run: runResolverSet,
			},
		},
	}
}

func runPing(ctx context.Context, req plugin.Request) (view.View, error) {
	host := req.String("host")
	if verr := dottedNumber(host); verr != nil {
		return nil, verr
	}
	pinger, err := probing.NewPinger(host)
	if err != nil {
		return nil, namingHost(resolveFailed("net.ping.resolve", req.Surface(), host, err), err)
	}
	pinger.Count = req.Int("count")
	pinger.Timeout = req.Duration("timeout")
	// Unprivileged UDP ping works without root on macOS and (with sysctl
	// ping_group_range) on Linux.
	pinger.SetPrivileged(false)

	if err := pinger.RunWithContext(ctx); err != nil {
		return nil, view.Errorf("net.ping.failed", "pinging %s: %v", host, err).
			WithHint("on Linux, unprivileged ping may need: sysctl -w net.ipv4.ping_group_range=\"0 2147483647\"")
	}
	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return nil, view.Errorf("net.ping.unreachable", "%s: no reply to %d probes", host, stats.PacketsSent).
			WithHint("host may be down, or ICMP may be filtered")
	}
	if req.Bool("graph") {
		points := make([]float64, 0, len(stats.Rtts))
		for _, rtt := range stats.Rtts {
			points = append(points, float64(rtt.Microseconds())/1000)
		}
		return view.Chart{
			Kind:   view.ChartLine,
			Unit:   "ms",
			Series: []view.Series{{Name: "rtt " + host, Points: points}},
		}, nil
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "host", Value: fmt.Sprintf("%s (%s)", host, stats.IPAddr)},
		{Key: "sent/recv", Value: fmt.Sprintf("%d/%d (%.0f%% loss)", stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss)},
		{Key: "min", Value: stats.MinRtt.Round(time.Microsecond).String()},
		{Key: "avg", Value: stats.AvgRtt.Round(time.Microsecond).String()},
		{Key: "max", Value: stats.MaxRtt.Round(time.Microsecond).String()},
		{Key: "stddev", Value: stats.StdDevRtt.Round(time.Microsecond).String()},
	}}, nil
}

// dnsTypes are the record types net.dns knows how to ask for. "auto" is not
// listed: it expands to a set, chosen from the shape of the query.
var dnsTypes = []string{"A", "AAAA", "CNAME", "MX", "NS", "TXT", "PTR", "SRV"}

// resolverFor builds the resolver to query and a label naming it. An explicit
// server bypasses the system resolver entirely (PreferGo), which is what
// makes "the record is wrong" distinguishable from "my resolver is stale".
func resolverFor(server string) (*stdnet.Resolver, string) {
	if server == "" {
		return &stdnet.Resolver{}, "system"
	}
	addr := server
	if _, _, err := stdnet.SplitHostPort(addr); err != nil {
		addr = stdnet.JoinHostPort(server, "53")
	}
	return &stdnet.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (stdnet.Conn, error) {
			return (&stdnet.Dialer{}).DialContext(ctx, network, addr)
		},
	}, addr
}

// autoTypes picks the records that answer "where does this point": a PTR for
// an address, the address records plus any alias for a name.
func autoTypes(name string) []string {
	if stdnet.ParseIP(name) != nil {
		return []string{"PTR"}
	}
	return []string{"A", "AAAA", "CNAME"}
}

// lookup asks for one record type and appends what it finds. Errors are
// returned rather than aggregated: a multi-type query tolerates misses (most
// names have no CNAME), a single-type query does not.
func lookup(ctx context.Context, r *stdnet.Resolver, rtype, name string, add func(string, string)) error {
	switch rtype {
	case "A", "AAAA":
		network := "ip4"
		if rtype == "AAAA" {
			network = "ip6"
		}
		ips, err := r.LookupIP(ctx, network, name)
		for _, ip := range ips {
			add(rtype, ip.String())
		}
		return err
	case "CNAME":
		cname, err := r.LookupCNAME(ctx, name)
		// A name with no alias resolves to itself; that is not a CNAME.
		if err == nil && !strings.EqualFold(strings.TrimSuffix(cname, "."), strings.TrimSuffix(name, ".")) {
			add("CNAME", cname)
		}
		return err
	case "PTR":
		names, err := r.LookupAddr(ctx, name)
		for _, n := range names {
			add("PTR", n)
		}
		return err
	case "MX":
		mxs, err := r.LookupMX(ctx, name)
		for _, mx := range mxs {
			add("MX", fmt.Sprintf("%d %s", mx.Pref, mx.Host))
		}
		return err
	case "NS":
		nss, err := r.LookupNS(ctx, name)
		for _, ns := range nss {
			add("NS", ns.Host)
		}
		return err
	case "TXT":
		txts, err := r.LookupTXT(ctx, name)
		for _, txt := range txts {
			add("TXT", txt)
		}
		return err
	case "SRV":
		// The name is expected in _service._proto.domain form, which
		// LookupSRV accepts wholesale when service and proto are empty.
		_, srvs, err := r.LookupSRV(ctx, "", "", name)
		for _, s := range srvs {
			add("SRV", fmt.Sprintf("%d %d %d %s", s.Priority, s.Weight, s.Port, s.Target))
		}
		return err
	}
	return view.Errorf("net.dns.badtype", "unsupported record type %q", rtype).
		WithHint("use auto, " + strings.Join(dnsTypes, ", "))
}

// isNotFound reports that the resolver answered and the answer was "no such
// name" — as opposed to every other error, which means it did not answer.
func isNotFound(err error) bool {
	var d *stdnet.DNSError
	return errors.As(err, &d) && d.IsNotFound
}

// anotherQuestion is what a name with no records is asked next, named the way
// sf, the surface asking, gives it: another record type, or another resolver.
// The resolver is Local, so no agent's schema has it, and over MCP it is the
// operator's to change — a hint naming a server argument sent the agent after
// one its tool does not take.
func anotherQuestion(sf plugin.Surface) string {
	hint := "try another record type in " + sf.InputName("type") + " (" + strings.Join(dnsTypes, ", ") + ")"
	if sf == plugin.SurfaceMCP {
		return hint + " — which resolver answers is the operator's to change"
	}
	return hint + ", or another resolver in " + sf.InputName("server")
}

func runDNS(ctx context.Context, req plugin.Request) (view.View, error) {
	// Refused, not trimmed. The name is this capability's record, and the
	// gate judged it as the call spelled it: " example.com" is not the record
	// a grant on example.com covers, nor the one a person approving it read,
	// and trimming it here asked the resolver about a name nobody allowed. No
	// name or address has white space around it, so there is nothing a
	// caller could have meant that this refuses.
	name := req.String("name")
	if name != strings.TrimSpace(name) {
		return nil, view.Errorf("net.dns.badname", "%q has white space around it, which no name or address has", name).
			WithHint("give the name as it is written, with nothing around it").WithInput("name")
	}
	rtype := strings.ToUpper(strings.TrimSpace(req.String("type")))
	if rtype == "" {
		rtype = "AUTO"
	}
	if timeout := req.Duration("timeout"); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	resolver, label := resolverFor(req.String("server"))

	types := []string{rtype}
	auto := rtype == "AUTO"
	if auto {
		types = autoTypes(name)
	}

	t := view.Table{Columns: []view.Column{{Name: "Type"}, {Name: "Value"}}}
	add := func(rtype, value string) { t.Rows = append(t.Rows, []string{rtype, value}) }

	start := time.Now()
	var lastErr error
	failures := map[string]error{}
	for _, rt := range types {
		if err := lookup(ctx, resolver, rt, name, add); err != nil {
			// An unknown type is a usage error whatever else happens.
			var ve *view.Error
			if errors.As(err, &ve) {
				return nil, ve.WithInput("type")
			}
			lastErr = err
			failures[rt] = err
		}
	}
	elapsed := time.Since(start)

	// Nothing came back, and whether that is an answer or a failure to get one
	// is the whole difference between "this name has no AAAA record" and "this
	// machine could not ask". Reported as the first, the second is a
	// diagnostic tool saying a name does not resolve when it never found out
	// — measured in a `--network none` container, where `net dns example.com`
	// answered "no A/AAAA/CNAME records for example.com".
	//
	// `auto` used to suppress the error entirely, on reasoning that holds for
	// a partial miss and not for this one: with no rows at all every type
	// failed, so the errors are not the misses that come with asking broadly,
	// they are the whole answer. A name that genuinely does not exist keeps
	// the old wording, because a name that does not exist really has no
	// records — that is an answer, and the resolver gave it.
	if len(t.Rows) == 0 {
		if lastErr != nil && !isNotFound(lastErr) {
			return nil, view.Errorf("net.dns.failed", "resolving %s %s: %v",
				strings.Join(types, "/"), name, lastErr)
		}
		return nil, view.Errorf("net.dns.norecords", "no %s records for %s", strings.Join(types, "/"), name).
			WithHint(anotherQuestion(req.Surface()))
	}
	// **One type answering does not make another type's failure stop
	// mattering.** Reaching the line above means something resolved, and
	// until now that was the end of it: a real failure on one of the other
	// types — a timeout, a SERVFAIL, the shared deadline expiring mid-query
	// — lived in lastErr, which is read only when nothing came back at all.
	// So a name whose A record resolved while its AAAA lookup failed
	// produced a clean table of A records, on the one tool somebody reaches
	// for *because* resolution is behaving oddly.
	//
	// Appended after the empty check, so the "every type failed" path above
	// still reports an error rather than a table of apologies.
	answers := len(t.Rows)
	t.Rows = append(t.Rows, dnsFailureRows(failures, types)...)
	t.Total = len(t.Rows)
	if !req.Bool("detail") {
		return t, nil
	}
	return view.Sections{Items: []view.Section{
		{ID: "query", Title: "query", View: view.KeyValue{Pairs: []view.Pair{
			{Key: "name", Value: name},
			{Key: "type", Value: strings.Join(types, ", ")},
			{Key: "resolver", Value: label},
			{Key: "elapsed", Value: elapsed.Round(time.Millisecond).String()},
			{Key: "records", Value: strconv.Itoa(answers)},
		}}},
		{ID: "answers", Title: "answers", View: t},
	}}, nil
}

// dnsFailureRows is what the types that could not be asked contribute to the
// answer table.
//
// A miss is not a failure and gets no row: asking broadly means asking for
// records that are not there, and a name with no AAAA record really has no
// AAAA record — the resolver answered. isNotFound is what tells that apart
// from never having found out.
//
// Its own function, in the order the query asked, so the decision is
// testable without a resolver that fails on demand — the same reason
// activitySQL is split out in plugins/pg.
func dnsFailureRows(failures map[string]error, types []string) [][]string {
	var out [][]string
	for _, rt := range types {
		if err := failures[rt]; err != nil && !isNotFound(err) {
			out = append(out, []string{rt, "lookup failed — " + err.Error()})
		}
	}
	return out
}

// maxScanPorts bounds what one call may ask for.
//
// `--ports 1-65535` is seven bytes. It bought 65,535 goroutines and ~180 MiB
// before the first dial — and a grant authorizes a host, not a ports value,
// so even a caller already holding one for this host can still pass an
// unbounded --ports spec (and the CLI/TUI are never gated at all), so that
// was an agent allocating a sixth of a gigabyte per call inside the process
// that enforces every grant for every other tool the agent has open.
//
// The cap is not redundant with the worker pool below, and it is the half that
// bounds the *unbounded* cost. The goroutine spike has a ceiling and drains on
// its own; parsePorts' expansion has neither. It runs on the calling goroutine
// and takes no context, so there is nothing for a client cancellation, a
// closed stdio pipe or a killed session to interrupt — measured at ~55µs per
// argument byte, which makes a 1 MB `ports` value roughly a minute of
// uninterruptible single-threaded work, and 10 MB about nine. The dedup map
// keeps resident memory flat at 65,535 entries throughout, so every
// memory-shaped guard is blind to it.
//
// A sweep wider than this is nmap's job, not a diagnostics tile's.
const maxScanPorts = 4096

// portScanWorkers is how many dials are in flight at once — the number the
// old semaphore claimed to enforce and now actually does.
const portScanWorkers = 64

// errTooManyPorts is a sentinel rather than a plain message because the two
// refusals need different hints. `net.port.badspec` says "example: --ports
// 22,80,8000-8010", which is exactly the advice somebody who asked for 1-65535
// does not need — it tells them to do the thing that was just refused.
var errTooManyPorts = fmt.Errorf("more than %d ports requested", maxScanPorts)

// lookupPortHost is how net.port resolves the host it scans, a var so a test
// can answer for a name without a resolver.
var lookupPortHost = stdnet.DefaultResolver.LookupIPAddr

// portLookupTimeout is the floor of the one lookup a scan makes, the default
// net.dns gives a query. A port's timeout is for the port; a resolver on the
// far side of a VPN can take longer than two seconds for a first answer, and
// the scan it held up is as good once it has one.
const portLookupTimeout = 5 * time.Second

// parsePorts expands "22,80,8000-8010" into a sorted port list.
func parsePorts(spec string) ([]int, error) {
	seen := map[int]bool{}
	// Counted before expanding rather than checked afterwards, which is what
	// makes it a bound on the CPU and not only on the result: `len(seen)` is
	// deduplicated, so "1-65535,1-65535,…" costs 65,535 map writes per
	// repeated part while the count never moves.
	expanded := 0
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, found := strings.Cut(part, "-")
		start, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, fmt.Errorf("invalid port %q", part)
		}
		end := start
		if found {
			if end, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return nil, fmt.Errorf("invalid range %q", part)
			}
		}
		// Two different mistakes, two sentences. "Out of bounds" for a range
		// typed high to low (`8010-8000`) sent the reader to check numbers
		// that are all valid ports.
		switch {
		case end < start:
			return nil, fmt.Errorf("port range %q runs backwards — write it low-high", part)
		case start < 1 || end > 65535:
			return nil, fmt.Errorf("%q is not within the ports 1 to 65535", part)
		}
		expanded += end - start + 1
		if expanded > maxScanPorts {
			return nil, errTooManyPorts
		}
		for p := start; p <= end; p++ {
			seen[p] = true
		}
	}
	ports := make([]int, 0, len(seen))
	for p := range seen {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports, nil
}

func runPort(ctx context.Context, req plugin.Request) (view.View, error) {
	host := req.String("host")
	if verr := dottedNumber(host); verr != nil {
		return nil, verr
	}
	ports, err := parsePorts(req.String("ports"))
	if errors.Is(err, errTooManyPorts) {
		return nil, view.Errorf("net.port.toomany", "%v", err).
			WithHint(fmt.Sprintf("scan up to %d ports at a time — this is a diagnostic, and a "+
				"full sweep is nmap's job", maxScanPorts)).WithInput("ports")
	}
	if err != nil {
		return nil, view.Errorf("net.port.badspec", "%v", err).
			WithHint(req.Surface().InputName("ports") + " takes a list such as 22,80,8000-8010").WithInput("ports")
	}
	if len(ports) == 0 {
		return nil, view.Errorf("net.port.empty", "no ports to scan").WithInput("ports")
	}
	timeout := req.Duration("timeout")

	// **One lookup for the scan, before any port is tried.** Each port's dial
	// looked the name up again, inside that port's timeout: up to 4096 queries
	// for one call, the ports of a round-robin name tried at whichever address
	// each lookup gave, and a resolver slow to answer the first query failed
	// the first wave of ports in the lookup while later ones were answered from
	// its cache. Those ports read "closed", next to rows that showed the name
	// resolved, and a lookup that failed is no answer from a port. Resolved
	// once, a name that does not resolve is refused as one, and every row is
	// the answer of the addresses it gave.
	lctx, cancel := context.WithTimeout(ctx, max(timeout, portLookupTimeout))
	ips, err := lookupPortHost(lctx, host)
	cancel()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(ips) == 0 {
		return nil, namingHost(resolveFailed("net.port.resolve", req.Surface(), host, err), err)
	}

	type result struct {
		port int
		open bool
	}
	results := make([]result, len(ports))
	// A worker pool, not a goroutine per port that then queues on a semaphore.
	// The old shape spawned every goroutine first and made them wait, so the
	// bound applied to the concurrent *dials* and the fan-out was the port
	// count — and the comment beside it said "bounded concurrency", which is
	// the bound it did not provide. Against a filtered host the parked
	// goroutines do not retire until they get through, so the memory was held
	// for the whole scan rather than spiked and released.
	//
	// Feeding the channel under a ctx select also means a cancelled scan stops
	// at the current batch instead of walking the rest of the list to hand
	// each remaining port an already-dead context.
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(portScanWorkers, len(ports)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				// Written by index, so the rows keep the caller's port order
				// without a sort and without a lock.
				results[i] = result{port: ports[i], open: scanDialer.answers(ctx, ips, ports[i], timeout)}
			}
		}()
	}
feed:
	for i := range ports {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Port", Kind: view.KindNumber},
		{Name: "State", Kind: view.KindStatus},
	}}
	openCount := 0
	for _, r := range results {
		state := "closed"
		if r.open {
			state = "open"
			openCount++
		}
		t.Rows = append(t.Rows, []string{strconv.Itoa(r.port), state})
	}
	t.Total = len(t.Rows)
	return t, nil
}

// portDialer tries a port at the addresses its host resolved to the way
// net.Dialer tries the addresses of a name it resolves itself, which is how
// every port was dialled before a scan looked its host up once. A Dialer
// cannot be handed addresses, only a name it would resolve again, so the two
// of its rules that decide whether a port reads open are kept here, unchanged.
//
// **Each address gets at least two seconds while the timeout lasts.** The
// timeout is shared among the addresses not yet tried, and a share under two
// seconds is raised to two, or to all that is left, so the first addresses
// take time from the last. An even split with no floor would give each of a
// name's four addresses 250ms of net.port's 1s minimum, and an
// intercontinental round trip, or a first SYN lost and resent after a second,
// would read "closed" on a port that a dial by name reads open with the whole
// second.
//
// **The other address family is raced, 300ms after the first began or as soon
// as it has failed.** Tried in turn, a name whose IPv6 route drops packets
// would spend the whole timeout on its AAAA address and never try its IPv4
// one: the port would read closed where a dial by name reads it open.
//
// dial, now and fallbackDelay are fields so a test can stand in for the
// network and the clock, and see the deadline each address is given.
type portDialer struct {
	dial          func(ctx context.Context, network, address string) (stdnet.Conn, error)
	now           func() time.Time
	fallbackDelay time.Duration
}

// scanDialer is the portDialer net.port dials with, on net.Dialer's values.
var scanDialer = portDialer{
	dial:          new(stdnet.Dialer).DialContext,
	now:           time.Now,
	fallbackDelay: 300 * time.Millisecond,
}

// minAddressShare is the least of a port's timeout one address is given while
// that much is left: net.Dialer's own floor.
const minAddressShare = 2 * time.Second

// answers reports whether port accepts a connection at any of ips within
// timeout. ips are in the order the resolver gave them, which puts the family
// it prefers first.
func (d portDialer) answers(ctx context.Context, ips []stdnet.IPAddr, port int, timeout time.Duration) bool {
	ctx, cancel := context.WithDeadline(ctx, d.now().Add(timeout))
	// Cancelled on return, which also stops a racer still dialling: its
	// result goes to a channel with room for it that nobody reads.
	defer cancel()
	// The sooner of the port's deadline and the caller's, since what is
	// shared out is the time the call has left, as the dialler shares it.
	deadline, _ := ctx.Deadline()
	var primaries, fallbacks []stdnet.IPAddr
	for _, ip := range ips {
		if (ip.IP.To4() != nil) == (ips[0].IP.To4() != nil) {
			primaries = append(primaries, ip)
		} else {
			fallbacks = append(fallbacks, ip)
		}
	}
	if len(fallbacks) == 0 {
		return d.inTurn(ctx, deadline, primaries, port)
	}
	type outcome struct{ primary, open bool }
	results := make(chan outcome, 2)
	race := func(family []stdnet.IPAddr, primary bool) {
		results <- outcome{primary: primary, open: d.inTurn(ctx, deadline, family, port)}
	}
	go race(primaries, true)
	fallback := time.NewTimer(d.fallbackDelay)
	defer fallback.Stop()
	failed := 0
	for {
		select {
		case <-fallback.C:
			go race(fallbacks, false)
		case r := <-results:
			if r.open {
				return true
			}
			if failed++; failed == 2 {
				return false
			}
			// Stop reports that the other family had not begun: the first
			// failed inside its 300ms, and the other need not wait out the
			// rest of them.
			if r.primary && fallback.Stop() {
				fallback.Reset(0)
			}
		}
	}
}

// inTurn tries port at each of ips in order until one accepts, each given its
// share of what is left until deadline.
func (d portDialer) inTurn(ctx context.Context, deadline time.Time, ips []stdnet.IPAddr, port int) bool {
	for i, ip := range ips {
		if ctx.Err() != nil {
			return false
		}
		until, ok := addressDeadline(d.now(), deadline, len(ips)-i)
		if !ok {
			return false
		}
		actx, cancel := context.WithDeadline(ctx, until)
		conn, err := d.dial(actx, "tcp", stdnet.JoinHostPort(ip.String(), strconv.Itoa(port)))
		cancel()
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

// addressDeadline is when the next of remaining addresses must have answered
// by, at now with deadline for them all: an even share of what is left, raised
// to minAddressShare or to all of it, as net.Dialer's partialDeadline gives
// it. ok is false when nothing is left.
func addressDeadline(now, deadline time.Time, remaining int) (until time.Time, ok bool) {
	left := deadline.Sub(now)
	if left <= 0 {
		return time.Time{}, false
	}
	share := left / time.Duration(remaining)
	if share < minAddressShare {
		share = min(left, minAddressShare)
	}
	return now.Add(share), true
}

// runOverview assembles the local network overview. Everything is read from the
// host itself; nothing leaves the machine (this backs an auto-refreshing
// dashboard tile, which must never phone home).
func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	if req.Bool("detail") {
		return detailedOverview(ctx, req)
	}
	return readNetFacts(ctx, req).page(), nil
}

// ifacePairs lists active non-loopback interfaces with their addresses,
// IPv4 first, capped so VPN/tunnel clutter cannot flood the view.
func ifacePairs() []view.Pair {
	ifs, err := stdnet.Interfaces()
	if err != nil {
		return []view.Pair{{Key: "interfaces", Value: "unreadable: " + err.Error()}}
	}
	var pairs []view.Pair
	for _, in := range ifs {
		if in.Flags&stdnet.FlagUp == 0 || in.Flags&stdnet.FlagLoopback != 0 {
			continue
		}
		addrs, _ := in.Addrs()
		var v4, v6 []string
		for _, a := range addrs {
			ipn, ok := a.(*stdnet.IPNet)
			if !ok || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			if ip4 := ipn.IP.To4(); ip4 != nil {
				v4 = append(v4, ip4.String())
			} else {
				v6 = append(v6, ipn.IP.String())
			}
		}
		vals := append(v4, v6...)
		if len(vals) == 0 {
			continue
		}
		if len(vals) > 2 {
			vals = vals[:2]
		}
		pairs = append(pairs, view.Pair{Key: in.Name, Value: strings.Join(vals, " · ")})
		if len(pairs) == 4 {
			break
		}
	}
	if len(pairs) == 0 {
		return []view.Pair{{Key: "interfaces", Value: "none active"}}
	}
	return pairs
}

// dnsServers is the configured resolvers as the detailed page lists them. On
// macOS resolv.conf mirrors the primary resolver, which is enough for an
// overview.
//
// Read as a file nobody named (pathin.ReadFile) and to net.resolver.list's
// cap: it was the one read of the file left whole and blocking, on the
// overview every surface opens, so a pipe in its place held the call for
// good and a file of any size was read to its end.
func dnsServers() string { return readDNS().text() }

// proxyEnvVars in the order net/http consults them; both cases are honored.
var proxyEnvVars = []string{"https_proxy", "http_proxy", "all_proxy", "no_proxy"}

func proxySummary() string {
	var parts []string
	for _, name := range proxyEnvVars {
		v := os.Getenv(name)
		if v == "" {
			v = os.Getenv(strings.ToUpper(name))
		}
		if v == "" {
			continue
		}
		if name != "no_proxy" {
			v = maskProxy(v)
		}
		parts = append(parts, name+"="+v)
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " · ")
}

// maskProxy hides credentials embedded in a proxy URL. The host stays
// visible wherever the value lets it — it is the useful part; the secret is
// the userinfo.
//
// **Parsed by hand rather than by url.Parse, because the schemeless form is
// a working configuration and url.Parse does not read it as one.** Given
// `bob:s3cret@proxy.corp:3128` — with no scheme, which is what a great many
// shells actually export — url.Parse takes `bob` for the scheme and the rest
// for an opaque body, so u.User is nil, the guard above returned raw, and
// this function printed the password in the clear directly underneath its
// own published promise that proxy credentials are masked.
//
// It is not a form to dismiss as malformed: golang.org/x/net/http/httpproxy
// re-parses a schemeless value with `http://` prepended, so the credential
// works, and rta's own http plugin reaches through it via DefaultTransport.
// A value that authenticates is a value that has to be masked.
//
// Scanning for the userinfo covers both spellings with one rule and no
// dependency on a parser's opinion of what a scheme is.
func maskProxy(raw string) string {
	// A `://` is the end of a scheme only when what stands before it is one
	// (hasScheme). The first one anywhere in the value was taken for it, so
	// `bob:s3cret@proxy.corp:3128/x://y` — schemeless, and read by net/http
	// as bob with s3cret, since the path is not its business — was split
	// after the credential, and printed whole; and `bob:x://s3cret@…`, whose
	// password holds the `://`, printed the username and the password's
	// first letter as though they were a scheme.
	prefix, rest := "", raw
	if i := strings.Index(raw, "://"); i >= 0 && hasScheme(raw[:i]) {
		prefix, rest = raw[:i+3], raw[i+3:]
	}
	// **The last `@`, not the first**, because that is where url.Parse splits
	// — and url.Parse is what net/http reads a proxy variable with. A password
	// holding a raw `@`, `bob:P@ssw0rd@proxy.corp:3128`, is a working
	// configuration that authenticates as bob with P@ssw0rd; cut at the first
	// `@`, this printed `***@ssw0rd@proxy.corp:3128`, most of the password,
	// underneath net.overview's promise that proxy credentials are masked.
	//
	// **And the last in the whole value, not in its authority.** The scan
	// stopped at the authority's end, the first `/`, `?` or `#`, so that an
	// `@` in a path would not mask the host — and a raw `/`, `?` or `#` in a
	// password ends the authority just as early. `bob:2024/Secret@proxy.corp`
	// is, to url.Parse, a proxy at bob:2024 with a path and no userinfo, and
	// the scan, finding no `@` in that authority, printed the password whole.
	// `bob:s3/cr3t@…`, which url.Parse refuses for its port, was printed the
	// same way. Nothing in such a value tells the password from a path, and a
	// proxy's path, query and fragment are never read — by net/http or by the
	// SOCKS dialer — so the rare value whose path holds an `@` loses its host
	// to the mask, and a password never reaches the screen.
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return raw
	}
	// The whole userinfo, username included: this function's stated rule is
	// that the host is the useful part and the userinfo is the secret.
	return prefix + "***@" + rest[at+1:]
}

// hasScheme reports whether s is a URL scheme: a letter, then letters,
// digits, `+`, `-` or `.` (RFC 3986). No colon, and nothing that could be a
// userinfo's or a path's.
func hasScheme(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return s != ""
}

// throughputKey names the row for what it counts, which is not every interface
// (see countedInterfaces).
const throughputKey = "throughput (excluding loopback)"

// ratesOf is the per-second growth of the counters of the interfaces keep
// accepts, summed. An interface that is not in the first reading has nothing to
// be compared with, and a counter that went backwards was reset and reads as
// nothing.
func ratesOf(before, after []gnet.IOCountersStat, window time.Duration, keep func(string) bool) (rx, tx uint64) {
	then := make(map[string]gnet.IOCountersStat, len(before))
	for _, s := range before {
		then[s.Name] = s
	}
	grown := func(now, was uint64) uint64 {
		if now < was {
			return 0
		}
		return now - was
	}
	var recv, sent uint64
	for _, s := range after {
		was, ok := then[s.Name]
		if !ok || !keep(s.Name) {
			continue
		}
		recv += grown(s.BytesRecv, was.BytesRecv)
		sent += grown(s.BytesSent, was.BytesSent)
	}
	return uint64(float64(recv) / window.Seconds()), uint64(float64(sent) / window.Seconds())
}

// countedIn is which interfaces' counters are traffic on the network. The
// loopback is not: a copy to 127.0.0.1 is the machine talking to itself, and
// summed with the rest it read as 20 MiB/s of network. Nor, on Linux, is the
// plumbing of a container host (containerPlumbing).
func countedIn(ifs []stdnet.Interface) func(name string) bool {
	left := map[string]bool{}
	for _, in := range ifs {
		if in.Flags&stdnet.FlagLoopback != 0 || containerPlumbing(in.Name) {
			left[in.Name] = true
		}
	}
	return func(name string) bool { return !left[name] }
}

// containerPlumbing reports whether an interface is a bridge or one end of a
// veth pair, which is what Linux says in /sys/class/net/<name>: a directory
// named bridge for the first, DEVTYPE=veth in the uevent file for the second.
// A container host is full of them, and what crosses them is counted again on
// the interface that leaves the machine. Where there is no such directory, as
// on macOS, nothing is.
func containerPlumbing(name string) bool {
	if strings.ContainsRune(name, '/') {
		return false
	}
	dir := filepath.Join(sysClassNet, name)
	if info, err := os.Stat(filepath.Join(dir, "bridge")); err == nil && info.IsDir() {
		return true
	}
	uevent, err := os.ReadFile(filepath.Join(dir, "uevent"))
	return err == nil && strings.Contains("\n"+string(uevent), "\nDEVTYPE=veth\n")
}

// detailedOverview is the full-page network report, composed from parts: the
// same summary the tile shows, the interface detail tree, and the hosts-file
// table that net.hosts.list already produces. Reusing capabilities keeps one
// implementation per fact (pkg/view Sections).
func detailedOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	tree, err := interfaceTree(ctx)
	if err != nil {
		return nil, err
	}
	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", readNetFacts(ctx, req).summary())
	p.PutAs("interfaces", "interfaces", tree)
	// The hosts file is local network truth: worth a section when non-empty.
	// Page.Run rather than AddAs precisely so a failure can be decided on
	// instead of dropping the section — and the failure was dropped anyway.
	// A hosts file that could not be read left the page one heading shorter,
	// which reads exactly like a machine whose hosts file has nothing in it,
	// on the page somebody opens to find out why a name resolves oddly.
	if v, err := p.Run(Plugin().Capability("net.hosts.list"), nil); err != nil {
		p.Warn(view.AsError(err, "net.hosts.unreadable"))
	} else if t, ok := v.(view.Table); ok && len(t.Rows) > 0 {
		p.PutAs("hosts", "hosts file", t)
	}
	return p.View(), nil
}

// interfaceTree details every interface: hardware, flags, addresses, counters.
func interfaceTree(ctx context.Context) (view.View, error) {
	var roots []view.Node

	// Per-interface traffic counters, keyed by name.
	counters := map[string]gnet.IOCountersStat{}
	if stats, err := gnet.IOCountersWithContext(ctx, true); err == nil {
		for _, s := range stats {
			counters[s.Name] = s
		}
	}

	ifs, err := stdnet.Interfaces()
	if err != nil {
		return nil, view.Errorf("net.overview.interfaces", "listing interfaces: %v", err)
	}
	var up, down []view.Node
	addressed := map[string]bool{}
	for _, in := range ifs {
		node := view.Node{Label: in.Name}
		var kids []view.Node
		if in.HardwareAddr != nil {
			kids = append(kids, view.Node{Label: "mac", Detail: in.HardwareAddr.String()})
		}
		kids = append(kids, view.Node{Label: "mtu", Detail: strconv.Itoa(in.MTU)})
		kids = append(kids, view.Node{Label: "flags", Detail: in.Flags.String()})
		if addrs, err := in.Addrs(); err == nil {
			for _, a := range addrs {
				kids = append(kids, view.Node{Label: "addr", Detail: a.String()})
				addressed[in.Name] = true
			}
		}
		if c, ok := counters[in.Name]; ok && (c.BytesRecv > 0 || c.BytesSent > 0) {
			kids = append(kids, view.Node{Label: "traffic", Detail: fmt.Sprintf(
				"rx %s (%d pkts) · tx %s (%d pkts)",
				format.Bytes(c.BytesRecv), c.PacketsRecv, format.Bytes(c.BytesSent), c.PacketsSent)})
			if c.Errin+c.Errout+c.Dropin+c.Dropout > 0 {
				kids = append(kids, view.Node{Label: "errors", Detail: fmt.Sprintf(
					"in %d · out %d · dropped %d", c.Errin, c.Errout, c.Dropin+c.Dropout)})
			}
		}
		node.Children = kids
		if in.Flags&stdnet.FlagUp != 0 {
			node.Detail = "up"
			up = append(up, node)
		} else {
			node.Detail = "down"
			down = append(down, node)
		}
	}
	if len(up) > 0 {
		// Interfaces that actually carry an address first — the rest are
		// hardware that happens to be up.
		sort.SliceStable(up, func(i, j int) bool {
			return addressed[up[i].Label] && !addressed[up[j].Label]
		})
		roots = append(roots, view.Node{Label: "interfaces", Detail: fmt.Sprintf("%d up", len(up)), Children: up})
	}
	if len(down) > 0 {
		roots = append(roots, view.Node{Label: "inactive", Detail: fmt.Sprintf("%d down", len(down)), Children: down})
	}

	return view.Tree{Roots: roots}, nil
}
