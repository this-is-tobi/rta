package net

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/mcp"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// hostsFixture points the plugin at a scratch hosts file and returns its
// path. Backups land in a scratch data dir too, so nothing touches the real
// machine.
func hostsFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	orig := hostsFile
	hostsFile = path
	t.Cleanup(func() { hostsFile = orig })
	return path
}

func hostsContent(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func run(t *testing.T, h plugin.Handler, values map[string]any) view.View {
	t.Helper()
	v, err := h(context.Background(), plugin.NewRequest(values, false, true))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

const sampleHosts = `##
# Host Database
##
127.0.0.1	localhost local.dev
255.255.255.255	broadcasthost
::1             localhost

# 10.0.0.5 parked.example.com
10.0.0.9 api.example.com  # while the staging box is down
`

func TestHostsListShowsOneRowPerNameIncludingDisabled(t *testing.T) {
	hostsFixture(t, sampleHosts)
	tbl := run(t, runHostsList, nil).(view.Table)

	states := map[string]string{}
	ips := map[string]string{}
	for _, row := range tbl.Rows {
		states[row[0]] = row[2]
		ips[row[0]] = row[1]
	}
	// A line with two names becomes two rows: a name is what you act on.
	if states["localhost"] != "active" || states["local.dev"] != "active" {
		t.Errorf("active entries = %v", states)
	}
	if ips["local.dev"] != "127.0.0.1" {
		t.Errorf("local.dev -> %q", ips["local.dev"])
	}
	// A commented-out entry is parked, not prose, and must be visible.
	if states["parked.example.com"] != "disabled" {
		t.Errorf("parked entry = %q, want disabled", states["parked.example.com"])
	}
	// A genuine comment is not an entry.
	if _, ok := states["Database"]; ok {
		t.Errorf("a prose comment was parsed as an entry: %v", states)
	}
}

func TestHostsListEmptyIsFriendly(t *testing.T) {
	hostsFixture(t, "# nothing but comments\n")
	v := run(t, runHostsList, nil)
	if !strings.Contains(v.(view.Text).Body, "No entries") {
		t.Errorf("empty hosts = %v", v)
	}
}

// The file belongs to whoever wrote it: an edit must change the one thing it
// was asked to and leave every comment, blank line and unrelated entry where
// it was.
func TestHostsAddPreservesEverythingElse(t *testing.T) {
	path := hostsFixture(t, sampleHosts)
	run(t, runHostsAdd, map[string]any{"ip": "192.168.1.50", "hostname": []string{"new.local"}})

	got := hostsContent(t, path)
	for _, keep := range []string{
		"##\n# Host Database\n##", "255.255.255.255\tbroadcasthost",
		"::1             localhost", "# 10.0.0.5 parked.example.com",
		"while the staging box is down",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("edit lost %q:\n%s", keep, got)
		}
	}
	if !strings.Contains(got, "192.168.1.50 new.local") {
		t.Errorf("new entry missing:\n%s", got)
	}
}

// A name resolves to one address, so pointing it somewhere new must take it
// away from where it was — two active entries for one name is a coin toss.
func TestHostsAddMovesTheNameOffItsOldAddress(t *testing.T) {
	path := hostsFixture(t, "127.0.0.1 api.example.com other.local\n")
	run(t, runHostsAdd, map[string]any{"ip": "10.0.0.1", "hostname": []string{"api.example.com"}})

	got := hostsContent(t, path)
	if strings.Contains(got, "127.0.0.1 api.example.com") {
		t.Errorf("the old mapping survived:\n%s", got)
	}
	if !strings.Contains(got, "127.0.0.1 other.local") {
		t.Errorf("an unrelated name on the same line was lost:\n%s", got)
	}
	if !strings.Contains(got, "10.0.0.1 api.example.com") {
		t.Errorf("the new mapping is missing:\n%s", got)
	}
}

// Adding a name to an address that already has a line joins that line rather
// than starting a second one for the same address.
func TestHostsAddMergesIntoAnExistingAddress(t *testing.T) {
	path := hostsFixture(t, "127.0.0.1 localhost\n")
	run(t, runHostsAdd, map[string]any{"ip": "127.0.0.1", "hostname": []string{"api.local"}})

	got := hostsContent(t, path)
	if strings.Count(got, "127.0.0.1") != 1 {
		t.Errorf("address duplicated instead of merged:\n%s", got)
	}
	if !strings.Contains(got, "localhost") || !strings.Contains(got, "api.local") {
		t.Errorf("merge lost a name:\n%s", got)
	}
}

// A line emptied of names goes away; a line with names left stays.
func TestHostsRemove(t *testing.T) {
	path := hostsFixture(t, "127.0.0.1 localhost local.dev\n10.0.0.9 api.example.com\n")
	run(t, runHostsRemove, map[string]any{"hostname": []string{"api.example.com", "local.dev"}})

	got := hostsContent(t, path)
	if strings.Contains(got, "api.example.com") || strings.Contains(got, "local.dev") {
		t.Errorf("names survived removal:\n%s", got)
	}
	if !strings.Contains(got, "127.0.0.1 localhost") {
		t.Errorf("the rest of the line was lost:\n%s", got)
	}
	if strings.Contains(got, "10.0.0.9") {
		t.Errorf("an emptied line was left behind:\n%s", got)
	}
}

func TestHostsRemoveUnknownIsCoded(t *testing.T) {
	hostsFixture(t, "127.0.0.1 localhost\n")
	_, err := runHostsRemove(context.Background(),
		plugin.NewRequest(map[string]any{"hostname": []string{"nope.local"}}, false, true))
	ve := view.AsError(err, "x")
	if ve.Code != "net.hosts.notfound" || ve.Hint == "" {
		t.Errorf("want net.hosts.notfound with hint, got %+v", ve)
	}
}

// Parking an entry is the whole point: the thing everyone forgets is taking
// it out again, so disabling has to be reversible and keep the entry.
func TestHostsToggleRoundTrips(t *testing.T) {
	path := hostsFixture(t, "10.0.0.9 api.example.com  # staging override\n")

	body := run(t, runHostsToggle, map[string]any{"hostname": "api.example.com"}).(view.Text).Body
	if !strings.Contains(body, "disabled") {
		t.Errorf("first toggle = %q", body)
	}
	got := hostsContent(t, path)
	if !strings.HasPrefix(strings.TrimSpace(got), "#") {
		t.Errorf("entry not parked:\n%s", got)
	}
	if !strings.Contains(got, "staging override") {
		t.Errorf("the note explaining the entry was lost:\n%s", got)
	}

	body = run(t, runHostsToggle, map[string]any{"hostname": "api.example.com"}).(view.Text).Body
	if !strings.Contains(body, "enabled") {
		t.Errorf("second toggle = %q", body)
	}
	if got := hostsContent(t, path); strings.HasPrefix(strings.TrimSpace(got), "#") {
		t.Errorf("entry not restored:\n%s", got)
	}
}

// A disabled entry is not competing for the name, so adding elsewhere leaves
// it parked rather than silently deleting somebody's saved override.
func TestHostsAddLeavesDisabledEntriesAlone(t *testing.T) {
	path := hostsFixture(t, "# 10.0.0.5 api.example.com\n")
	run(t, runHostsAdd, map[string]any{"ip": "127.0.0.1", "hostname": []string{"api.example.com"}})

	got := hostsContent(t, path)
	if !strings.Contains(got, "# 10.0.0.5 api.example.com") {
		t.Errorf("a parked entry was destroyed:\n%s", got)
	}
	if !strings.Contains(got, "127.0.0.1 api.example.com") {
		t.Errorf("the new entry is missing:\n%s", got)
	}
}

func TestHostsAddBadIPIsCoded(t *testing.T) {
	hostsFixture(t, "")
	_, err := runHostsAdd(context.Background(), plugin.NewRequest(
		map[string]any{"ip": "api.example.com", "hostname": []string{"127.0.0.1"}}, false, true))
	ve := view.AsError(err, "x")
	if ve.Code != "net.hosts.badip" || ve.Hint == "" {
		t.Errorf("want net.hosts.badip with hint, got %+v", ve)
	}
}

// The address half was checked from the beginning; the name half took
// anything. A hosts file is line-oriented, so a newline inside one argument
// is a second entry on disk — and "1.2.3.4 bank.com" is then what every
// process on the machine resolves bank.com to, from a call that asked for
// evil.com. Reachable over MCP by an agent holding a grant for one hostname,
// which is why the refusal has to happen before anything is written rather
// than being caught by a reader later.
func TestHostsAddRefusesAHostnameTheFileWouldReadAsStructure(t *testing.T) {
	for name, hostname := range map[string]string{
		"newline":         "evil.com\n1.2.3.4 bank.com",
		"carriage return": "evil.com\r1.2.3.4 bank.com",
		"space":           "evil.com 1.2.3.4",
		"tab":             "evil.com\t1.2.3.4",
		"comment marker":  "evil.com#1.2.3.4",
		"empty":           "",
	} {
		t.Run(name, func(t *testing.T) {
			path := hostsFixture(t, "127.0.0.1 localhost\n")
			before := hostsContent(t, path)

			_, err := runHostsAdd(context.Background(), plugin.NewRequest(
				map[string]any{"ip": "127.0.0.1", "hostname": []string{hostname}}, false, true))
			if err == nil {
				t.Fatalf("accepted %q as a hostname:\n%s", hostname, hostsContent(t, path))
			}
			ve := view.AsError(err, "x")
			if ve.Code != "net.hosts.badhostname" || ve.Hint == "" {
				t.Fatalf("want net.hosts.badhostname with hint, got %+v", ve)
			}
			// A refused call leaves no half-applied edit and no backup.
			if got := hostsContent(t, path); got != before {
				t.Errorf("the file was written anyway:\n%s", got)
			}
			if entries, err := os.ReadDir(backupDir()); err == nil && len(entries) > 0 {
				t.Errorf("a refused call still backed the file up: %v", entries)
			}
		})
	}
}

// The other half of the same finding, end to end. An operator who allowlists
// net.hosts.add and grants one hostname is consenting to a hosts-file entry;
// with the path arriving in the arguments they got an appended line in any
// file this process can write. This drives the real bridge with the real
// allowlist and a real grant, so it proves the route an agent would take is
// closed rather than that a field carries an annotation.
func TestHostsAddCannotBeAimedAtAnArbitraryFileOverMCP(t *testing.T) {
	path := hostsFixture(t, "127.0.0.1 localhost\n")
	if verr := grant.Save([]grant.Grant{{
		Target: "net.hosts.add", Scope: "api.local",
		Issued: time.Now(), Expires: time.Now().Add(15 * time.Minute),
	}}); verr != nil {
		t.Fatalf("issuing the grant: %v", verr)
	}

	reg := registry.New()
	if err := reg.Register(Plugin()); err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(reg, "test", mcp.Options{AllowDestructive: []string{"net.hosts.add"}})
	st, ct := sdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	published := map[string]map[string]any{}
	for _, tl := range tools.Tools {
		props, _ := tl.InputSchema.(map[string]any)["properties"].(map[string]any)
		published[tl.Name] = props
	}
	if _, offered := published["net_hosts_add"]["file"]; offered {
		t.Fatal("file is offered in the net_hosts_add tool schema — an agent can be told to ask for it")
	}
	// Reading one is the ordinary sysadmin job the flag exists for, and must
	// not have been closed along with the writing half.
	if _, offered := published["net_hosts_list"]["file"]; !offered {
		t.Error("net_hosts_list lost its file input — reading a container's hosts file is harmless")
	}

	victim := filepath.Join(t.TempDir(), ".zshrc")
	original := "must survive untouched"
	if err := os.WriteFile(victim, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      "net_hosts_add",
		Arguments: map[string]any{"ip": "10.0.0.1", "hostname": []string{"api.local"}, "file": victim},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the granted call was refused: %+v", res.Content)
	}

	after, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("the injected path was written to: %q", after)
	}
	if got := hostsContent(t, path); !strings.Contains(got, "10.0.0.1 api.local") {
		t.Errorf("the entry went nowhere near the hosts file:\n%s", got)
	}
}

