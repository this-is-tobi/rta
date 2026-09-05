package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/sdktest"
	"github.com/this-is-tobi/rta/pkg/view"
)

// testRegistry builds a registry with one read and one destructive capability.
func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name:    "demo",
		Summary: "demo plugin",
		Capabilities: []plugin.Capability{
			{
				ID:      "demo.item.list",
				Summary: "list items",
				Safety:  plugin.Read,
				Inputs: []plugin.Field{
					{Name: "limit", Type: plugin.Int, Help: "max items", Default: 10},
					{Name: "name", Type: plugin.String, Help: "filter", Positional: true},
				},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: strings.TrimSpace("items " + req.String("name"))}, nil
				},
			},
			{
				ID:      "demo.item.rm",
				Summary: "remove an item",
				Safety:  plugin.Destructive,
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					if req.DryRun {
						return view.Text{Body: "would remove item"}, nil
					}
					return view.Text{Body: "removed"}, nil
				},
			},
			{
				ID:      "demo.item.pick",
				Summary: "has a closed set and a suggested value",
				Safety:  plugin.Read,
				Inputs: []plugin.Field{
					{Name: "name", Type: plugin.String, Positional: true, Required: true,
						Suggest: func(_ context.Context, req plugin.Request) []string {
							// Depends on an earlier answer, which is the whole
							// reason completion gets the request.
							return []string{"alpha\tfirst", "beta-" + req.String("mode")}
						}},
					{Name: "mode", Type: plugin.String, Options: []string{"fast", "slow"}, Help: "how"},
					{Name: "out", Type: plugin.Path, Help: "where to write it"},
					{Name: "surface", Type: plugin.String,
						Suggest: func(_ context.Context, req plugin.Request) []string {
							return []string{string(req.Surface())}
						}},
				},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "picked"}, nil
				},
			},
			{
				ID:      "demo.item.fail",
				Summary: "always fails",
				Safety:  plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return nil, view.Errorf("demo.fail", "it broke").WithHint("try again")
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// run executes the root command with args and captures stdout/stderr.
func run(t *testing.T, reg *registry.Registry, args ...string) (string, string, error) {
	t.Helper()
	// NewRoot calls config.Load(), which reads RTA_CONFIG (or the real XDG
	// config path) unconditionally — every test in this file ran against
	// whatever config happened to be sitting on the machine running the
	// suite, config.Output included, silently changing the default --output
	// format underfoot. Isolated the same way doctor_test.go's isolate() is:
	// its own directory, no ambient key material either.
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_KV_PASSPHRASE", "")
	t.Setenv("RTA_KV_IDENTITY", "")
	root := NewRoot(reg, "test")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func TestRunCapability(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "demo", "item", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "items") {
		t.Errorf("output = %q", out)
	}
}

func TestPositionalArg(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "demo", "item", "list", "widgets")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "items widgets") {
		t.Errorf("positional not passed: %q", out)
	}
}

func TestVariadicPositional(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "multi", Summary: "multi",
		Capabilities: []plugin.Capability{{
			ID: "multi.check", Summary: "check many", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "targets", Type: plugin.StringSlice, Positional: true, Required: true, Help: "targets"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: strings.Join(req.StringSlice("targets"), "+")}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, reg, "multi", "check", "a", "b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a+b+c") {
		t.Errorf("variadic args not collected: %q", out)
	}
}

func TestTypedPositionalConversion(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "typed", Summary: "typed",
		Capabilities: []plugin.Capability{{
			ID: "typed.take", Summary: "take an int", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "id", Type: plugin.Int, Positional: true, Required: true, Help: "id"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: fmt.Sprintf("got %d", req.Int("id")+1)}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, reg, "typed", "take", "41")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "got 42") {
		t.Errorf("int positional not converted: %q", out)
	}
	// Invalid ints are usage errors.
	_, _, err = run(t, reg, "typed", "take", "abc")
	if ExitCode(err) != 2 {
		t.Errorf("bad int exit = %d, want 2", ExitCode(err))
	}
}

func TestJSONOutput(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "demo", "item", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	if m["type"] != "text" {
		t.Errorf("type = %v", m["type"])
	}
}

func TestDestructiveRequiresYes(t *testing.T) {
	_, _, err := run(t, testRegistry(t), "demo", "item", "rm")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeConfirmRequired {
		t.Fatalf("want %s, got %v", CodeConfirmRequired, err)
	}
	if ExitCode(err) != 3 {
		t.Errorf("exit code = %d, want 3", ExitCode(err))
	}
}

func TestDestructiveWithYes(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "demo", "item", "rm", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed") {
		t.Errorf("output = %q", out)
	}
}

func TestDestructiveDryRunBypassesGate(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "demo", "item", "rm", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "would remove") {
		t.Errorf("output = %q", out)
	}
}

func TestCapabilityErrorRendering(t *testing.T) {
	_, errOut, err := run(t, testRegistry(t), "demo", "item", "fail")
	if ExitCode(err) != 1 {
		t.Errorf("exit code = %d, want 1", ExitCode(err))
	}
	for _, want := range []string{"demo.fail", "it broke", "try again"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
	}
}

func TestUnknownSubcommandIsUsageError(t *testing.T) {
	_, _, err := run(t, testRegistry(t), "demo", "nope")
	if err == nil {
		t.Fatal("unknown subcommand accepted")
	}
	if ExitCode(err) != 2 {
		t.Errorf("exit code = %d, want 2", ExitCode(err))
	}
}

