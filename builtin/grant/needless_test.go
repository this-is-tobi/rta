package grant

import (
	"context"
	"strings"
	"testing"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A grant on a read used to succeed and list as live, while the gate never
// consulted it: reads are free, so the row authorized nothing. The same dead
// end a typo'd target is refused for, reached by a correct spelling.
func TestAGrantOnAReadIsRefusedAsNeedless(t *testing.T) {
	setup(t)
	_, err := allowH(context.Background(), req(map[string]any{"target": "todo.list"}))
	var verr *view.Error
	if !asError(err, &verr) || verr.Code != "grant.needless" {
		t.Fatalf("err = %v, want grant.needless", err)
	}
	if !strings.Contains(verr.Message, "todo.list needs no grant") {
		t.Errorf("message = %q, want it to name the read", verr.Message)
	}
	if grants, _ := core.Load(); len(grants) != 0 {
		t.Errorf("the needless grant was written anyway: %+v", grants)
	}
}

// A plugin name is asked the same question of everything in it, and a
// capability reserved for the person at the terminal is never a tool.
func TestAGrantNothingCouldSpendIsRefusedWhateverItNames(t *testing.T) {
	setup(t)
	cat := func() []plugin.Capability {
		return []plugin.Capability{
			{ID: "sys.host", Summary: "host", Safety: plugin.Read},
			{ID: "sys.load", Summary: "load", Safety: plugin.Read},
			{ID: "grant.list", Summary: "list", Safety: plugin.Read, HumanOnly: true},
			{ID: "note.add", Summary: "add", Safety: plugin.Write},
		}
	}
	allow := func(target string) error {
		_, err := runAllow(context.Background(), req(map[string]any{"target": target}), cat, builtIn)
		return err
	}
	for _, target := range []string{"sys", "grant.list", "grant"} {
		var verr *view.Error
		if err := allow(target); !asError(err, &verr) || verr.Code != "grant.needless" {
			t.Errorf("%s: err = %v, want grant.needless", target, err)
		}
	}
	if err := allow("note"); err != nil {
		t.Errorf("a plugin with a write in it is grantable: %v", err)
	}
}

// Not a refusal — the first grant on a fresh machine names a client that has
// not connected yet — but said, beside the names this machine has seen, so
// a grant to "cluade" is not a row that looks live and matches nobody.
func TestNamingAnAgentThisMachineHasNotSeenIsSaid(t *testing.T) {
	setup(t)
	v := run(t, allowH, map[string]any{"target": "kv.get", "agent": "tset"})
	body := v.(view.Text).Body
	if !strings.Contains(body, `no agent named "tset"`) || !strings.Contains(body, "knows test") {
		t.Errorf("body = %q, want the unknown name beside the known one", body)
	}
	v = run(t, allowH, map[string]any{"target": "kv.env", "agent": "test"})
	if body := v.(view.Text).Body; strings.Contains(body, "no agent named") {
		t.Errorf("a known agent drew the note: %q", body)
	}
}

// The prompt already refused a passphrase on argv when the guard was on;
// with it off nothing read the flag, and the command succeeded with the
// value in shell history.
func TestAPassphraseOnArgvIsRefusedWithTheGuardOff(t *testing.T) {
	setup(t)
	_, err := allowH(context.Background(), req(map[string]any{"target": "kv.get", "passphrase": "x"}))
	var verr *view.Error
	if !asError(err, &verr) || verr.Code != "core.guard.passphrase.argv" {
		t.Fatalf("err = %v, want the argv refusal", err)
	}
	if grants, _ := core.Load(); len(grants) != 0 {
		t.Errorf("the grant was written anyway: %+v", grants)
	}
	// The TUI's masked field is a channel that lands nowhere, and is not argv.
	if _, err := allowH(context.Background(), reqTUI(map[string]any{"target": "kv.get", "passphrase": "x"})); err != nil {
		t.Errorf("the TUI field was refused: %v", err)
	}
}

// A dry run exists to find out what would happen, and a window shorter than
// asked is the most common thing there is to find out.
func TestADryRunSaysWhenTheWindowWasCapped(t *testing.T) {
	setup(t)
	dry := plugin.NewRequest(map[string]any{"target": "kv.get", "ttl": "72h"}, true, true).WithSurface(plugin.SurfaceCLI)
	v, err := allowH(context.Background(), dry)
	if err != nil {
		t.Fatal(err)
	}
	body := v.(view.Text).Body
	if !strings.HasPrefix(body, "would let") {
		t.Fatalf("body = %q, want a dry run", body)
	}
	if grants, _ := core.Load(); len(grants) != 0 {
		t.Errorf("the dry run wrote a grant: %+v", grants)
	}
	if !strings.Contains(body, "for 24h") || !strings.Contains(body, "capped at the 24h maximum (you asked for 72h)") {
		t.Errorf("body = %q, want the cap named in a person's units", body)
	}
	if strings.Contains(body, "0m0s") {
		t.Errorf("body = %q, still prints durations the way time.Duration does", body)
	}
}