func TestHostsDryRunChangesNothing(t *testing.T) {
	path := hostsFixture(t, "127.0.0.1 localhost\n")
	before := hostsContent(t, path)

	for name, tc := range map[string]struct {
		h      plugin.Handler
		values map[string]any
	}{
		"add":    {runHostsAdd, map[string]any{"ip": "10.0.0.1", "hostname": []string{"new.local"}}},
		"rm":     {runHostsRemove, map[string]any{"hostname": []string{"localhost"}}},
		"toggle": {runHostsToggle, map[string]any{"hostname": "localhost"}},
	} {
		v, err := tc.h(context.Background(), plugin.NewRequest(tc.values, true, true))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if body := v.(view.Text).Body; !strings.HasPrefix(body, "would ") {
			t.Errorf("%s dry-run = %q", name, body)
		}
	}
	if hostsContent(t, path) != before {
		t.Error("a dry run modified the file")
	}
}

// Nothing is overwritten without a copy first: this is a file that, wrong,
// makes a machine talk to the wrong server.
func TestHostsEditBacksUpFirst(t *testing.T) {
	path := hostsFixture(t, "127.0.0.1 localhost\n")
	before := hostsContent(t, path)

	body := run(t, runHostsAdd, map[string]any{"ip": "10.0.0.1", "hostname": []string{"new.local"}}).(view.Text).Body
	if !strings.Contains(body, "saved to") {
		t.Fatalf("no backup reported: %q", body)
	}
	entries, err := os.ReadDir(backupDir())
	if err != nil || len(entries) != 1 {
		t.Fatalf("backups = %v (%v)", entries, err)
	}
	saved, err := os.ReadFile(filepath.Join(backupDir(), entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != before {
		t.Errorf("backup does not match the original:\n%s", saved)
	}
}

// The file has to stay readable by every process on the machine: a 0600
// /etc/hosts breaks name resolution for everything that is not root.
func TestHostsEditKeepsFilePermissions(t *testing.T) {
	path := hostsFixture(t, "127.0.0.1 localhost\n")
	run(t, runHostsAdd, map[string]any{"ip": "10.0.0.1", "hostname": []string{"new.local"}})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("mode = %v, want the original 0644", perm)
	}
}

func TestParseHostLine(t *testing.T) {
	for name, tc := range map[string]struct {
		line    string
		ip      string
		names   []string
		enabled bool
		ok      bool
	}{
		"plain":          {"127.0.0.1 localhost", "127.0.0.1", []string{"localhost"}, true, true},
		"tabs and many":  {"127.0.0.1\tlocalhost local.dev", "127.0.0.1", []string{"localhost", "local.dev"}, true, true},
		"trailing note":  {"10.0.0.1 api  # why", "10.0.0.1", []string{"api"}, true, true},
		"disabled":       {"# 10.0.0.1 api", "10.0.0.1", []string{"api"}, false, true},
		"disabled tight": {"#10.0.0.1 api", "10.0.0.1", []string{"api"}, false, true},
		"ipv6":           {"::1 localhost", "::1", []string{"localhost"}, true, true},
		"prose comment":  {"# Host Database", "", nil, false, false},
		"no name":        {"127.0.0.1", "", nil, false, false},
		"not an ip":      {"nothing here", "", nil, false, false},
		"blank":          {"   ", "", nil, false, false},
	} {
		ip, names, enabled, ok := parseHostLine(tc.line)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", name, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if ip != tc.ip || enabled != tc.enabled || strings.Join(names, ",") != strings.Join(tc.names, ",") {
			t.Errorf("%s: got %s %v enabled=%v", name, ip, names, enabled)
		}
	}
}

