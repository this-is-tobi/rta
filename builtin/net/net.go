// Package net is the built-in network diagnostics plugin: overview, ping,
// DNS lookups, TCP port scans and hosts-file listing. Zero configuration.
package net

import (
	"context"
	"fmt"
	stdnet "net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	probing "github.com/prometheus-community/pro-bing"
	gnet "github.com/shirou/gopsutil/v4/net"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// hostsFile and resolvConf are vars so tests can point them at fixtures.
var (
	hostsFile  = "/etc/hosts"
	resolvConf = "/etc/resolv.conf"
)

// Plugin returns the net plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "net",
		Summary: "Network diagnostics: ping, DNS, ports, hosts file",
		Capabilities: []plugin.Capability{
			{
				ID:         "net.info",
				Summary:    "Local network overview: interfaces, DNS, proxy, throughput",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				Description: "Reports local facts only — it never calls external services, so no " +
					"public-IP lookup. Proxy credentials are masked. Throughput is sampled over 500ms. " +
					"With --detail: every interface with MAC, MTU, flags and addresses, per-interface " +
					"traffic counters, the full resolver list and proxy environment.",
				Run: runInfo,
			},
			{
				ID:         "net.ping",
				Summary:    "Ping a host and report latency statistics",
				Safety:     plugin.Read,
				Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "host to ping"},
					{Name: "count", Type: plugin.Int, Default: 4, Min: 1, Max: 100, Help: "number of probes"},
					{Name: "timeout", Type: plugin.Int, Default: 10, Min: 1, Max: 300, Help: "overall timeout in seconds"},
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
				Description: "--type auto (the default) asks the question you usually mean: A, AAAA and " +
					"CNAME for a name, PTR for an address. --server sends the query to a specific " +
					"resolver instead of the system one, which is how you tell \"the record is wrong\" " +
					"apart from \"my resolver is stale\". With --detail: the query, the resolver that " +
					"answered, and how long it took.",
				Inputs: []plugin.Field{
					{Name: "name", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "name or IP address to resolve"},
					{Name: "type", Type: plugin.String, Default: "auto",
						Options: append([]string{"auto"}, dnsTypes...),
						Help:    "which record type to ask for"},
					{Name: "server", Type: plugin.String, Suggest: suggestResolvers,
						Help: "resolver to query, e.g. 1.1.1.1 or 9.9.9.9:53 (default: the system resolver)"},
					{Name: "timeout", Type: plugin.Int, Default: 5, Min: 1, Max: 120, Help: "query timeout in seconds"},
				},
				Run: runDNS,
			},
			{
				ID:         "net.trace",
				Summary:    "Trace the route to a host, hop by hop",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "ICMP echo probes with a rising TTL: each hop that expires one reports " +
					"itself. Answers the question ping cannot — not just \"is it reachable\" but " +
					"\"where does it stop\". Silent hops show as *; that is a router declining to " +
					"reply, not necessarily a fault.",
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "host to trace"},
					{Name: "max-hops", Type: plugin.Int, Default: 30, Min: 1, Max: 64, Help: "stop after this many hops"},
					{Name: "probes", Type: plugin.Int, Default: 3, Min: 1, Max: 10, Help: "probes per hop"},
					{Name: "timeout", Type: plugin.Int, Default: 2, Min: 1, Max: 60, Help: "per-probe timeout in seconds"},
					{Name: "resolve", Type: plugin.Bool, Default: true, Help: "reverse-resolve hop addresses to names"},
				},
				Run: runTrace,
			},
			{
				ID:         "net.probe",
				Summary:    "Open a TCP connection and report what answers",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "What telnet <host> <port> is really used for: prove the port accepts a " +
					"connection, time the handshake, and show the banner the service volunteers. " +
					"--tls completes a TLS handshake first and reports the negotiated version and " +
					"cipher. It only ever listens; to speak first — the protocols that expect the " +
					"client to open — use `net send`, which is a write and gated as one.",
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "host to connect to"},
					{Name: "port", Type: plugin.Int, Positional: true, Required: true, Min: 1, Max: 65535, Help: "port to connect to"},
					{Name: "tls", Type: plugin.Bool, Help: "complete a TLS handshake after connecting"},
					{Name: "timeout", Type: plugin.Int, Default: 5, Min: 1, Max: 120, Help: "connect timeout in seconds"},
					{Name: "wait", Type: plugin.Int, Default: 2, Min: 1, Max: 120, Help: "seconds to wait for the service to say something"},
				},
				Run: runProbe,
			},
			{
				ID:      "net.send",
				Summary: "Send bytes to a TCP port and report what comes back",
				// Writing attacker-chosen bytes to an arbitrary port is a
				// remote write primitive, and a strictly more capable one than
				// http.post — which this same catalogue classifies Write.
				// "FLUSHALL\r\n" to 6379 empties a Redis; a hand-rolled
				// "DELETE /orders/7 HTTP/1.0" performs exactly the request
				// http.delete is gated to prevent. It shipped as Read, which
				// put it on every MCP server with no flag, no grant, and a
				// readOnlyHint the client was entitled to believe.
				Safety:     plugin.Write,
				NeedsGrant: true,
				Scope:      "host",
				Description: "Opens a TCP connection, writes what you give it, and shows the reply — " +
					"how you poke a protocol that expects the client to speak first (SMTP, Redis, " +
					"HTTP/1.0). \\r\\n and \\n in --data are interpreted. --tls speaks it over TLS. " +
					"This is a write: what arrives is executed by whatever is listening, so it needs " +
					"an explicit grant before an AI agent can use it. `net probe` is the read-only " +
					"half — connect, handshake, listen — and needs no grant.",
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "host to connect to"},
					{Name: "port", Type: plugin.Int, Positional: true, Required: true, Min: 1, Max: 65535, Help: "port to connect to"},
					{Name: "data", Type: plugin.String, Required: true, Help: `bytes to send, e.g. "GET / HTTP/1.0\r\n\r\n"`},
					{Name: "tls", Type: plugin.Bool, Help: "complete a TLS handshake before sending"},
					{Name: "timeout", Type: plugin.Int, Default: 5, Min: 1, Max: 120, Help: "connect timeout in seconds"},
					{Name: "wait", Type: plugin.Int, Default: 2, Min: 1, Max: 120, Help: "seconds to wait for a reply"},
				},
				Run: runSend,
			},
			{
				ID:         "net.port",
				Summary:    "TCP connect-scan ports on a host",
				Safety:     plugin.Read,
				Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Positional: true, Required: true,
						Suggest: suggestHostnames, Help: "host to scan"},
					{Name: "ports", Type: plugin.String, Default: "22,80,443",
						Help: "comma-separated ports and ranges, e.g. 22,80,8000-8010"},
					{Name: "timeout", Type: plugin.Int, Default: 2, Min: 1, Max: 60, Help: "per-port timeout in seconds"},
				},
				Run: runPort,
			},
			{
				ID:         "net.hosts.list",
				Summary:    "List hosts-file entries, including disabled ones",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "One row per hostname, since a hostname is what you look up, disable and " +
					"remove. Entries commented out by `net hosts toggle` are shown as disabled rather " +
					"than hidden as comments.",
				Inputs: []plugin.Field{
					{Name: "file", Type: plugin.Path, Help: "operate on this file instead of the system one (a container's, a chroot's)"},
				},
				Run: runHostsList,
			},
			{
				ID:      "net.hosts.add",
				Summary: "Point a hostname at an address in the hosts file",
				Safety:  plugin.Destructive,
				Scope:   "hostname",
				Description: "Adding \"127.0.0.1 api.example.com\" is a thirty-second job that takes two " +
					"minutes by hand. A name resolves to one address, so this takes the name away from " +
					"any other active entry rather than leaving two that race. Everything else in the " +
					"file — comments, ordering, spacing — survives untouched, and the previous version " +
					"is saved before the write.\n\n" +
					"Classified destructive rather than write: this rewrites a root-owned file that " +
					"governs name resolution for every process on the machine, and a wrong entry " +
					"silently reroutes traffic instead of failing. Over MCP that means a per-capability " +
					"allowlist, not just --allow-write.",
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
				ID:      "net.hosts.toggle",
				Summary: "Enable or disable a hosts-file entry without deleting it",
				Safety:  plugin.Destructive,
				Scope:   "hostname",
				Description: "The part everyone forgets is taking the entry out again, so entries can be " +
					"parked instead: a disabled entry stays in the file, commented, and comes back with " +
					"the same command. Same blast radius as add, same classification.",
				Inputs: []plugin.Field{
					{Name: "hostname", Type: plugin.String, Positional: true, Required: true, Help: "hostname to toggle",
						Suggest: suggestHostnames},
					{Name: "file", Type: plugin.Path, Local: true, Help: "operate on this file instead of the system one (a container's, a chroot's)"},
				},
				Run: runHostsToggle,
			},
			{
				ID:      "net.hosts.rm",
				Summary: "Remove hostnames from the hosts file",
				Safety:  plugin.Destructive,
				Scope:   "hostname",
				Description: "Removes the names, and the line only if nothing is left on it. " +
					"`net hosts toggle` parks an entry instead, which is usually what you want.",
				Inputs: []plugin.Field{
					{Name: "hostname", Type: plugin.StringSlice, Positional: true, Required: true, Help: "hostnames to remove",
						Suggest: suggestHostnames},
					{Name: "file", Type: plugin.Path, Local: true, Help: "operate on this file instead of the system one (a container's, a chroot's)"},
				},
				Run: runHostsRemove,
			},
			{
				ID:         "net.resolver.list",
				Summary:    "Show the system resolver, and who controls it",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "resolv.conf is the file people most often edit to no effect: on most modern " +
					"systems it is generated and replaced. This leads with who owns it, because that " +
					"decides whether anything else in it is worth changing.",
				Inputs: []plugin.Field{
					{Name: "file", Type: plugin.Path, Help: "operate on this file instead of the system one (a container's, a chroot's)"},
				},
				Run: runResolverList,
			},
			{
				ID:      "net.resolver.set",
				Summary: "Set the system nameservers",
				Safety:  plugin.Destructive,
				Scope:   "server",
				Description: "Replaces the nameserver lines, leaving search domains, options and comments " +
					"where they are, after saving the previous version. Refuses when the file is " +
					"generated by something else — silently editing a generated file is worse than " +
					"refusing, since the change works and then vanishes with nothing to point at. " +
					"--force overrides that, with the warning repeated in the result.",
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
	pinger, err := probing.NewPinger(host)
	if err != nil {
		return nil, view.Errorf("net.ping.resolve", "resolving %s: %v", host, err)
	}
	pinger.Count = req.Int("count")
	pinger.Timeout = time.Duration(req.Int("timeout")) * time.Second
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

func runDNS(ctx context.Context, req plugin.Request) (view.View, error) {
	name := strings.TrimSpace(req.String("name"))
	rtype := strings.ToUpper(strings.TrimSpace(req.String("type")))
	if rtype == "" {
		rtype = "AUTO"
	}
	if timeout := req.Int("timeout"); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
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
	for _, rt := range types {
		if err := lookup(ctx, resolver, rt, name, add); err != nil {
			// An unknown type is a usage error whatever else happens.
			if ve, ok := err.(*view.Error); ok {
				return nil, ve
			}
			lastErr = err
		}
	}
	elapsed := time.Since(start)

	// A single-type query that found nothing is a failure worth reporting;
	// an auto query tolerates the misses that come with asking broadly.
	if len(t.Rows) == 0 {
		if lastErr != nil && !auto {
			return nil, view.Errorf("net.dns.failed", "resolving %s %s: %v", rtype, name, lastErr)
		}
		return nil, view.Errorf("net.dns.norecords", "no %s records for %s", strings.Join(types, "/"), name).
			WithHint("try another --type (" + strings.Join(dnsTypes, ", ") + ") or another --server")
	}
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
			{Key: "records", Value: strconv.Itoa(len(t.Rows))},
		}}},
		{ID: "answers", Title: "answers", View: t},
	}}, nil
}

