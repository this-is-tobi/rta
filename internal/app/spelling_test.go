package app

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/pflag"

	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/spelling"
)

// newSpeller is the SDK's speller over rta's own registry: every capability
// and every namespace the host has, where a plugin's own tests know only
// theirs (spelling.ForPlugin). With the registry behind it, it tells `rta
// doctor`, a command of rta's own, from `rta net dns`, a capability's.
func newSpeller(t *testing.T) (spelling.Speller, *registry.Registry) {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	var namespaces []string
	for _, p := range reg.Plugins() {
		namespaces = append(namespaces, p.Name)
	}
	return spelling.New(reg.Capability, namespaces...), reg
}

// The speller itself, held to the spellings it exists to catch and the ones
// it must let through: a test that passes because its scanner sees nothing
// guards nothing. Against rta's own catalogue, which only the host has;
// pkg/sdk/spelling holds the same rules against a plugin of its own.
func TestTheSpellerTellsATerminalsSpellingFromEveryoneElses(t *testing.T) {
	sp, _ := newSpeller(t)
	for text, want := range map[string]bool{
		"run `rta note list --all` to see every note":                     true,
		"use --timeout to extend the deadline":                            true,
		"add one with: rta note add \"...\"":                              true,
		"`rta doctor` reports it":                                         true,
		"`kv list --match aws` finds it":                                  true,
		"With `--all`, the remote branches follow":                        true,
		"ask the operator to run `rta grant allow kv.get --ttl 15m`":      false,
		"the structured equivalent of `git status --porcelain`":           false,
		"the `note_list` tool with the \"all\" argument lists every note": false,
		"the environment rta mcp serve runs in":                           false,
		"`key` takes a private key file":                                  false,
		"-----BEGIN PUBLIC KEY-----":                                      false,
		"use fs hash to inspect one file":                                 true,
		"compare it with fs hash.":                                        true,
		"net hosts add writes the line":                                   true,
		"there is no \"fs hash\" to run":                                  true,
		"the fs_hash tool checks one file":                                false,
		"fs.hash checks one file":                                         false,
		"the structured equivalent of `git log`":                          false,
		"a key set one with a keys list":                                  false,
		"Path from the operator's git config":                             false,
		"the files under fs/hash and reads fs hash.go":                    false,
		// An error opening on a capability's words reads as that command;
		// after "the", the same words are the thing they name.
		"agent log is busy: timed out":                  true,
		"agent log key /data/agent-log.key: too short":  true,
		"the agent log is busy: timed out":              false,
		"the agent log's key /data/agent-log.key: gone": false,
		// A command word holding a digit, codec.b64's.
		"decode it with rta codec b64":           true,
		"`codec b64 --decode` reads it back":     true,
		"a b64 value is not a codec b64 command": false,
		// A switch the host adds, which kv.list does not declare, is the
		// CLI's all the same.
		"`kv list --dry-run` previews it": true,
		// A namespace and a word after it are a command line the reader
		// would go looking for, whether or not a capability is behind them.
		"see rta kv bogus for it": true,
	} {
		if got := len(sp.Find(text, false)) > 0; got != want {
			t.Errorf("Find(%q) found a terminal's spelling: %v, want %v", text, got, want)
		}
	}
	// A command line is one finding, not its own and its words' without "rta".
	if hits := sp.Find("run rta fs hash ./x", false); len(hits) != 1 {
		t.Errorf("a command line was found %d times: %q", len(hits), hits)
	}
	// Text only a person at a terminal reads may name a command with no
	// capability behind it, and still not one with.
	if hits := sp.Find("the name `rta mcp serve --as` uses", true); len(hits) > 0 {
		t.Errorf("a command with no capability behind it was held against HumanOnly text: %v", hits)
	}
	if hits := sp.Find("Allow one with `rta grant allow <capability>`", true); len(hits) == 0 {
		t.Error("a capability's command line passed in HumanOnly text")
	}
	if hits := sp.Find("pipe it through `rta codec b64`", true); len(hits) == 0 {
		t.Error("a capability whose command words hold a digit passed in HumanOnly text")
	}
}

// spelling.HostSwitches is what the speller counts as a flag every
// capability's command takes, and the command tree is what adds them: every
// flag a capability command is given that the capability did not declare is
// one of them, and each of them is given to some capability command. Read
// off the tree rather than the list the SDK derives it from, so a flag the
// host starts adding, or stops, fails here before a span naming it is read
// wrongly — held against a plugin that never said it, or let through.
func TestTheHostSwitchesAreTheFlagsTheHostGivesACapability(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	switches := spelling.HostSwitches()
	given := map[string]bool{}
	for _, c := range reg.Capabilities() {
		cmd, _, err := root.Find(c.Words())
		if err != nil || cmd == root {
			t.Fatalf("cannot reach the command for %s: %v", c.ID, err)
		}
		cmd.InitDefaultHelpFlag()
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if slices.ContainsFunc(c.Inputs, func(in plugin.Field) bool { return in.Name == f.Name }) {
				return
			}
			given[f.Name] = true
			if !slices.Contains(switches, f.Name) {
				t.Errorf("%s is given --%s, which %s does not declare and spelling.HostSwitches "+
					"does not name: a span holding it after the capability's words is not read as "+
					"the CLI's", cmd.CommandPath(), f.Name, c.ID)
			}
		})
	}
	for _, name := range switches {
		if !given[name] {
			t.Errorf("spelling.HostSwitches names --%s, and no capability command is given it", name)
		}
	}
}