// A dry run has to read as a dry run. One string cannot serve both tenses:
// "would pointed api at 127.0.0.1" is what that mistake looks like.
func TestDryRunReadsAsAPrediction(t *testing.T) {
	hostsFixture(t, "127.0.0.1 localhost\n")
	for name, tc := range map[string]struct {
		h      plugin.Handler
		values map[string]any
		want   string
	}{
		"add":     {runHostsAdd, map[string]any{"ip": "10.0.0.1", "hostname": []string{"new.local"}}, "would point new.local at 10.0.0.1"},
		"rm":      {runHostsRemove, map[string]any{"hostname": []string{"localhost"}}, "would remove localhost"},
		"disable": {runHostsToggle, map[string]any{"hostname": "localhost"}, "would disable localhost"},
	} {
		v, err := tc.h(context.Background(), plugin.NewRequest(tc.values, true, true))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if body := v.(view.Text).Body; !strings.HasPrefix(body, tc.want) {
			t.Errorf("%s dry-run = %q, want it to start %q", name, body, tc.want)
		}
	}
}

// Successive edits each keep their own backup. Two of them land in the same
// second easily — add then toggle takes about that long — and a timestamp
// alone would have the second overwrite the first, so "saved to X" would
// point at a copy of the state it claimed to preserve.
func TestBackupsDoNotOverwriteEachOther(t *testing.T) {
	path := hostsFixture(t, "127.0.0.1 localhost\n")
	original := hostsContent(t, path)

	run(t, runHostsAdd, map[string]any{"ip": "10.0.0.1", "hostname": []string{"one.local"}})
	afterFirst := hostsContent(t, path)
	run(t, runHostsAdd, map[string]any{"ip": "10.0.0.2", "hostname": []string{"two.local"}})

	entries, err := os.ReadDir(backupDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("backups = %d, want one per edit", len(entries))
	}
	saved := map[string]bool{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(backupDir(), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		saved[string(data)] = true
	}
	if !saved[original] {
		t.Error("the original file is no longer recoverable from any backup")
	}
	if !saved[afterFirst] {
		t.Error("the state before the second edit was not kept")
	}
}

// The index was computed against one trimming of the line and applied to
// another, which failed both ways: a parked entry indented past its "#"
// aborted the process, and a comment whose "#" sat after leading whitespace
// came back as live hostnames. Both are reachable from `net hosts toggle`,
// which is the commonest thing this capability does.
func TestFormatHostLineKeepsCommentsAsComments(t *testing.T) {
	cases := []struct {
		name     string
		entry    hostEntry
		original string
		want     string
	}{
		{
			"enabling a parked entry keeps its note a note",
			hostEntry{ip: "10.0.0.1", names: []string{"db.local"}, enabled: true},
			"# 10.0.0.1 db.local # staging only",
			"10.0.0.1 db.local  # staging only",
		},
		{
			"parking an entry keeps its note a note",
			hostEntry{ip: "10.0.0.1", names: []string{"db.local"}, enabled: false},
			"10.0.0.1 db.local # staging only",
			"# 10.0.0.1 db.local  # staging only",
		},
		{
			"indented past the hash, with a short note",
			hostEntry{ip: "10.0.0.1", names: []string{"db.local"}, enabled: true},
			"#    10.0.0.1 db.local  #",
			"10.0.0.1 db.local  #",
		},
		{
			"no comment at all",
			hostEntry{ip: "127.0.0.1", names: []string{"a", "b"}, enabled: true},
			"127.0.0.1 a b",
			"127.0.0.1 a b",
		},
		{
			"several hashes keep everything from the first",
			hostEntry{ip: "10.0.0.1", names: []string{"x"}, enabled: true},
			"## 10.0.0.1 x # note # more",
			"10.0.0.1 x  # note # more",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatHostLine(tc.entry, tc.original) // must not panic
			if got != tc.want {
				t.Errorf("formatHostLine(%q)\n got %q\nwant %q", tc.original, got, tc.want)
			}
		})
	}
}

