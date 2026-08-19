package net

import (
	"context"
	"fmt"
	stdnet "net"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Reading and setting the system resolver.
//
// resolv.conf is the file people most often edit to no effect. On most
// modern systems it is generated — a symlink into /run on Linux with
// systemd-resolved, rewritten by configd on macOS, replaced on every DHCP
// lease elsewhere — so an edit appears to work and is gone by morning, with
// nothing to indicate why. `net resolver list` therefore leads with who owns
// the file, and `net resolver set` refuses to write one that is owned,
// unless told twice.

// resolverPath resolves which resolv.conf to work on; see hostsPath.
func resolverPath(req plugin.Request) string {
	if p := strings.TrimSpace(req.String("file")); p != "" {
		return p
	}
	return resolvConf
}

// resolverConfig is what a resolv.conf actually says.
type resolverConfig struct {
	nameservers []string
	search      []string
	options     []string
	managedBy   string
}

func parseResolv(lines []string) resolverConfig {
	var cfg resolverConfig
	for _, line := range lines {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			cfg.nameservers = append(cfg.nameservers, fields[1])
		case "search", "domain":
			cfg.search = append(cfg.search, fields[1:]...)
		case "options":
			cfg.options = append(cfg.options, fields[1:]...)
		}
	}
	return cfg
}

func runResolverList(_ context.Context, req plugin.Request) (view.View, error) {
	path := resolverPath(req)
	lines, verr := readLines(path)
	if verr != nil {
		return nil, verr
	}
	cfg := parseResolv(lines)

	pairs := []view.Pair{{Key: "file", Value: path}}
	// Who owns the file comes first: it decides whether anything else here
	// is worth changing.
	if what, _ := managedBy(path); what != "" {
		pairs = append(pairs, view.Pair{Key: "managed by", Value: what + " — edits here get overwritten"})
	} else {
		pairs = append(pairs, view.Pair{Key: "managed by", Value: "nothing — safe to edit"})
	}
	if len(cfg.nameservers) == 0 {
		pairs = append(pairs, view.Pair{Key: "nameservers", Value: "none configured"})
	}
	for i, ns := range cfg.nameservers {
		key := "nameserver"
		if i > 0 {
			// The order is the failover order, and worth showing as such.
			key = fmt.Sprintf("fallback %d", i)
		}
		pairs = append(pairs, view.Pair{Key: key, Value: ns})
	}
	if len(cfg.search) > 0 {
		pairs = append(pairs, view.Pair{Key: "search", Value: strings.Join(cfg.search, " ")})
	}
	if len(cfg.options) > 0 {
		pairs = append(pairs, view.Pair{Key: "options", Value: strings.Join(cfg.options, " ")})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

func runResolverSet(_ context.Context, req plugin.Request) (view.View, error) {
	servers := req.StringSlice("server")
	if len(servers) == 0 {
		return nil, view.Errorf("net.resolver.noserver", "no nameserver given").
			WithHint("rta net resolver set 1.1.1.1 9.9.9.9")
	}
	for _, s := range servers {
		if stdnet.ParseIP(s) == nil {
			return nil, view.Errorf("net.resolver.badserver", "%q is not an IP address", s).
				WithHint("resolv.conf takes addresses, not names — a name would need a resolver to look up")
		}
	}
	path := resolverPath(req)
	if verr := guardManaged(path, req.Bool("force")); verr != nil {
		return nil, verr
	}
	lines, verr := readLines(path)
	if verr != nil {
		return nil, verr
	}

	// Replace the nameserver lines in place, leaving search domains,
	// options and comments exactly where they were.
	out := make([]string, 0, len(lines)+len(servers))
	placed := false
	for _, line := range lines {
		if fields := strings.Fields(line); len(fields) >= 1 && fields[0] == "nameserver" {
			if !placed {
				for _, s := range servers {
					out = append(out, "nameserver "+s)
				}
				placed = true
			}
			continue
		}
		out = append(out, line)
	}
	if !placed {
		for _, s := range servers {
			out = append(out, "nameserver "+s)
		}
	}

	summary := "set the resolver to " + strings.Join(servers, ", ")
	if req.DryRun {
		return view.Text{Body: "would " + summary + " in " + path}, nil
	}
	// "set" is its own past tense, so one string serves both here.
	saved, verr := backup(path)
	if verr != nil {
		return nil, verr
	}
	if verr := writeLines(path, out); verr != nil {
		return nil, verr
	}
	body := fmt.Sprintf("%s in %s\nprevious version saved to %s", summary, path, saved)
	if what, _ := managedBy(path); what != "" {
		body += fmt.Sprintf("\n\nwarning: %s is %s — this change will be overwritten", path, what)
	}
	return view.Text{Body: body}, nil
}

// suggestResolvers offers the resolvers already configured, then the public
// ones worth comparing against. Answering "is my resolver stale?" means
// querying somebody else's, so the alternatives belong in the list.
func suggestResolvers(_ context.Context, req plugin.Request) []string {
	var out []string
	seen := map[string]bool{}
	add := func(addr, note string) {
		if addr == "" || seen[addr] {
			return
		}
		seen[addr] = true
		out = append(out, addr+"\t"+note)
	}
	if lines, verr := readLines(resolverPath(req)); verr == nil {
		for _, ns := range parseResolv(lines).nameservers {
			add(ns, "configured here")
		}
	}
	add("1.1.1.1", "Cloudflare")
	add("8.8.8.8", "Google")
	add("9.9.9.9", "Quad9")
	return out
}