// parsePorts expands "22,80,8000-8010" into a sorted port list.
func parsePorts(spec string) ([]int, error) {
	seen := map[int]bool{}
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
		if start < 1 || end > 65535 || end < start {
			return nil, fmt.Errorf("port range %q out of bounds", part)
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
	ports, err := parsePorts(req.String("ports"))
	if err != nil {
		return nil, view.Errorf("net.port.badspec", "%v", err).
			WithHint("example: --ports 22,80,8000-8010")
	}
	if len(ports) == 0 {
		return nil, view.Errorf("net.port.empty", "no ports to scan")
	}
	timeout := time.Duration(req.Int("timeout")) * time.Second

	type result struct {
		port int
		open bool
	}
	results := make([]result, len(ports))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64) // bounded concurrency
	dialer := &stdnet.Dialer{Timeout: timeout}
	for i, port := range ports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			conn, err := dialer.DialContext(ctx, "tcp", stdnet.JoinHostPort(host, strconv.Itoa(port)))
			if err == nil {
				conn.Close()
			}
			results[i] = result{port: port, open: err == nil}
		}()
	}
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

// runInfo assembles the local network overview. Everything is read from the
// host itself; nothing leaves the machine (this backs an auto-refreshing
// dashboard tile, which must never phone home).
func runInfo(ctx context.Context, req plugin.Request) (view.View, error) {
	if req.Bool("detail") {
		return detailedInfo(ctx, req)
	}
	kv := view.KeyValue{Pairs: ifacePairs()}
	kv.Pairs = append(kv.Pairs,
		view.Pair{Key: "dns", Value: dnsServers()},
		view.Pair{Key: "proxy", Value: proxySummary()},
		view.Pair{Key: "throughput", Value: throughput(ctx)},
	)
	return kv, nil
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

// dnsServers reads the configured resolvers. On macOS resolv.conf mirrors
// the primary resolver, which is enough for an overview.
func dnsServers() string {
	data, err := os.ReadFile(resolvConf)
	if err != nil {
		return "unknown"
	}
	var servers []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && f[0] == "nameserver" {
			servers = append(servers, f[1])
		}
	}
	if len(servers) == 0 {
		return "none configured"
	}
	return strings.Join(servers, " · ")
}

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
// visible — it is the useful part; the secret is the userinfo.
func maskProxy(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	// Splice the mask in directly: url.User would %-encode it.
	u.User = nil
	s := u.String()
	if i := strings.Index(s, "://"); i >= 0 {
		return s[:i+3] + "***@" + s[i+3:]
	}
	return "***@" + s
}