func TestExitCodeContract(t *testing.T) {
	if ExitCode(nil) != 0 {
		t.Error("nil should be 0")
	}
	if ExitCode(errors.New("usage")) != 2 {
		t.Error("plain errors are usage errors → 2")
	}
	if ExitCode(&view.Error{Code: "x.y", Message: "m"}) != 1 {
		t.Error("capability errors → 1")
	}
	if ExitCode(&view.Error{Code: CodeConfirmRequired, Message: "m"}) != 3 {
		t.Error("confirmation declined → 3")
	}
}

func TestBuiltinRegistryLoads(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Capability("sys.cpu"); !ok {
		t.Error("sys.cpu not registered")
	}
}

// --- Completion ---------------------------------------------------------
//
// cobra answers a shell through a hidden __complete command, so driving that
// is testing exactly what a terminal sees.

func complete(t *testing.T, reg *registry.Registry, args ...string) []string {
	t.Helper()
	out, _, err := run(t, reg, append([]string{"__complete"}, args...)...)
	if err != nil {
		t.Fatalf("__complete: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l == "" || strings.HasPrefix(l, ":") { // trailing directive line
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

// A closed set completes to exactly its members.
func TestFlagCompletionOffersOptions(t *testing.T) {
	got := complete(t, testRegistry(t), "demo", "item", "pick", "x", "--mode", "")
	if len(got) != 2 || got[0] != "fast" || got[1] != "slow" {
		t.Errorf("--mode completion = %v, want the declared options", got)
	}
}

// A positional completes from what exists, descriptions included — cobra
// shows the text after the tab, which is what makes an id worth choosing.
func TestPositionalCompletionOffersSuggestions(t *testing.T) {
	got := complete(t, testRegistry(t), "demo", "item", "pick", "")
	if len(got) == 0 || !strings.HasPrefix(got[0], "alpha\tfirst") {
		t.Fatalf("positional completion = %v", got)
	}
}

// Suggestions see what the caller has already typed, so a later field can
// depend on an earlier one.
func TestCompletionSeesEarlierAnswers(t *testing.T) {
	got := complete(t, testRegistry(t), "demo", "item", "pick", "--mode", "fast", "")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "beta-fast") {
		t.Errorf("completion ignored the flag already given: %v", got)
	}
}

// A field with nothing to offer must not fall back to completing filenames:
// a list of the current directory is worse than no answer.
func TestCompletionWithoutCandidatesOffersNothing(t *testing.T) {
	if got := complete(t, testRegistry(t), "demo", "item", "list", ""); len(got) != 0 {
		t.Errorf("completion = %v, want nothing", got)
	}
}

// A path is completable with nothing declared: the shell knows what is on the
// filesystem, and directive 0 is how cobra is told to let it answer.
func TestPathCompletionDefersToTheShell(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "__complete", "demo", "item", "pick", "x", "--out", "")
	if err != nil {
		t.Fatal(err)
	}
	directive := ""
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(l, ":") {
			directive = l
		}
	}
	if directive != ":0" {
		t.Errorf("directive = %q, want :0 (default file completion)", directive)
	}
}

// A field with no path type keeps refusing file completion, so the two cases
// stay distinguishable rather than one silently becoming the other.
func TestNonPathCompletionStillRefusesFiles(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "__complete", "demo", "item", "pick", "x", "--mode", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ":4") {
		t.Errorf("want ShellCompDirectiveNoFileComp, got %q", out)
	}
}

// Completion is a keystroke, not a caller: a Suggest that would prompt has to
// be able to tell, and the surface is how.
func TestCompletionRunsOnItsOwnSurface(t *testing.T) {
	got := complete(t, testRegistry(t), "demo", "item", "pick", "x", "--surface", "")
	if len(got) != 1 || got[0] != string(plugin.SurfaceCompletion) {
		t.Errorf("surface seen by Suggest = %v, want completion", got)
	}
}

// `rta explain` answers "what can I do"; this answers "what do I have".
func TestPluginsInventory(t *testing.T) {
	out, _, err := run(t, testRegistry(t), "plugin", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("plugin list is not a table: %v (%s)", err, out)
	}
	if len(env.Rows) != 1 || env.Rows[0][0] != "demo" {
		t.Fatalf("rows = %v", env.Rows)
	}
	// The reach column is the one worth having: "which of these can change my
	// machine?" — answered by the worst class the plugin holds.
	if env.Rows[0][2] != "destructive" {
		t.Errorf("reach = %q, want the worst class the plugin holds", env.Rows[0][2])
	}
}

// --- Catalogue conformance ----------------------------------------------
//
// pkg/sdk/sdktest is about to be published as the definition of "a correct
// plugin", and M2 freezes the contract it checks. A rule nothing has ever
// been held to is a guess, so these run the suite over the real catalogue —
// the built-ins are the first third-party plugin, and they get no
// exemption.

