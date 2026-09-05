package net

import (
	"context"
	"fmt"
	stdnet "net"
	"strings"
	"unicode"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The hosts file, managed rather than merely read.
//
// Adding "127.0.0.1 api.example.com" while debugging is a thirty-second job
// that takes two minutes: find the file, sudo an editor, remember the
// syntax, and — the part everyone gets wrong — remember to take it out
// again. So entries can be disabled instead of deleted, which is the habit
// the shell scripts this replaces all grew independently, and `net hosts
// list` shows the disabled ones rather than pretending they are comments.

// hostsPath resolves which hosts file to work on. --file is not only a
// testing seam: editing a container's or a chroot's hosts file from the host
// is an ordinary sysadmin job, and the alternative is copying the command
// and hand-editing the path.
func hostsPath(req plugin.Request) string {
	if p := strings.TrimSpace(req.String("file")); p != "" {
		return p
	}
	return hostsFile
}

// hostEntry is one name/address mapping, and whether it is in force.
type hostEntry struct {
	line    int // index into the file's lines
	ip      string
	names   []string
	enabled bool
}

// parseHostLine reads one line as an entry. A commented-out line that still
// parses as an entry is a disabled entry, not prose — that convention is
// what makes disabling reversible, and it is what every hand-rolled
// hosts-file script already assumes.
func parseHostLine(line string) (ip string, names []string, enabled, ok bool) {
	trimmed := strings.TrimSpace(line)
	enabled = true
	if strings.HasPrefix(trimmed, "#") {
		enabled = false
		trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
	}
	// A trailing comment is somebody's note; it is not part of the entry.
	if i := strings.Index(trimmed, "#"); i >= 0 {
		trimmed = strings.TrimSpace(trimmed[:i])
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 2 || stdnet.ParseIP(fields[0]) == nil {
		return "", nil, false, false
	}
	return fields[0], fields[1:], enabled, true
}

// parseHosts reads every entry in a hosts file, remembering where each came
// from so a rewrite can touch only the lines it means to.
func parseHosts(lines []string) []hostEntry {
	var out []hostEntry
	for i, line := range lines {
		if ip, names, enabled, ok := parseHostLine(line); ok {
			out = append(out, hostEntry{line: i, ip: ip, names: names, enabled: enabled})
		}
	}
	return out
}

// formatHostLine renders an entry, keeping any trailing comment from the
// line it replaces so people's notes survive an edit.
//
// The index and the slice must come from the same string. They did not: the
// position of the trailing "#" was found in one trimming of the original and
// then applied to a differently-trimmed copy, which failed in both available
// directions. A parked entry indented past its "#" produced an index past the
// end of the shorter string and aborted the process; and where the second
// trim removed leading whitespace, the slice started *after* the "#", so
//
//	# 10.0.0.1 db.local # staging only
//
// came back from `net hosts toggle` as
//
//	10.0.0.1 db.local  staging only
//
// with "staging" and "only" now live hostnames resolving to 10.0.0.1 for
// every process on the machine. Re-enabling a parked entry is the single
// commonest thing this capability does.
func formatHostLine(e hostEntry, original string) string {
	body := e.ip + " " + strings.Join(e.names, " ")
	if !e.enabled {
		body = "# " + body
	}
	// One string, trimmed once: leading whitespace, then the comment markers
	// that park an entry, then whatever whitespace they were hiding.
	rest := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(original), "#"))
	if i := strings.Index(rest, "#"); i >= 0 {
		body += "  " + strings.TrimSpace(rest[i:])
	}
	return body
}