// Whatever the input, a rendered line must never turn commentary into
// hostnames — the failure that silently resolves words to an address.
func TestFormatHostLineNeverPromotesACommentToAHostname(t *testing.T) {
	e := hostEntry{ip: "10.0.0.1", names: []string{"db.local"}, enabled: true}
	for _, original := range []string{
		"", "#", "##", "#  ", " # ", "#\t10.0.0.1 db.local\t# note",
		"# 10.0.0.1 db.local #", "#10.0.0.1 db.local#note",
		"   #   10.0.0.1   db.local   #   spaced   note   ",
		"10.0.0.1 db.local ### hashes",
	} {
		got := formatHostLine(e, original) // must not panic
		body, comment, split := strings.Cut(got, "#")
		if !split {
			continue // no comment survived, which is always safe
		}
		if strings.Contains(strings.TrimSpace(body), "#") {
			t.Errorf("%q: a hash leaked into the entry body: %q", original, got)
		}
		// Everything after the first hash is commentary; nothing from the
		// original's comment may appear before it.
		if note := strings.TrimSpace(strings.TrimLeft(original, "# \t")); note != "" {
			if i := strings.Index(note, "#"); i >= 0 {
				want := strings.TrimSpace(strings.TrimPrefix(note[i:], "#"))
				if want != "" && !strings.Contains(comment, want) {
					t.Errorf("%q: comment %q lost, got %q", original, want, got)
				}
			}
		}
	}
}

// Needing root to edit /etc/hosts is the expected case, not a mistake, so
// "permission denied" has to arrive as the sentence that says to use sudo.
//
// The hint is one errors.Is away from being lost: the write goes through
// internal/atomicfile, which wraps, and os.IsPermission — which reads as the
// obvious choice and is what this used before the write moved — only unwraps
// the error types the os package defines itself. A wrapped EACCES sails past
// it and the caller gets the generic message on the one path they always
// take.
func TestAPermissionFailureStillSaysToUseSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: nothing is permission-denied")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Readable and traversable, so the Stat succeeds, but nothing new can be
	// created in it — which is where the write actually fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	verr := writeLines(path, []string{"127.0.0.1 localhost", "10.0.0.1 example.test"})
	if verr == nil {
		t.Fatal("writing into an unwritable directory reported success")
	}
	if verr.Code != "net.sysfile.permission" {
		t.Fatalf("code = %q, want net.sysfile.permission (message: %s)", verr.Code, verr.Message)
	}
	if !strings.Contains(verr.Hint, "sudo") {
		t.Errorf("hint = %q, want it to name sudo", verr.Hint)
	}
}