// conformanceInputs supplies values for the capabilities the suite cannot
// drive from their declared defaults.
//
// What is deliberately absent is as load-bearing as what is here: no target
// is given to anything that would leave the machine. cert.*, http.get/head,
// net.dns/ping/port/probe/trace, audit.mail and audit.web are left
// undrivable, and the suite reports each one by name. Network
// calls happen only on explicit user action, and a test suite is nobody
// asking.
func conformanceInputs(dir string) map[string]map[string]any {
	// A hosts file the net capabilities can edit, inside the watched
	// directory, so that a dry run that rewrites it is caught by the same
	// comparison that watches the data dir. Copying /etc/hosts would make the
	// test depend on the machine; this is the shape, not the content.
	hosts := filepath.Join(dir, "hosts")
	_ = os.WriteFile(hosts, []byte("127.0.0.1\tlocalhost\n10.0.0.1\tdb.local\n"), 0o600)
	resolv := filepath.Join(dir, "resolv.conf")
	_ = os.WriteFile(resolv, []byte("nameserver 1.1.1.1\n"), 0o600)
	payload := filepath.Join(dir, "payload.txt")
	_ = os.WriteFile(payload, []byte("hello\n"), 0o600)

	return map[string]map[string]any{
		// Pure transforms: no reason not to run them.
		"codec.b64": {"value": "aGVsbG8="},
		"codec.hex": {"value": "68656c6c6f"},
		"codec.url": {"value": "a%20b"},
		"codec.jwt": {"token": "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2ln"},
		"fs.hash":   {"path": payload},

		// Local state, against the temp data dir.
		"kv.copy":     {"key": "absent", "to": "absent-copy"},
		"lock.add":    {"kind": "agent", "name": "nobody"},
		"lock.rm":     {"kind": "agent", "name": "nobody"},
		"kv.edit":     {"key": "absent"},
		"kv.get":      {"key": "absent"},
		"kv.rename":   {"key": "absent", "new-name": "absent-renamed"},
		"kv.rm":       {"key": "absent"},
		"kv.set":      {"key": "demo", "value": "s3cret"},
		"kv.show":     {"key": "absent"},
		"kv.history":  {"key": "absent"},
		"kv.restore":  {"key": "absent"},
		"note.edit":   {"id": 1, "title": "t"},
		"note.rm":     {"id": 1},
		"note.search": {"query": "x"},
		"note.show":   {"id": 1},
		"todo.done":   {"id": 1},
		"todo.edit":   {"id": 1, "title": "t"},
		"todo.reopen": {"id": 1},
		"todo.rm":     {"id": 1},
		"todo.search": {"query": "x"},
		"todo.show":   {"id": 1},
		"note.add":    {"title": "conformance"},
		"todo.add":    {"title": "conformance"},
		"grant.allow": {"target": "sys.cpu", "ttl": "1m"},

		// Ids nothing is parked under: the dry-run rule watches the directory,
		// and a request that does not exist still has to leave it alone.
		"agent.allow": {"id": "absent"},
		"agent.deny":  {"id": "absent"},

		// keys.restore is driven with a real mnemonic on purpose. The all-zero
		// entropy BIP39 vector reaches the key derivation and the write, which
		// is the only part of this capability --dry-run has anything to say
		// about; an invalid phrase would be refused at validation and would
		// have proved nothing about the write. keys.backup gets an absent path
		// because it writes nothing at all — it reads a key and prints words.
		"keys.backup": {"key": filepath.Join(dir, "absent-key")},
		// keys.add gets a path nothing occupies, so the dry run reaches the
		// point where it would write and the watched-directory comparison has
		// something to say about it. A path that already existed would be
		// refused before that, which proves nothing about the write.
		"keys.add": {"out": filepath.Join(dir, "generated")},
		"keys.restore": {"out": filepath.Join(dir, "restored"), "words": strings.TrimSpace(
			strings.Repeat("abandon ", 23) + "art")},

		// net.send, on the same doctrine as http.post below and for a sharper
		// reason: its own declaration calls it "a remote write primitive, and a
		// strictly more capable one than http.post", and it is the capability
		// whose dry run this suite caught sending real bytes on its first run.
		// Leaving it undriven afterwards was the gap that let six external
		// plugins ship the same defect.
		"net.send": {"host": "127.0.0.1", "port": 1, "data": "conformance\n"},

		// Only present under -tags ai, and an unused key in the lean build.
		// --base-url at a dead port for the same reason http.post gets one,
		// with a sharper edge: the request this would send costs the operator
		// money and goes to a third party.
		"ai.ask": {"prompt": []string{"conformance"}, "base-url": "http://127.0.0.1:1/v1", "key": "conformance"},

		// Files the mutating net capabilities edit, redirected into the
		// watched directory. Without this they are skipped, and hosts/resolver
		// editing is exactly the shape --dry-run exists for.
		"net.hosts.add":    {"ip": "10.0.0.2", "hostname": []string{"conformance.local"}, "file": hosts},
		"net.hosts.rm":     {"hostname": []string{"db.local"}, "file": hosts},
		"net.hosts.toggle": {"hostname": "db.local", "file": hosts},
		"net.resolver.set": {"server": []string{"9.9.9.9"}, "file": resolv},

		// A URL that resolves to nothing anyone is listening on. If a dry run
		// ever stops being dry, the failure is a refused connection here and
		// not a request sent to somebody's service.
		"http.delete": {"url": "http://127.0.0.1:1/conformance"},
		"http.post":   {"url": "http://127.0.0.1:1/conformance", "data": "{}"},
		"http.put":    {"url": "http://127.0.0.1:1/conformance", "data": "{}"},
	}
}

func TestBuiltinsPassTheConformanceSuite(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range reg.Plugins() {
		t.Run(p.Name, func(t *testing.T) {
			// Same isolation run() uses: no ambient config, and no key
			// material, so kv answers as it would on a fresh machine.
			t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
			t.Setenv("RTA_KV_PASSPHRASE", "")
			t.Setenv("RTA_KV_IDENTITY", "")
			sdktest.Check(t, p, sdktest.WithInputs(conformanceInputs))
		})
	}
}