func runHostsList(_ context.Context, req plugin.Request) (view.View, error) {
	path := hostsPath(req)
	lines, verr := readLines(path)
	if verr != nil {
		return nil, verr
	}
	t := view.Table{Columns: []view.Column{
		{Name: "Host"},
		{Name: "IP"},
		{Name: "State", Kind: view.KindStatus},
	}}
	// One row per name rather than per line: a name is the thing you look
	// up, disable and remove, so it should be the thing you can select.
	for _, e := range parseHosts(lines) {
		state := "active"
		if !e.enabled {
			state = "disabled"
		}
		for _, name := range e.names {
			t.Rows = append(t.Rows, []string{name, e.ip, state})
		}
	}
	t.Total = len(t.Rows)
	if len(t.Rows) == 0 {
		return view.Text{Body: fmt.Sprintf("No entries in %s — add one with: rta net hosts add <ip> <hostname>", path)}, nil
	}
	return t, nil
}

// applyHosts writes the rebuilt lines, backing up first, and reports what
// happened in the same breath. It takes the change described two ways —
// "point x at y" and "pointed x at y" — because "would pointed x at y" is
// how a dry run reads when one string tries to serve both.
func applyHosts(req plugin.Request, lines []string, action, done string) (view.View, error) {
	path := hostsPath(req)
	if req.DryRun {
		return view.Text{Body: "would " + action + " in " + path}, nil
	}
	saved, verr := backup(path)
	if verr != nil {
		return nil, verr
	}
	if verr := writeLines(path, lines); verr != nil {
		return nil, verr
	}
	return view.Text{Body: fmt.Sprintf("%s in %s\nprevious version saved to %s", done, path, saved)}, nil
}

// dropNames removes the given names from an entry, reporting whether the
// entry still has any left.
func dropNames(e *hostEntry, remove map[string]bool) bool {
	kept := e.names[:0]
	for _, n := range e.names {
		if !remove[strings.ToLower(n)] {
			kept = append(kept, n)
		}
	}
	e.names = kept
	return len(e.names) > 0
}

// checkHostname refuses a name the file would read as structure rather than
// as a name. A hosts file is line-oriented and whitespace-separated, so every
// separator it has was a character the hostname argument accepted: one
// argument of "evil.com\n1.2.3.4 bank.com" is two entries on disk, and the
// second — which nobody asked for — is what every process on the machine then
// resolves bank.com to. A space does the same thing on a single line, and a
// "#" parks the rest of the entry as a comment. The address half has been
// checked by ParseIP since the beginning; this is the same refusal for the
// half that was taking anything.
//
// Only net.hosts.add needs it. rm and toggle match names that are already in
// the file and write back only what parseHostLine produced, so nothing a
// caller supplies ever reaches a line.
func checkHostname(name string) *view.Error {
	if name == "" {
		return view.Errorf("net.hosts.badhostname", "a hostname cannot be empty").
			WithHint("rta net hosts add 127.0.0.1 api.local")
	}
	for _, r := range name {
		// unicode.IsSpace is the predicate strings.Fields splits the file on,
		// so this rejects exactly what would come back as another field.
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '#' {
			return view.Errorf("net.hosts.badhostname", "%q is not a hostname: it contains %q", name, r).
				WithHint("one name per argument: rta net hosts add 127.0.0.1 api.local www.api.local")
		}
	}
	return nil
}

