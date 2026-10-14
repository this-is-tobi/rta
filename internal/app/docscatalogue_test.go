package app

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/wire"
	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// The docs tell a reader to type commands of the first-party plugins — `rta pg
// query`, `rta vault kv tree`, `rta kube ...` — and the default build does not
// have those commands: their source lives in rta-plugins, and a binary that
// carries none of them cannot say what `rta pg query` takes. So every such
// line the docs spell was a line no test could judge, and the three that were
// wrong (`--path` for an argument, a query with no SQL, twice) sat on the pages
// that send a reader to a database for as long as the page had been there.
//
// This file is what makes them judgeable without the plugins: a record of what
// each first-party capability declares as inputs — its names, types, which
// are arguments, which are required, which a profile fills, which a forward
// does — kept in testdata and registered next to the built-ins, so the same
// command tree a reader meets is built and the same parsing the real command
// does is what a docs line is held to. Nothing here runs a plugin and nothing
// is a second description of the CLI: the commands are made by the host's own
// code from the declarations, which is what stops a test's idea of "takes an
// argument" from drifting from the program's.
//
// It keeps the shape and leaves most of the words out. A capability's
// description, an input's help, its examples and keywords are what a plugin
// author rewrites most, and none of them decides whether `--set host=db`
// resolves, so they are not in the file. An input's default is left out for the
// same reason: it is the value a flag shows in --help, and a line is held to
// what an input takes (its type, its closed set, its bounds), not to what it
// does when nothing is typed. What stays is the ID, the safety class, whether a
// grant is needed, the scope, and for each input its name, type, short letter,
// closed set and bounds and who fills it, and one sentence: the capability's
// summary, because registration refuses a capability that has none. So the file
// moves when an input does and also when a plugin rewords a summary, and a
// regeneration whose only change is summaries is a harmless one.
//
// **It is a snapshot, and it says so.** What the plugins declare today is read
// from their binaries, which this repository does not have. A copy kept by
// hand would be the drift this exists to prevent, so the file is written by the
// test below from built plugins and checked against them the same way:
//
//	export RTA_FIRST_PARTY_PLUGINS=<dir holding rta-plugin-* built from rta-plugins>
//	go test ./internal/app -run TestTheFirstPartyCatalogue                         # fails when the file is behind
//	go test ./internal/app -run TestTheFirstPartyCatalogue -update-first-party    # rewrites it
//
// Without the variable the file is only read, which is all a run of the suite
// that has no plugin binaries can do: a plugin that changes an input is
// noticed by whoever next regenerates, and a page that spells an input the
// file lacks fails then, not before. Nothing prompts that regeneration, so it
// belongs to whoever moves the pin the plugins are built at.
//
// The rewrite has a flag of its own, and `-update`, which every golden file in
// this package shares, does not reach it. A shell that still exports the
// variable from an earlier session points at whatever those plugin binaries
// were built from, and a rewrite of the MCP surface that also rewrote this
// file from them would have put a stale declaration into the one record the
// docs are judged against.
var updateFirstParty = flag.Bool("update-first-party", false,
	"rewrite the first-party catalogue from the plugins RTA_FIRST_PARTY_PLUGINS names")

const firstPartyCatalogue = "testdata/first-party-plugins.jsonl"

// shapeOnly takes the words off one capability's declaration, leaving what a
// command line, a profile entry and a dashboard tile are checked against.
func shapeOnly(c *rtav1.Capability) {
	c.Description, c.Agent, c.Copy, c.Primary = "", "", "", ""
	c.Examples, c.Keywords, c.Actions, c.Toggles = nil, nil, nil, nil
	c.Idempotent, c.NoPreview, c.Reveals, c.Live, c.Flash, c.HasPrefill = false, false, false, false, false, false
	c.MinWidth, c.RefreshSeconds = 0, 0
	for _, f := range c.Inputs {
		f.Help, f.HasSuggest, f.Live, f.TlsAdjacent, f.Widens = "", false, false, false, ""
		f.Default = nil
	}
}