// Every format, not just the JSON one sdktest can reach. `pretty`, `csv` and
// `md` live in internal/render and a public package cannot import them, so
// the half of "degradable to every surface" that only the host can prove is
// proven here — and it is the half a plugin author is likeliest to break,
// since a view that reads fine as JSON can still be a table with more cells
// than columns or a chart the pretty renderer divides by.
func TestEveryBuiltinViewSurvivesEveryOutputFormat(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	t.Setenv("RTA_KV_PASSPHRASE", "")
	t.Setenv("RTA_KV_IDENTITY", "")
	inputs := conformanceInputs(dir)

	formats := []cli.Format{cli.Pretty, cli.JSON, cli.YAML, cli.CSV, cli.Markdown}
	for _, c := range reg.Capabilities() {
		if c.Safety != plugin.Read || c.NoPreview {
			continue
		}
		values := plugin.Resolve(c, plugin.Inputs{Caller: inputs[c.ID]})
		if !runnable(c, values) {
			continue
		}
		v, err := c.Run(context.Background(), plugin.NewRequest(values, false, false).WithSurface(plugin.SurfaceCLI))
		if err != nil || v == nil {
			continue
		}
		_, isTable := v.(view.Table)
		for _, f := range formats {
			// csv is tables-only by declaration, not by accident: a
			// "key,value" pair list is not a spreadsheet and pretending
			// otherwise is what makes csv output untrustworthy. It refuses
			// anything else, so anything else is not asked.
			if f == cli.CSV && !isTable {
				continue
			}
			var buf bytes.Buffer
			// Width is fixed rather than taken from the terminal: a
			// golden-free test that renders differently on a narrow window is
			// a test that fails for whoever has a small laptop.
			opts := cli.Options{Format: f, NoColor: true, Width: 100}
			if err := cli.Render(&buf, v, opts); err != nil {
				t.Errorf("%s as %s: %v", c.ID, f, err)
			}
			if buf.Len() == 0 {
				// A renderer that returns nil and writes nothing has produced
				// a successful empty result, which a script reads as "no
				// data" and a person reads as a broken terminal.
				t.Errorf("%s as %s rendered nothing", c.ID, f)
			}
		}
	}
}

// runnable reports whether every required input has a value, which is what
// separates "this capability is broken" from "this test cannot drive it".
func runnable(c plugin.Capability, values map[string]any) bool {
	for _, f := range c.Inputs {
		if f.Required {
			if _, ok := values[f.Name]; !ok {
				return false
			}
		}
	}
	return true
}

// knownDryRunLeaks names capabilities whose --dry-run still reaches the
// network. Each entry is a bug in builtin/, not a rule to soften: the test
// below reports them instead of failing, so the suite stays green while the
// entry keeps the debt visible rather than absent.
//
// It is empty, and the test fails the moment it stops being: sdktest found
// net.send here on its first run — runSend went straight to the dialer
// without ever reading req.DryRun — and that is now fixed and pinned by
// TestSendDryRunNeverReachesTheNetwork.
var knownDryRunLeaks = map[string]string{}

// The dry-run rule sdktest publishes watches a directory, which is the wrong
// organ for the bug that motivated it: `http post --dry-run` shipped sending
// the request, and on-box nothing had changed. This is the half a public
// package cannot express and the host can — a listener nobody may connect to.
//
// Targets are derived from the declaration rather than listed by ID, so a
// capability that grows a --url next year is covered the day it lands.
func TestNoDryRunReachesTheNetwork(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var accepted atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			conn.Close()
		}
	}()
	host, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	for _, c := range reg.Capabilities() {
		if c.Safety == plugin.Read {
			continue
		}
		values, aimed := aimAtListener(c, host, port)
		if !aimed {
			continue
		}
		before := accepted.Load()
		req := plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), true, false).WithSurface(plugin.SurfaceCLI)
		_, _ = c.Run(context.Background(), req)
		// The kernel completes a connect() from the backlog before Accept
		// runs, so the counter can trail the handler by a moment. Every leak
		// this catches also waits for a reply, which makes 100ms generous
		// rather than tight.
		time.Sleep(100 * time.Millisecond)
		if accepted.Load() == before {
			continue
		}
		if why, known := knownDryRunLeaks[c.ID]; known {
			t.Logf("TODO(%s): --dry-run opened a connection — %s", c.ID, why)
			continue
		}
		t.Errorf("%s opened a connection under --dry-run; a dry run reports what would happen", c.ID)
	}
}

// aimAtListener points a capability's declared network inputs at addr, and
// reports whether it has any. A capability with no way to name a destination
// cannot reach one.
func aimAtListener(c plugin.Capability, host string, port int) (map[string]any, bool) {
	values := map[string]any{}
	var has struct{ url, host, port bool }
	for _, f := range c.Inputs {
		switch f.Name {
		case "url":
			has.url = true
		case "host":
			has.host = true
		case "port":
			has.port = true
		case "data":
			// Enough to be a write and not enough to be a valid request:
			// what is being measured is the connection, not the reply.
			values["data"] = "PING\r\n"
		}
	}
	switch {
	case has.url:
		values["url"] = "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/dry-run"
	case has.host && has.port:
		// port as an int, not the string SplitHostPort hands back: Resolve
		// normalises the widths an integer legitimately arrives in and a
		// string is not one of them, so req.Int would read 0 and the
		// capability would fail its own range check before dialling — a test
		// that passes because nothing was attempted.
		values["host"], values["port"] = host, port
	default:
		return nil, false
	}
	// Nothing should be waiting on a reply that is not coming; a leak is
	// counted at connect time either way.
	values["timeout"], values["wait"] = 2, 1
	return values, true
}