// Everything a client is told about a tool — its description, rta's frame
// around it, every string in its input schema — is read by an agent, which
// has arguments and no flags, and tools and no terminal. Both transports'
// tool lists, since the remote one is a different set.
func TestAToolListSpellsNothingForATerminal(t *testing.T) {
	sp, _ := newSpeller(t)
	for _, opts := range []mcp.Options{{}, {Remote: true}} {
		for _, tl := range surface(t, opts) {
			texts := []string{tl.Description}
			collectStrings(tl.Schema, &texts)
			for _, text := range texts {
				for _, hit := range sp.Find(text, false) {
					t.Errorf("tool %s (remote %v) spells a terminal's: …%s…", tl.Name, opts.Remote, hit)
				}
			}
		}
	}
}

func collectStrings(v any, out *[]string) {
	switch v := v.(type) {
	case string:
		*out = append(*out, v)
	case []any:
		for _, e := range v {
			collectStrings(e, out)
		}
	case map[string]any:
		for _, e := range v {
			collectStrings(e, out)
		}
	}
}

// The refusals an agent is actually handed, from a representative set of
// tools called over MCP the way a client calls them: the host's own — a
// missing argument, one outside its options or range, a path outside the
// roots, a grant nobody issued — and the ones handlers word. Every one names
// a tool and an argument, never a flag or an `rta` command line, except in
// the one form that hands a command on to the operator.
func TestARefusalOverMCPSpellsNothingForATerminal(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	root := t.TempDir()
	guard, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	sp, reg := newSpeller(t)
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	if _, err := mcp.NewServer(reg, "spelling", mcp.Options{Paths: guard}).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "spelling", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	const pemKey = "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAGb9ECWmEzf6FQbrBZ9w7lshQhqowtrbLDFw4rXAxZuE=\n-----END PUBLIC KEY-----\n"
	hs256 := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhIn0.c2lnbmF0dXJl"
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"codec_jwt", map[string]any{}}, // a required argument left out
		{"codec_jwt", map[string]any{"token": hs256, "key": `{"kty":"oct","k":"c2VjcmV0"}`}}, // a shared secret as a key
		{"codec_jwk", map[string]any{"key": pemKey}},                                         // PEM where a JWK goes
		{"debug_ansi", map[string]any{"input": ""}},                                          // a piped input, empty
		{"gen_token", map[string]any{"encoding": "b64"}},                                     // outside its options
		{"gen_password", map[string]any{"length": 0}},                                        // outside its range
		{"sys_ps", map[string]any{"sort": "name"}},
		{"note_show", map[string]any{"id": 999}},
		{"http_status", map[string]any{"code": "499"}},
		{"eol_check", map[string]any{"product": "demo@2", "cycle": "3"}},
		{"fs_tree", map[string]any{"path": "/"}},                         // outside the roots
		{"audit_deps", map[string]any{"path": root}},                     // nothing declared
		{"audit_why", map[string]any{"package": "lodash", "path": root}}, // nothing declared
		{"kv_get", map[string]any{"key": "db-password"}},                 // a grant nobody issued
		{"net_port", map[string]any{"host": "localhost", "ports": "22"}}, // a grant nobody issued
		{"time_at", map[string]any{"when": "not a time"}},
	}
	for _, c := range calls {
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: c.tool, Arguments: c.args})
		if err != nil {
			t.Fatalf("%s %v: %v", c.tool, c.args, err)
		}
		if !res.IsError {
			t.Errorf("%s %v was not refused, so it tests nothing", c.tool, c.args)
			continue
		}
		for _, content := range res.Content {
			text, ok := content.(*sdk.TextContent)
			if !ok {
				continue
			}
			for _, hit := range sp.Find(text.Text, false) {
				t.Errorf("%s %v refuses in a terminal's words: …%s…", c.tool, c.args, hit)
			}
		}
	}
}

// What a capability declares about itself is shown on every surface at once
// — `rta explain` and --help, the TUI's form, an agent's tool list — and has
// no surface to ask which one is reading, so it names an input as `key` and a
// capability by its ID, and never as one surface spells it.
//
// sdktest.Check holds each built-in to the same rule on its own; this reads
// the text against the whole registry, which knows every plugin's words,
// where one plugin's speller knows its own: "use fs hash" in kv's text, or
// "run rta net dns" in prose, is a command line to this one alone.
func TestDeclaredTextSpellsNothingForOneSurface(t *testing.T) {
	sp, reg := newSpeller(t)
	for _, p := range reg.Plugins() {
		for _, d := range spelling.Declared(p) {
			for _, hit := range sp.Find(d.Text, d.TerminalOnly) {
				t.Errorf("%s %s spells a terminal's: …%s…", d.ID, d.Where, hit)
			}
		}
	}
}
