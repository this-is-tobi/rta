package lock

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func isolated(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

// knows makes this machine know an agent by name: connected, as a running
// server is.
func knows(t *testing.T, name string) {
	t.Helper()
	if err := session.Start(session.Record{
		ID: session.NewID(), Agent: name, Since: time.Now(), PID: os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}
}

func call(t *testing.T, id string, values map[string]any) (view.View, error) {
	t.Helper()
	return capByID(t, id).Run(context.Background(), req(values))
}

// receipt is the key/value receipt of a call, as a map.
func receipt(t *testing.T, id string, values map[string]any) map[string]string {
	t.Helper()
	v, err := call(t, id, values)
	if err != nil {
		t.Fatal(err)
	}
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("got %s, want a receipt", view.TypeOf(v))
	}
	out := map[string]string{}
	for _, p := range kv.Pairs {
		out[p.Key] = p.Value
	}
	return out
}

// placed is a lock added, failing the test if it was refused.
func placed(t *testing.T, values map[string]any) {
	t.Helper()
	if _, err := call(t, "lock.add", values); err != nil {
		t.Fatal(err)
	}
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("want a *view.Error, got %T: %v", err, err)
	}
	return verr.Code
}

// A real stop used to be one lock per agent plus a revoke, and an agent that
// connected after the operator started typing was not covered.
func TestLockAddAllFreezesEveryAgentIncludingOnesThatHaveNotConnected(t *testing.T) {
	isolated(t)
	got := receipt(t, "lock.add", map[string]any{"all": true, "note": "paused while we read the refusals"})
	if got["locked"] != "every agent" {
		t.Errorf("the receipt says %q locked, want every agent", got["locked"])
	}
	if !strings.Contains(got["effect"], "any that connects later") {
		t.Errorf("the receipt does not say it covers an agent that has not connected: %q", got["effect"])
	}
	if want := "somebody runs `rta lock rm --all`"; got["until"] != want {
		t.Errorf("until = %q, want %q", got["until"], want)
	}
	for _, agent := range []string{"claude", "a-name-nobody-has-used"} {
		if l, _ := lockdown.NewPin().Check(agent, ""); l == nil {
			t.Errorf("%s was let through", agent)
		}
	}
	if _, noted := got["note"]; noted {
		t.Error("a lock on every agent warned about a name it does not have")
	}
}

func TestLockAddAllIsTheAgentKindsAloneAndNeedsNoName(t *testing.T) {
	isolated(t)
	for name, c := range map[string]struct {
		values map[string]any
		code   string
	}{
		"a name beside it":         {map[string]any{"all": true, "name": "claude"}, "core.lock.all"},
		"another kind":             {map[string]any{"all": true, "kind": "credential"}, "core.lock.all"},
		"an operator, all of them": {map[string]any{"all": true, "kind": "operator"}, "core.lock.all"},
		"neither a name nor all":   {map[string]any{}, "core.lock.name"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := call(t, "lock.add", c.values)
			if err == nil || codeOf(t, err) != c.code {
				t.Fatalf("got %v, want %s", err, c.code)
			}
		})
	}
	if locks, _ := lockdown.Load(); len(locks) != 0 {
		t.Errorf("a refused call placed %+v", locks)
	}
	if _, err := call(t, "lock.rm", map[string]any{}); err == nil || codeOf(t, err) != "core.lock.name" {
		t.Errorf("lock rm with nothing named: %v", err)
	}
}

func TestLockAddAllDryRunSaysWhatItWouldFreezeAndHowToLiftIt(t *testing.T) {
	isolated(t)
	dry := plugin.NewRequest(map[string]any{"all": true, "ttl": "1d"}, true, true).WithSurface(plugin.SurfaceCLI)
	v, err := capByID(t, "lock.add").Run(context.Background(), dry)
	if err != nil {
		t.Fatal(err)
	}
	body := v.(view.Text).Body
	for _, want := range []string{"would lock every agent", "`rta lock rm --all`", "lifting itself at"} {
		if !strings.Contains(body, want) {
			t.Errorf("dry run says %q, want %q", body, want)
		}
	}
	if locks, _ := lockdown.Load(); len(locks) != 0 {
		t.Errorf("a dry run placed %+v", locks)
	}
}

// One row, listed as the others are, and said in words beside the table where
// a column of names would leave a person to decode a `*`.
func TestTheLockOnEveryAgentIsOneRowWithItsMeaningBesideTheTable(t *testing.T) {
	isolated(t)
	placed(t, map[string]any{"all": true})
	placed(t, map[string]any{"name": "claude"})
	v, err := call(t, "lock.list", nil)
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) != 2 {
		t.Fatalf("%d rows, want the wildcard and claude: %v", len(tbl.Rows), tbl.Rows)
	}
	var everyone []string
	for _, row := range tbl.Rows {
		if row[1] == lockdown.Everyone {
			everyone = row
		}
	}
	if everyone == nil || everyone[0] != "agent" {
		t.Fatalf("no agent row named %s: %v", lockdown.Everyone, tbl.Rows)
	}
	if len(tbl.Warnings) != 1 || tbl.Warnings[0].Code != "lock.everyone" || !tbl.Warnings[0].Advisory ||
		!strings.Contains(tbl.Warnings[0].Hint, "`rta lock rm --all`") {
		t.Errorf("warnings = %+v, want one advisory that says what the row means and how to lift it", tbl.Warnings)
	}
	if _, err := call(t, "lock.rm", map[string]any{"all": true}); err != nil {
		t.Fatal(err)
	}
	v, _ = call(t, "lock.list", nil)
	if left := v.(view.Table); len(left.Warnings) != 0 || len(left.Rows) != 1 {
		t.Errorf("after lifting it: %+v", left)
	}
}

