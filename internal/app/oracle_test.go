package app

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// leavesTheMachine names the namespaces whose capabilities reach off the box.
//
// Network calls happen only on explicit user action, and a test
// suite is nobody asking. The existing conformance run gets this for free by
// declining to supply their required inputs; this test *supplies* a value, so
// it could turn an undrivable capability into a drivable one that resolves a
// filename as a hostname. Stated rather than relied upon.
var leavesTheMachine = map[string]bool{"cert": true, "http": true, "net": true}

// The MCP path gate hooks on Field.Type == Path. An input that names a file
// but is declared String, StringSlice or Text is invisible to it, so a caller
// gets an unconfined read through a capability whose schema never mentions a
// path. That shipped twice: cert.expiry's targets was a StringSlice and
// became a file-existence oracle over MCP with no flag and no grant, and
// kv.init's recipient echoed 32 bytes of any readable file back in a parse
// error.
//
// Both replacements so far asked the declaration a question — is this input
// Path? does its help mention a file? — and review recorded the real
// answer as an interprocedural AST walk of what handlers reach. This asks
// something better than either, because it is the property that actually
// matters rather than a proxy for it: *can a caller tell an existing file
// from a missing one through this input?* An oracle is what the gate exists
// to prevent, indirection cannot hide it, and no amount of reachability
// analysis is needed to observe it.
func TestNoInputBehavesLikeAFileWithoutBeingDeclaredOne(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_KV_PASSPHRASE", "")
	t.Setenv("RTA_KV_IDENTITY", "")
	inputs := conformanceInputs(dir)

	// ONE path, and the file under it appears and disappears between runs.
	//
	// The first version of this used two different paths, one existing and
	// one not, and reported four capabilities immediately — every one a false
	// positive. `todo.search` answers `No tasks match "<value>"`, so the
	// output differed because the *string* differed, not because anything was
	// read. Holding the value fixed and moving only the file makes the
	// filesystem the sole variable, which is what the question actually is.
	canary := filepath.Join(dir, "canary.txt")

	// outcome canonicalises what a caller can observe: the rendered view and
	// the error, which together are everything that crosses the boundary.
	outcome := func(c plugin.Capability, values map[string]any) string {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Write capabilities run as a dry run, which the contract makes
		// mandatory for them (proto CallRequest.dry_run: "a dry run that
		// performs the operation and then reports what would happen is worse
		// than no dry run"). Without them this test could not see the kv.init
		// shape at all, which is half of what this test exists to cover.
		v, err := c.Run(ctx, plugin.NewRequest(
			plugin.Resolve(c, plugin.Inputs{Caller: values}), c.Safety != plugin.Read, false))
		var buf bytes.Buffer
		if v != nil {
			_ = cli.Render(&buf, v, cli.Options{Format: cli.JSON, NoColor: true})
		}
		if err != nil {
			buf.WriteString("\nERR: " + err.Error())
		}
		return buf.String()
	}

	var tested, unstable, undrivable []string
	for _, c := range reg.Capabilities() {
		// Destructive is excluded even under dry run: it is the one class
		// where a handler that ignores the flag is unrecoverable, and it is
		// also the class an MCP caller cannot reach without a pinned
		// --allow-destructive and a human-issued grant.
		if c.Safety == plugin.Destructive {
			continue
		}
		if leavesTheMachine[c.Words()[0]] {
			continue
		}
		base := map[string]any{}
		maps.Copy(base, inputs[c.ID])
		if c.Detailed {
			base["detail"] = true
		}
		if missingInput(c, base) {
			undrivable = append(undrivable, c.ID)
			continue
		}
		for _, f := range c.Inputs {
			// Path is the declaration this test exists to demand, and Local
			// is never accepted from a remote caller at all.
			if f.Local || f.Type == plugin.Path {
				continue
			}
			if f.Type != plugin.String && f.Type != plugin.StringSlice && f.Type != plugin.Text {
				continue
			}
			values := map[string]any{}
			maps.Copy(values, base)
			if f.Type == plugin.StringSlice {
				values[f.Name] = []string{canary}
			} else {
				values[f.Name] = canary
			}
			absent := func() string {
				_ = os.Remove(canary)
				return outcome(c, values)
			}
			present := func() string {
				if err := os.WriteFile(canary, []byte("canary contents\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.Remove(canary) }()
				return outcome(c, values)
			}
			// Two runs with the same value first: a capability whose output
			// carries a timestamp, a duration or a map iteration order would
			// otherwise read as an oracle every time, and a test that cries
			// wolf gets deleted.
			a, b := absent(), absent()
			if a != b {
				unstable = append(unstable, c.ID+"."+f.Name)
				continue
			}
			tested = append(tested, c.ID+"."+f.Name)
			if got := present(); got != a {
				t.Errorf("%s: input %q is declared %s, but the answer changes depending on whether "+
					"the value names a file that exists — so it is a path, and the MCP gate "+
					"(checkPaths, which hooks Type == Path) never sees it. Declare it Path, or "+
					"Local if the host should resolve it.\n  with a missing file: %.200s\n  with an existing one: %.200s",
					c.ID, f.Name, f.Type, a, got)
			}
		}
	}

	// Said out loud, because the failure mode of this test is covering
	// nothing: every capability could be skipped as undrivable and it would
	// pass in silence, and that has happened here before.
	t.Logf("probed %d input(s) for file-oracle behaviour: %v", len(tested), tested)
	// The limits, measured rather than claimed, and the first measurement was
	// wrong so the correction is worth keeping: reverting kv.init's recipient
	// to non-Local leaves this green, and NOT because kv.init goes undriven.
	// It is driven, and the input *is* probed — the probe count goes 43 -> 44
	// and kv.init.recipient appears by name. It bails before the read, on the
	// passphrase this environment cannot supply, so parseRecipient never opens
	// the file and there is nothing for the canary to change. The cert.expiry
	// half is out of reach for a different reason: cert is excluded above for
	// reaching off the machine.
	//
	// So this catches neither of the two cases that motivated it, and says so.
	// What it does catch is the property itself, on all 44 inputs it can drive
	// across seven namespaces, with no reachability analysis and no way for
	// indirection to hide — which is why it earns its place regardless.
	t.Logf("not drivable from the conformance inputs, so not probed (%d): %v",
		len(undrivable), undrivable)
	if len(unstable) > 0 {
		t.Logf("non-deterministic, so not comparable (%d): %v", len(unstable), unstable)
	}
	if len(tested) < 12 {
		t.Errorf("only %d inputs were probed; the catalogue has more String-ish inputs on drivable "+
			"read capabilities than that, so this test has stopped covering what it claims", len(tested))
	}
}