// throughput samples total rx/tx over 500ms and reports per-second rates.
func throughput(ctx context.Context) string {
	const sample = 500 * time.Millisecond
	before, err := gnet.IOCountersWithContext(ctx, false)
	if err != nil || len(before) == 0 {
		return "unreadable"
	}
	select {
	case <-ctx.Done():
		return "unreadable"
	case <-time.After(sample):
	}
	after, err := gnet.IOCountersWithContext(ctx, false)
	if err != nil || len(after) == 0 {
		return "unreadable"
	}
	rate := func(now, then uint64) uint64 {
		if now < then { // counter reset
			return 0
		}
		return uint64(float64(now-then) / sample.Seconds())
	}
	return fmt.Sprintf("rx %s/s · tx %s/s",
		format.Bytes(rate(after[0].BytesRecv, before[0].BytesRecv)),
		format.Bytes(rate(after[0].BytesSent, before[0].BytesSent)))
}

// detailedInfo is the full-page network report, composed from parts: the
// same summary the tile shows, the interface detail tree, and the hosts-file
// table that net.hosts.list already produces. Reusing capabilities keeps one
// implementation per fact (pkg/view Sections).
func detailedInfo(ctx context.Context, req plugin.Request) (view.View, error) {
	tree, err := interfaceTree(ctx)
	if err != nil {
		return nil, err
	}
	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", view.KeyValue{Pairs: append(ifacePairs(),
		view.Pair{Key: "dns", Value: dnsServers()},
		view.Pair{Key: "proxy", Value: proxySummary()},
		view.Pair{Key: "throughput", Value: throughput(ctx)},
	)})
	p.PutAs("interfaces", "interfaces", tree)
	// The hosts file is local network truth: worth a section when non-empty.
	if v, err := p.Run(runHostsList, plugin.Read, nil); err == nil {
		if t, ok := v.(view.Table); ok && len(t.Rows) > 0 {
			p.PutAs("hosts", "hosts file", t)
		}
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
		return nil, view.Errorf("net.info.interfaces", "listing interfaces: %v", err)
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