// catalogueLine is one capability as the file holds it: protojson, read back
// through a map so the keys are sorted and the line is the same bytes however
// the library chose to space it (protojson's output is deliberately unstable).
func catalogueLine(t *testing.T, c plugin.Capability) string {
	t.Helper()
	msg := wire.CapabilityToProto(c)
	shapeOnly(msg)
	raw, err := protojson.Marshal(msg)
	if err != nil {
		t.Fatalf("encoding %s: %v", c.ID, err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("re-reading %s: %v", c.ID, err)
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(generic); err != nil {
		t.Fatalf("writing %s: %v", c.ID, err)
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// firstPartyPlugins is the catalogue file read as the plugins it describes,
// one per namespace, with no handlers: nothing in it can run.
func firstPartyPlugins(t *testing.T) []plugin.Plugin {
	t.Helper()
	body, err := os.ReadFile(firstPartyCatalogue)
	if err != nil {
		t.Fatalf("reading the first-party catalogue: %v", err)
	}
	byNamespace := map[string]*plugin.Plugin{}
	var order []string
	for i, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var msg rtav1.Capability
		if err := protojson.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("%s:%d: %v", firstPartyCatalogue, i+1, err)
		}
		c, unknown := wire.CapabilityFromProto(&msg)
		if len(unknown) > 0 {
			t.Fatalf("%s:%d: %s declares something this build cannot read (%v); regenerate it",
				firstPartyCatalogue, i+1, c.ID, unknown)
		}
		// Registration refuses a capability that cannot be called, and a plugin's
		// is a call to its process. This one answers that it is a record: the
		// tests that use it build commands and read declarations and call nothing.
		id := c.ID
		c.Run = func(context.Context, plugin.Request) (view.View, error) {
			return nil, fmt.Errorf("%s is recorded for the docs tests and does not run", id)
		}
		ns := plugin.Namespace(c.ID)
		p, ok := byNamespace[ns]
		if !ok {
			p = &plugin.Plugin{Name: ns, Summary: "first-party " + ns + " plugin, as its declaration is recorded for the docs tests"}
			byNamespace[ns] = p
			order = append(order, ns)
		}
		p.Capabilities = append(p.Capabilities, c)
	}
	out := make([]plugin.Plugin, 0, len(order))
	for _, ns := range order {
		out = append(out, *byNamespace[ns])
	}
	return out
}

// documentedRegistry is what the docs are held to: the built-ins this binary
// carries and, registered beside them the way an installed plugin is, the
// first-party plugins the catalogue describes.
func documentedRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("building the built-in registry: %v", err)
	}
	for _, p := range firstPartyPlugins(t) {
		if err := reg.RegisterFrom(p, registry.Origin{}); err != nil {
			t.Fatalf("registering the recorded %s plugin: %v — if the rules a declaration is held to have changed, "+
				"the record is rewritten from built plugins, as the comment on firstPartyCatalogue says", p.Name, err)
		}
	}
	return reg
}

// declaredBy is the catalogue as the plugins in dir declare it: every
// rta-plugin-* binary there, launched sandboxed as a load launches it.
func declaredBy(t *testing.T, dir string) string {
	t.Helper()
	binaries, err := filepath.Glob(filepath.Join(dir, "rta-plugin-*"))
	if err != nil || len(binaries) == 0 {
		t.Fatalf("no rta-plugin-* binary in %s (%v)", dir, err)
	}
	var lines []string
	for _, bin := range binaries {
		declared, verr := plugindist.Describe(context.Background(), bin)
		if verr != nil {
			t.Fatalf("reading what %s declares: %v", bin, verr)
		}
		for _, c := range declared.Capabilities {
			lines = append(lines, catalogueLine(t, c))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// The catalogue is held to the plugins whenever there are plugins to hold it to,
// and to its own shape whenever there are not.
func TestTheFirstPartyCatalogueIsWhatThePluginsDeclare(t *testing.T) {
	if dir := os.Getenv("RTA_FIRST_PARTY_PLUGINS"); dir != "" {
		want := declaredBy(t, dir)
		if *updateFirstParty {
			if err := os.WriteFile(firstPartyCatalogue, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		got, err := os.ReadFile(firstPartyCatalogue)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s is behind the plugins in %s; regenerate it with -update-first-party", firstPartyCatalogue, dir)
		}
		return
	}
	if *updateFirstParty {
		t.Fatal("-update-first-party rewrites the first-party catalogue from built plugins, and RTA_FIRST_PARTY_PLUGINS names none")
	}

	// Written as the test writes it, so a hand edit or a format that moved is
	// found here and not as a docs line that quietly resolves against nothing.
	body, err := os.ReadFile(firstPartyCatalogue)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, p := range firstPartyPlugins(t) {
		for _, c := range p.Capabilities {
			lines = append(lines, catalogueLine(t, c))
		}
	}
	sort.Strings(lines)
	if got := strings.Join(lines, "\n") + "\n"; got != string(body) {
		t.Errorf("%s is not in the form the test writes it; regenerate it with -update-first-party", firstPartyCatalogue)
	}
}

// The README names the first-party plugins, and a page that spells one of their
// commands is only checked when the catalogue knows the plugin: a thirteenth
// added to the list and not to the file would be one more set of commands the
// docs are free to get wrong.
func TestTheFirstPartyCatalogueHoldsEveryPluginTheReadmeNames(t *testing.T) {
	want := firstPartyNames(t)
	have := map[string]bool{}
	for _, p := range firstPartyPlugins(t) {
		have[p.Name] = true
		if len(p.Capabilities) == 0 {
			t.Errorf("%s holds no capability of %s", firstPartyCatalogue, p.Name)
		}
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("the README names the first-party plugin %s and %s holds none of its capabilities; regenerate it with -update-first-party", name, firstPartyCatalogue)
		}
	}
	if len(have) != len(want) {
		t.Errorf("%s holds %d plugins, the README names %d", firstPartyCatalogue, len(have), len(want))
	}
}