func runHostsAdd(_ context.Context, req plugin.Request) (view.View, error) {
	ip := strings.TrimSpace(req.String("ip"))
	if stdnet.ParseIP(ip) == nil {
		return nil, view.Errorf("net.hosts.badip", "%q is not an IP address", ip).
			WithHint("the address comes first: rta net hosts add 127.0.0.1 api.local")
	}
	names := req.StringSlice("hostname")
	if len(names) == 0 {
		return nil, view.Errorf("net.hosts.nohostname", "no hostname given").
			WithHint("rta net hosts add " + ip + " api.local")
	}
	// Before the file is read, let alone written: a refused call must leave
	// no backup and no half-applied edit behind.
	for _, n := range names {
		if verr := checkHostname(n); verr != nil {
			return nil, verr
		}
	}
	lines, verr := readLines(hostsPath(req))
	if verr != nil {
		return nil, verr
	}

	wanted := map[string]bool{}
	for _, n := range names {
		wanted[strings.ToLower(n)] = true
	}
	// A name resolves to one address, so adding it takes it away from
	// wherever it was: two enabled entries for one name is a coin toss
	// resolved by file order, which is not a thing to leave lying around.
	var drop []int
	merged := -1
	for _, e := range parseHosts(lines) {
		if e.ip == ip && e.enabled {
			if merged < 0 {
				merged = e.line
			}
			continue
		}
		if !e.enabled {
			continue // a disabled entry is not competing for the name
		}
		before := len(e.names)
		if !dropNames(&e, wanted) {
			drop = append(drop, e.line)
			continue
		}
		if len(e.names) != before {
			lines[e.line] = formatHostLine(e, lines[e.line])
		}
	}

	if merged >= 0 {
		ipTaken, existing, enabled, _ := parseHostLine(lines[merged])
		for _, n := range names {
			if !containsFold(existing, n) {
				existing = append(existing, n)
			}
		}
		lines[merged] = formatHostLine(hostEntry{ip: ipTaken, names: existing, enabled: enabled}, lines[merged])
	} else {
		lines = append(lines, formatHostLine(hostEntry{ip: ip, names: names, enabled: true}, ""))
	}
	lines = withoutLines(lines, drop)

	joined := strings.Join(names, ", ")
	return applyHosts(req, lines,
		fmt.Sprintf("point %s at %s", joined, ip),
		fmt.Sprintf("pointed %s at %s", joined, ip))
}

func runHostsRemove(_ context.Context, req plugin.Request) (view.View, error) {
	names := req.StringSlice("hostname")
	if len(names) == 0 {
		return nil, view.Errorf("net.hosts.nohostname", "no hostname given").
			WithHint("run `rta net hosts list` to see every entry")
	}
	lines, verr := readLines(hostsPath(req))
	if verr != nil {
		return nil, verr
	}
	remove := map[string]bool{}
	for _, n := range names {
		remove[strings.ToLower(n)] = true
	}

	var drop []int
	found := 0
	for _, e := range parseHosts(lines) {
		before := len(e.names)
		if dropNames(&e, remove) {
			if len(e.names) != before {
				found += before - len(e.names)
				lines[e.line] = formatHostLine(e, lines[e.line])
			}
			continue
		}
		found += before
		drop = append(drop, e.line)
	}
	if found == 0 {
		return nil, view.Errorf("net.hosts.notfound", "no entry for %s", strings.Join(names, ", ")).
			WithHint("run `rta net hosts list` to see every entry")
	}
	joined := strings.Join(names, ", ")
	return applyHosts(req, withoutLines(lines, drop),
		"remove "+joined, "removed "+joined)
}