// The verb vocabulary is published as derived from this catalogue rather than
// invented, and that claim is worth checking rather than asserting in a
// comment: a word nothing ships is a word rta is telling strangers to use and
// does not use itself.
func TestTheVerbVocabularyIsDerivedFromTheCatalogue(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]string{}
	for _, c := range reg.Capabilities() {
		words := c.Words()
		used[words[len(words)-1]] = c.ID
	}
	for _, verb := range sdktest.Vocabulary() {
		if _, ok := used[verb]; !ok {
			t.Errorf("vocabulary word %q is used by no built-in", verb)
		}
	}
}

// Every command that groups others must reject an unknown subcommand rather
// than print help and succeed.
//
// Walked over the real catalogue rather than a list of names, because the
// list of names is exactly what went wrong. Cobra's default for a parent with
// no Run is help-and-nil; the fourteen namespaces built from the registry
// overrode it and the hand-written groups did not, so `rta mcp serv` — one
// letter off `serve` — exited 0 having done nothing. A human reads that as an
// answer and a script reads it as success.
//
// Fixing the two named groups then left the nested nouns, `rta net hosts` and
// `rta net resolver`, still doing it one level down. That is the case this
// walk exists for: it covers every group there is now and every one added
// later, without anybody remembering to extend a list.
func TestEveryGroupCommandRejectsUnknownSubcommands(t *testing.T) {
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	var groups [][]string
	var walk func(cmd *cobra.Command, path []string)
	walk = func(cmd *cobra.Command, path []string) {
		if !cmd.HasSubCommands() {
			return
		}
		// The ai namespace is deliberately not a pure group: its bare form
		// takes free words as the question (`rta ai what broke`), so an
		// unrecognised first word is a prompt, never a typo to refuse. Its
		// subcommands underneath still get the rule.
		if len(path) > 0 && path[0] != "ai" {
			groups = append(groups, path)
		}
		for _, sub := range cmd.Commands() {
			if sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			walk(sub, append(append([]string{}, path...), sub.Name()))
		}
	}
	walk(NewRoot(reg, "test"), nil)
	if len(groups) < 15 {
		t.Fatalf("only found %d groups, the catalogue has more than that: %v", len(groups), groups)
	}

	for _, path := range groups {
		args := append(append([]string{}, path...), "definitely-not-a-subcommand")
		out, _, err := run(t, reg, args...)
		if err == nil {
			t.Errorf("`rta %s definitely-not-a-subcommand` succeeded, printing %d bytes",
				strings.Join(path, " "), len(out))
			continue
		}
		if !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("`rta %s ...` failed with %v, which does not say the command is unknown",
				strings.Join(path, " "), err)
		}
		if got := ExitCode(err); got != 2 {
			t.Errorf("`rta %s ...` exit code = %d, want 2 (usage)", strings.Join(path, " "), got)
		}
	}

	// And a bare group still shows help rather than erroring, which is what
	// makes the rule safe to apply to every group there is.
	for _, path := range groups {
		if _, _, err := run(t, reg, path...); err != nil {
			t.Errorf("bare `rta %s` failed: %v", strings.Join(path, " "), err)
		}
	}
}

// A command that printed its own error must say so, or it is printed twice.
//
// `rta explain <typo>` did exactly that: the command rendered the error to
// stderr and returned it bare, and main — which now prints unmarked
// view.Errors by default, so that a command returning one cannot fail
// silently — printed it again in a slightly different layout. Two renderings
// of one problem reads as two problems.
func TestACommandThatPrintsItsOwnErrorMarksIt(t *testing.T) {
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, runErr := run(t, reg, "explain", "definitely-not-a-capability")
	if runErr == nil {
		t.Fatal("explain accepted a capability that does not exist")
	}
	if _, ok := runErr.(RenderedError); !ok {
		t.Errorf("explain returned %T unmarked, so main prints it a second time", runErr)
	}
	if n := strings.Count(stderr, "core.capability.unknown"); n != 1 {
		t.Errorf("the error appears %d times in the command's own output:\n%s", n, stderr)
	}
}

// An input declared Positional is an argument, and saying so beats "unknown
// flag".
//
// The mistake is one the declaration invites: `rta explain hello.greet` lists
// `input:name`, so `--name` is the obvious thing to type, and pflag's honest
// answer — "unknown flag: --name" — leaves somebody staring at an input that
// plainly exists. The capability knows which of its inputs are positional, so
// the error names the one they meant and shows the form that works.
func TestAPositionalPassedAsAFlagSaysSo(t *testing.T) {
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}

	// todo.add declares `title` positional and `body` as a flag.
	_, _, runErr := run(t, reg, "todo", "add", "--title", "x")
	if runErr == nil {
		t.Fatal("--title was accepted for a positional input")
	}
	msg := runErr.Error()
	for _, want := range []string{"title", "argument", "rta todo add <title>"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
	// Usage mistakes exit 2. A view.Error would map to 1, which is why this
	// one stays a plain error.
	if got := ExitCode(runErr); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}

	// A flag that is not an input at all keeps pflag's own wording — there is
	// nothing better to say about a name the capability never declared.
	_, _, runErr = run(t, reg, "todo", "add", "x", "--definitely-not-declared")
	if runErr == nil {
		t.Fatal("an undeclared flag was accepted")
	}
	if strings.Contains(runErr.Error(), "argument, not a flag") {
		t.Errorf("an undeclared flag was reported as a positional: %v", runErr)
	}

	// And a real flag still works, so the hook did not swallow parsing.
	if _, _, err := run(t, reg, "todo", "add", "buy milk", "--body", "from the shop"); err != nil {
		t.Errorf("a declared flag stopped working: %v", err)
	}
}