// Lifted as it was placed. A lock on one agent was placed on purpose, and an
// operator who has just lifted the stop should not have to wonder whether it
// went with it.
func TestLiftingTheLockOnEveryAgentLeavesTheOnesOnSingleAgents(t *testing.T) {
	isolated(t)
	placed(t, map[string]any{"all": true})
	placed(t, map[string]any{"name": "claude"})
	got := receipt(t, "lock.rm", map[string]any{"all": true})
	if got["unlocked"] != "every agent" {
		t.Errorf("unlocked = %q", got["unlocked"])
	}
	if !strings.HasPrefix(got["still locked"], "claude") {
		t.Errorf("the receipt does not name the lock that stands: %v", got)
	}
	if l, _ := lockdown.NewPin().Check("claude", ""); l == nil {
		t.Error("claude was unfrozen with everybody")
	}
	if l, _ := lockdown.NewPin().Check("cursor", ""); l != nil {
		t.Error("cursor is still frozen")
	}
	again := receipt(t, "lock.rm", map[string]any{"all": true})
	if got := again["nothing to lift"]; got != "no lock on every agent stands" {
		t.Errorf("lifting what is not there says %v", again)
	}
}

// In an incident a slip of the keyboard is the likeliest way to freeze nobody
// while the screen says locked, and grant allow already says so.
func TestALockOnANameNobodyUsedSaysSoBesideTheNearestOne(t *testing.T) {
	isolated(t)
	knows(t, "claude")
	got := receipt(t, "lock.add", map[string]any{"name": "claudee"})
	for _, want := range []string{`no agent named "claudee"`, "this machine knows claude", "did you mean claude?"} {
		if !strings.Contains(got["note"], want) {
			t.Errorf("note = %q, want it to say %q", got["note"], want)
		}
	}
	if l, _ := lockdown.NewPin().Check("claudee", ""); l == nil {
		t.Error("the warning stopped the lock being placed: it is a note, not a refusal")
	}
	known := receipt(t, "lock.add", map[string]any{"name": "claude"})
	if _, noted := known["note"]; noted {
		t.Errorf("a name this machine knows was warned about: %q", known["note"])
	}
	other := receipt(t, "lock.add", map[string]any{"kind": "credential", "name": "ci-token"})
	if _, noted := other["note"]; noted {
		t.Errorf("a credential, whose names this machine cannot list, was warned about: %q", other["note"])
	}
	dry := plugin.NewRequest(map[string]any{"name": "claudee"}, true, true).WithSurface(plugin.SurfaceCLI)
	v, _ := capByID(t, "lock.add").Run(context.Background(), dry)
	if body := v.(view.Text).Body; !strings.Contains(body, "did you mean claude?") {
		t.Errorf("the dry run, where finding this out is the point, says %q", body)
	}
}

func TestTheNameCompletesFromTheAgentsThisMachineHasSeen(t *testing.T) {
	isolated(t)
	knows(t, "claude")
	f := capByID(t, "lock.add").Inputs[1]
	if f.Name != "name" || f.Suggest == nil {
		t.Fatalf("the name input has no completion: %+v", f)
	}
	if got := f.Suggest(context.Background(), req(nil)); !slices.Contains(got, "claude") {
		t.Errorf("agent completion = %v, want claude", got)
	}
	if got := f.Suggest(context.Background(), req(map[string]any{"kind": "credential"})); got != nil {
		t.Errorf("credential completion = %v: names the machine cannot list are typed", got)
	}
}

func TestALockWindowCanBeGivenInDays(t *testing.T) {
	isolated(t)
	placed(t, map[string]any{"name": "claude", "ttl": "1d"})
	locks, verr := lockdown.Load()
	if verr != nil || len(locks) != 1 {
		t.Fatalf("locks = %+v, %v", locks, verr)
	}
	if left := time.Until(locks[0].Expires); left < 23*time.Hour || left > 25*time.Hour {
		t.Errorf("1d lifts in %v", left)
	}
	if _, err := call(t, "lock.add", map[string]any{"name": "claude", "ttl": "tomorrow"}); err == nil ||
		codeOf(t, err) != "core.lock.ttl" {
		t.Errorf("a window that is none: %v", err)
	}
}

// A `*` offered as a name to lift is a glob the shell expands before rta reads it;
// the row on every agent is lifted by --all, which is what the receipt says.
func TestTheLocksOfferedToLiftDoNotIncludeTheOneOnEveryAgent(t *testing.T) {
	isolated(t)
	receipt(t, "lock.add", map[string]any{"all": true})
	receipt(t, "lock.add", map[string]any{"name": "claude"})
	got := suggestLockedNames(context.Background(), plugin.NewRequest(map[string]any{"kind": "agent"}, false, false))
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("offered %v to lift, want claude alone", got)
	}
}