func runHostsToggle(_ context.Context, req plugin.Request) (view.View, error) {
	name := strings.TrimSpace(req.String("hostname"))
	if name == "" {
		return nil, view.Errorf("net.hosts.nohostname", "no hostname given")
	}
	lines, verr := readLines(hostsPath(req))
	if verr != nil {
		return nil, verr
	}
	// **One name, whatever else shares its line.**
	//
	// A hosts line carries as many names as somebody put on it, and flipping
	// the line flips all of them: `toggle api.example.com` against
	//
	//	10.0.0.5  api.example.com  metrics.example.com
	//
	// parked a name nobody mentioned, changing what every process on this
	// machine resolves it to. That is a promise broken rather than a rough
	// edge — this capability declares Scope "hostname", so a grant naming one
	// name is consent to that name and to nothing else, and net.hosts.rm one
	// capability along already works per name.
	//
	// The enabling direction is the one to think about: a parked line is one
	// disabled row in `hosts list`, so re-enabling the granted name silently
	// brings back every other name parked beside it.
	//
	// So a line with company is split rather than flipped — the named entry
	// moves to a line of its own in its new state, the others stay exactly as
	// they were. Rebuilt through a replacement map rather than in place,
	// because inserting a line shifts every index parseHosts recorded.
	replace := map[int][]string{}
	nowEnabled := false
	for _, e := range parseHosts(lines) {
		if !containsFold(e.names, name) {
			continue
		}
		nowEnabled = !e.enabled
		named, others := splitName(e.names, name)
		if len(others) == 0 {
			e.enabled = nowEnabled
			replace[e.line] = []string{formatHostLine(e, lines[e.line])}
			continue
		}
		// The note on the line stays with the entry that keeps the line; the
		// new one carries the mapping and nothing else.
		rest := hostEntry{ip: e.ip, names: others, enabled: e.enabled}
		moved := hostEntry{ip: e.ip, names: named, enabled: nowEnabled}
		replace[e.line] = []string{
			formatHostLine(rest, lines[e.line]),
			formatHostLine(moved, ""),
		}
	}
	if len(replace) == 0 {
		return nil, view.Errorf("net.hosts.notfound", "no entry for %s", name).
			WithHint("run `rta net hosts list` to see every entry")
	}
	rebuilt := make([]string, 0, len(lines)+len(replace))
	for i, line := range lines {
		if r, ok := replace[i]; ok {
			rebuilt = append(rebuilt, r...)
			continue
		}
		rebuilt = append(rebuilt, line)
	}
	lines = rebuilt
	action, done := "disable", "disabled"
	if nowEnabled {
		action, done = "enable", "enabled"
	}
	return applyHosts(req, lines, action+" "+name, done+" "+name)
}

// splitName divides an entry's names into the ones that are `want` and the
// ones that are not, keeping each side's spelling and order.
//
// Case-insensitively, matching containsFold and the DNS the file stands in
// for. All the matches move, not the first: a line spelling one name twice
// ("api.example.com API.EXAMPLE.COM") is one name to a resolver, and leaving
// half of it behind would produce a file whose two lines disagree about
// whether that name is in force.
func splitName(names []string, want string) (named, others []string) {
	for _, n := range names {
		if strings.EqualFold(n, want) {
			named = append(named, n)
			continue
		}
		others = append(others, n)
	}
	return named, others
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// withoutLines drops the given line indices, leaving every other line
// untouched — including the comments and blank lines around them.
func withoutLines(lines []string, drop []int) []string {
	if len(drop) == 0 {
		return lines
	}
	dropped := map[int]bool{}
	for _, i := range drop {
		dropped[i] = true
	}
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if !dropped[i] {
			out = append(out, line)
		}
	}
	return out
}

// suggestHostnames completes from the hosts file itself — including the
// parked ones, since re-enabling an entry you disabled last week is the
// commonest reason to name it again and the hardest to remember.
// suggestAddresses completes the address half of a hosts-file entry: the
// addresses already mapped in this file, and the two everybody types.
//
// Reading the same file suggestHostnames does, because the useful answer is
// almost always one that is already in it — a second name pointed at the same
// local service, or at whatever address the last entry used.
func suggestAddresses(_ context.Context, req plugin.Request) []string {
	out := []string{
		"127.0.0.1\tthis machine",
		"::1\tthis machine, IPv6",
	}
	seen := map[string]bool{"127.0.0.1": true, "::1": true}
	lines, verr := readLines(hostsPath(req))
	if verr != nil {
		return out
	}
	for _, e := range parseHosts(lines) {
		if seen[e.ip] {
			continue
		}
		seen[e.ip] = true
		out = append(out, e.ip+"\t"+strings.Join(e.names, " "))
	}
	return out
}

func suggestHostnames(_ context.Context, req plugin.Request) []string {
	lines, verr := readLines(hostsPath(req))
	if verr != nil {
		return nil
	}
	var out []string
	for _, e := range parseHosts(lines) {
		state := e.ip
		if !e.enabled {
			state += " (disabled)"
		}
		for _, name := range e.names {
			out = append(out, name+"\t"+state)
		}
	}
	return out
}