// The message must not begin with the input name.
//
// fang applies a transform to the error text that capitalises the first
// letter it finds. Observed against the real binary: the message
// `"name" is an argument …` reached the terminal as `"Name" is an argument`,
// which names a field the capability does not have.
//
// Asserted as a property of rta's message rather than by rendering it through
// fang, and that is the second attempt. The first upper-cased byte zero, which
// for `"title" …` is the quote character, so it passed on the exact message it
// was written to reject. The second called fang.DefaultErrorHandler with a
// zero Styles — which adds the trailing full stop but not the capitalisation,
// because the transform lives on the theme's ErrorText style and fang exports
// no way to build one. Two vacuous tests in a row is the signal to stop
// modelling somebody else's renderer and state the rule instead: whatever
// capitalises the first letter, it must not be inside an identifier.
func TestThePositionalHintDoesNotStartWithTheInputName(t *testing.T) {
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, runErr := run(t, reg, "todo", "add", "--title", "x")
	if runErr == nil {
		t.Fatal("--title was accepted")
	}
	msg := runErr.Error()
	if !strings.Contains(msg, `"title"`) {
		t.Fatalf("the message no longer names the input: %s", msg)
	}
	for _, bad := range []string{`"title"`, "title"} {
		if strings.HasPrefix(msg, bad) {
			t.Errorf("the message starts with the input name, so the first letter capitalised "+
				"by the renderer is inside it: %s", msg)
		}
	}
	if r := []rune(msg)[0]; !unicode.IsLetter(r) {
		t.Errorf("the message starts with %q rather than a word, so what gets capitalised "+
			"is whatever follows it: %s", r, msg)
	}
}

// A failure must come back in the format the caller asked for.
//
// main printed an unrendered view.Error with fmt.Fprintf, ignoring --output
// entirely, so `rta plugin dev -o json` answered success with JSON and failure
// with prose. A script that parses the first has nothing to do with the
// second, and the failure is precisely when it most needs a code to branch on.
//
// cli.RenderError's own doc records this being fixed once already for -o yaml,
// inside the renderer. This was the same fault at the one call site that did
// not go through it.
func TestATopLevelErrorHonoursTheOutputFormat(t *testing.T) {
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	ve := view.Errorf("plugin.dev.dir", "/tmp/x is not a Go module").
		WithHint("run `rta plugin new <name>` to make one")

	for _, tc := range []struct {
		format string
		check  func(t *testing.T, out string)
	}{
		{"json", func(t *testing.T, out string) {
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("-o json produced something no parser accepts: %v\n%s", err, out)
			}
			if got["code"] != "plugin.dev.dir" {
				t.Errorf("code = %v", got["code"])
			}
		}},
		{"yaml", func(t *testing.T, out string) {
			if !strings.Contains(out, "code: plugin.dev.dir") {
				t.Errorf("-o yaml did not produce yaml:\n%s", out)
			}
			if strings.Contains(out, "ERROR ") {
				t.Errorf("-o yaml produced the styled block:\n%s", out)
			}
		}},
		{"pretty", func(t *testing.T, out string) {
			if !strings.Contains(out, "plugin.dev.dir") || !strings.Contains(out, "HINT") {
				t.Errorf("-o pretty lost the code or the hint:\n%s", out)
			}
		}},
	} {
		t.Run(tc.format, func(t *testing.T) {
			root := NewRoot(reg, "test")
			if err := root.PersistentFlags().Set("output", tc.format); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if !RenderTopLevelError(&buf, root, ve) {
				t.Fatal("a view.Error was not handled")
			}
			tc.check(t, buf.String())
		})
	}

	// An error a command already printed stays printed once.
	var buf bytes.Buffer
	if !RenderTopLevelError(&buf, NewRoot(reg, "test"), Rendered(ve)) {
		t.Error("a marked error was not handled")
	}
	if buf.Len() != 0 {
		t.Errorf("a marked error was printed a second time: %s", buf.String())
	}

	// And a usage error is not rta's to format — fang makes those match the
	// help, so the handler must decline them.
	buf.Reset()
	if RenderTopLevelError(&buf, NewRoot(reg, "test"), errors.New("unknown flag: --nope")) {
		t.Error("a plain error was claimed, so fang never styles a usage mistake")
	}
	if buf.Len() != 0 {
		t.Errorf("a declined error still wrote %q", buf.String())
	}
}

// Every section rta's own catalogue produces is addressable by a stable id.
//
// view.Section carries an ID and a Title because they are different jobs:
// the title is what a person reads and is therefore free to be reworded,
// while the id is what a script pulling one section out of a page, or an
// agent citing where a fact came from, addresses it by. Key() falls back to
// the title, so the fallback is not an error — but a catalogue that relies
// on it has made every wording improvement a silent breaking change for
// whoever scripted the old one, and the tension resolves by never improving
// the wording.
//
// A rule for rta, not for plugin authors: pkg/view deliberately leaves ID
// optional so a section pays for stability only when it wants it, and
// sdktest says so as a warning. rta's own pages are the ones agents read.
func TestEverySectionInTheCatalogueHasAStableID(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	idRe := regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	inputs := conformanceInputs(t.TempDir())

	var reached, skipped []string
	for _, c := range reg.Capabilities() {
		if c.Safety != plugin.Read {
			continue // a conformance run must not mutate anything
		}
		values := map[string]any{}
		maps.Copy(values, inputs[c.ID])
		if c.Detailed {
			values["detail"] = true
		}
		if missingInput(c, values) {
			skipped = append(skipped, c.ID)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		v, err := c.Run(ctx, plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false))
		cancel()
		if err != nil || v == nil {
			skipped = append(skipped, c.ID)
			continue
		}
		sections, ok := v.(view.Sections)
		if !ok {
			continue
		}
		reached = append(reached, c.ID)
		seen := map[string]bool{}
		for i, item := range sections.Items {
			switch {
			case item.ID == "":
				t.Errorf("%s section %d (%q) has no ID — its only stable handle is its heading",
					c.ID, i, item.Title)
			case !idRe.MatchString(item.ID):
				t.Errorf("%s section %d has ID %q, want lowercase kebab-case", c.ID, i, item.ID)
			case seen[item.ID]:
				t.Errorf("%s has two sections with ID %q, so neither can be addressed", c.ID, item.ID)
			}
			seen[item.ID] = true
		}
	}

	// Said out loud rather than left implicit: a test that quietly reached
	// three capabilities and passed would read as covering the catalogue.
	t.Logf("checked %d sectioned capabilities: %v", len(reached), reached)
	t.Logf("not reachable from the conformance inputs (%d): %v", len(skipped), skipped)
	if len(reached) < 8 {
		t.Errorf("only %d capabilities produced a sectioned page; the catalogue has more than that, "+
			"so this test has stopped covering what it claims", len(reached))
	}
}

// missingInput reports whether a capability still wants something no default
// and no conformance value supplies.
func missingInput(c plugin.Capability, values map[string]any) bool {
	for _, f := range c.Inputs {
		if !f.Required || f.Default != nil {
			continue
		}
		if _, ok := values[f.Name]; !ok {
			return true
		}
	}
	return false
}

// configCapability is a plugin whose inputs name config keys, registered
// as if it had been found on $PATH so the pin rules apply.
func configCapability(t *testing.T) (*registry.Registry, registry.Origin) {
	t.Helper()
	origin := registry.Origin{Path: "/usr/local/bin/rta-plugin-pg", Digest: "abcdef0123456789abcdef"}
	reg := registry.New()
	err := reg.RegisterFrom(plugin.Plugin{
		Name: "pg", Summary: "postgres", Capabilities: []plugin.Capability{{
			ID: "pg.query", Summary: "run a query", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Help: "host", Required: true, Config: "host"},
				{Name: "port", Type: plugin.Int, Help: "port", Default: 5432, Config: "port"},
				{Name: "verbose", Type: plugin.Bool, Help: "verbose", Config: "verbose"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.KeyValue{Pairs: []view.Pair{
					{Key: "host", Value: req.String("host")},
					{Key: "port", Value: strconv.Itoa(req.Int("port"))},
					{Key: "verbose", Value: strconv.FormatBool(req.Bool("verbose"))},
				}}, nil
			},
		}},
	}, origin)
	if err != nil {
		t.Fatal(err)
	}
	return reg, origin
}

// The end-to-end property, on the surface where it was structurally
// impossible before: cobra bakes every declared default into its flag set, so

// trustedConfig returns cfg as it would arrive from a config file somebody
// named. pluginconf.Resolve refuses every section of a file rta was not told
// to honour, and a config.Config literal is untrusted because the zero value
// is — so a test that hands Resolve a literal is now testing the refusal
// rather than whatever it meant to test. Going through the real loader is the
// idiom internal/profile's tests already use.
func trustedConfig(t *testing.T, cfg config.Config) config.Config {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
	loaded, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// collectValues read them all back and config could never be the thing that
// supplied a value.
func TestConfigFillsAnInputTheCallerDidNotPassOnTheCLI(t *testing.T) {
	reg, origin := configCapability(t)
	t.Cleanup(func() { SetPluginConfig(nil, nil) })
	SetPluginConfig(pluginconf.Resolve(trustedConfig(t, config.Config{Plugins: map[string]map[string]any{
		"pg@" + origin.Short(): {"host": "db.internal", "port": uint64(6543)},
	}}), reg.Origin))

	out, _, err := run(t, reg, "pg", "query")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "db.internal") {
		t.Errorf("output does not carry the configured host:\n%s", out)
	}
	if !strings.Contains(out, "6543") {
		t.Errorf("output does not carry the configured port:\n%s", out)
	}
}

// And the caller still wins, including for a bool set back to its zero value
// — which is the case a "did they pass it?" check written as a comparison
// against the default gets wrong.
func TestAPassedFlagBeatsConfigIncludingAFalseBool(t *testing.T) {
	reg, origin := configCapability(t)
	t.Cleanup(func() { SetPluginConfig(nil, nil) })
	SetPluginConfig(pluginconf.Resolve(trustedConfig(t, config.Config{Plugins: map[string]map[string]any{
		"pg@" + origin.Short(): {"host": "db.internal", "verbose": true},
	}}), reg.Origin))

	out, _, err := run(t, reg, "pg", "query", "--host", "typed.example", "--verbose=false")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "typed.example") {
		t.Errorf("the typed host did not win:\n%s", out)
	}
	if !strings.Contains(out, "false") {
		t.Errorf("--verbose=false did not beat the configured true:\n%s", out)
	}
}

// A required input that config can fill must not be rejected by cobra at
// parse time, which happens before anything has read the operator's file —
// and must still be reported when neither supplied it, naming both ways.
func TestARequiredInputIsSatisfiedByConfigAndReportedWhenNeitherSuppliesIt(t *testing.T) {
	reg, origin := configCapability(t)
	t.Cleanup(func() { SetPluginConfig(nil, nil) })

	SetPluginConfig(pluginconf.Resolve(trustedConfig(t, config.Config{Plugins: map[string]map[string]any{
		"pg@" + origin.Short(): {"host": "db.internal"},
	}}), reg.Origin))
	if _, _, err := run(t, reg, "pg", "query"); err != nil {
		t.Fatalf("a config-satisfied required input was still refused: %v", err)
	}

	SetPluginConfig(nil, nil)
	_, errOut, err := run(t, reg, "pg", "query")
	if err == nil {
		t.Fatal("a required input nothing supplied was accepted")
	}
	if !strings.Contains(errOut, "config") {
		t.Errorf("the message does not mention config as a way to supply it:\n%s", errOut)
	}
}

// failingCapability is configCapability's plugin with a capability that
// always fails, which is the only state where an unhonoured section explains
// anything.
func failingCapability(t *testing.T) (*registry.Registry, registry.Origin) {
	t.Helper()
	origin := registry.Origin{Path: "/usr/local/bin/rta-plugin-pg", Digest: "abcdef0123456789abcdef"}
	reg := registry.New()
	err := reg.RegisterFrom(plugin.Plugin{
		Name: "pg", Summary: "postgres", Capabilities: []plugin.Capability{{
			ID: "pg.query", Summary: "run a query", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Help: "host", Default: "localhost", Config: "host"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return nil, view.Errorf("pg.auth.failed",
					"%s rejected the credentials", req.String("host")).
					WithHint("check the role name")
			},
		}},
	}, origin)
	if err != nil {
		t.Fatal(err)
	}
	return reg, origin
}

// Rebuild a plugin and its digest changes, so the operator's section stops
// applying — by design. What they see is the capability failing
// against the *declared defaults*, with an error naming the database's
// refusal and nothing naming the reason it was asked that question. `rta
// doctor` knows; the failure did not say so.
func TestAFailureSaysWhenTheOperatorsSectionWasNotApplied(t *testing.T) {
	reg, _ := failingCapability(t)
	t.Cleanup(func() { SetPluginConfig(nil, nil) })
	SetPluginConfig(pluginconf.Resolve(trustedConfig(t, config.Config{Plugins: map[string]map[string]any{
		"pg@0000000000": {"host": "db.internal"}, // a pin from before the rebuild
	}}), reg.Origin))

	_, errOut, err := run(t, reg, "pg", "query")
	if err == nil {
		t.Fatal("the capability was supposed to fail")
	}
	// The plugin's own hint survives: rta is adding to it, not replacing it.
	if !strings.Contains(errOut, "check the role name") {
		t.Errorf("the plugin's hint was lost:\n%s", errOut)
	}
	if !strings.Contains(errOut, "plugins.pg@0000000000") {
		t.Errorf("the failure does not name the section rta ignored:\n%s", errOut)
	}
	if !strings.Contains(errOut, "abcdef012345") {
		t.Errorf("the failure does not name the installed digest to repin to:\n%s", errOut)
	}
}

// And it says nothing when there is nothing to say, on either axis: a section
// that applied, and a run that worked.
func TestNoNoteWhenTheSectionAppliedOrTheRunSucceeded(t *testing.T) {
	reg, origin := failingCapability(t)
	t.Cleanup(func() { SetPluginConfig(nil, nil) })
	SetPluginConfig(pluginconf.Resolve(trustedConfig(t, config.Config{Plugins: map[string]map[string]any{
		"pg@" + origin.Short(): {"host": "db.internal"},
	}}), reg.Origin))

	_, errOut, err := run(t, reg, "pg", "query")
	if err == nil {
		t.Fatal("the capability was supposed to fail")
	}
	if strings.Contains(errOut, "did not apply") {
		t.Errorf("a section that was applied was reported as ignored:\n%s", errOut)
	}

	// Somebody else's unhonoured section is somebody else's problem. Without
	// the namespace match this passes anyway, because a config whose only
	// section applied produces no problems at all — so the note has to be
	// offered a problem belonging to a different plugin.
	SetPluginConfig(pluginconf.Resolve(trustedConfig(t, config.Config{Plugins: map[string]map[string]any{
		"pg@" + origin.Short(): {"host": "db.internal"},
		"nosuchplugin@0000":    {"host": "elsewhere"},
	}}), reg.Origin))
	_, errOut, err = run(t, reg, "pg", "query")
	if err == nil {
		t.Fatal("the capability was supposed to fail")
	}
	if strings.Contains(errOut, "nosuchplugin") {
		t.Errorf("pg's failure reported another plugin's unhonoured section:\n%s", errOut)
	}

	// A run that succeeds says nothing either, however stale the pin — which
	// is SetPluginConfig's argument and the reason this is on failures only.
	okReg, _ := configCapability(t)
	SetPluginConfig(pluginconf.Resolve(trustedConfig(t, config.Config{Plugins: map[string]map[string]any{
		"pg@0000000000": {"host": "db.internal"},
	}}), okReg.Origin))
	out, errOut, err := run(t, okReg, "pg", "query", "--host", "typed.example")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out+errOut, "did not apply") {
		t.Errorf("a successful run carried the note:\n%s%s", out, errOut)
	}
}
