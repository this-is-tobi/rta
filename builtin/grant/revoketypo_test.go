package grant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// "No active grant for X." is true of a name that was mistyped, and it reads
// as all clear: a transposed letter left a destructive grant standing while
// the operator, and any script, took it for gone. A name that no registered
// capability or plugin answers to, and that matched nothing, is refused as
// `grant allow` refuses the same name, with the nearest grant that does stand.
func TestARevokeThatNamesNothingThatExistsIsRefusedAndPointsAtTheNearestGrant(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "agent": "claude"})

	_, err := revokeH(context.Background(), req(map[string]any{"target": "kv.gett"}))
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "grant.unknowntarget" {
		t.Fatalf("error = %v, want grant.unknowntarget", err)
	}
	if !strings.Contains(ve.Hint, "did you mean kv.get") {
		t.Errorf("hint %q does not point at the grant that stands", ve.Hint)
	}
	if grants, _ := core.Load(); len(grants) != 1 {
		t.Errorf("a refused revoke changed the store: %+v", grants)
	}

	// A preview is held to the same: it is how somebody checks before they commit.
	_, err = revokeH(context.Background(),
		plugin.NewRequest(map[string]any{"target": "kv.gett"}, true, true).WithSurface(plugin.SurfaceCLI))
	if !errors.As(err, &ve) || ve.Code != "grant.unknowntarget" {
		t.Errorf("a dry run said %v, want grant.unknowntarget", err)
	}
}

// A grant for a capability that has since left the catalogue (a plugin that
// was removed) is still the operator's to take back, so a name nothing answers
// to is refused only when it also matched no grant.
func TestAGrantForACapabilityThatIsNoLongerRegisteredCanStillBeRevoked(t *testing.T) {
	setup(t)
	if verr := core.Issue(core.Grant{Target: "gone.thing", Agent: "claude",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour)}, true); verr != nil {
		t.Fatal(verr)
	}
	body := run(t, revokeH, map[string]any{"target": "gone.thing"}).(view.Text).Body
	if !strings.HasPrefix(body, "revoked 1 grant") {
		t.Errorf("body = %q", body)
	}
	if grants, _ := core.Load(); len(grants) != 0 {
		t.Errorf("the grant survived: %+v", grants)
	}
}

// What was already true stays true: a name that exists and has no grant is
// told so (as before), and so is any name when nothing at all is allowed.
func TestARevokeOfARealNameWithNoGrantAndOfAnyNameWithNoGrantsAtAllIsToldAsBefore(t *testing.T) {
	setup(t)
	body := run(t, revokeH, map[string]any{"target": "kv.gett"}).(view.Text).Body
	if first, _, _ := strings.Cut(body, "\n"); first != "Nothing to revoke — no grant is active." {
		t.Errorf("with nothing allowed, a typo said %q", body)
	}
	run(t, allowH, map[string]any{"target": "kv.get", "agent": "claude"})
	body = run(t, revokeH, map[string]any{"target": "kv.env"}).(view.Text).Body
	if first, _, _ := strings.Cut(body, "\n"); first != "No active grant for kv.env." {
		t.Errorf("a real name with no grant said %q", body)
	}
}

// A typo too far from anything that stands gets no guess, only the way to see
// what does.
func TestARevokeNamingNothingNearAnyGrantOnlyPointsAtTheList(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "agent": "claude"})
	_, err := revokeH(context.Background(), req(map[string]any{"target": "zzzzzzzz"}))
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "grant.unknowntarget" {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(ve.Hint, "did you mean") || !strings.Contains(ve.Hint, "grant list") {
		t.Errorf("hint = %q", ve.Hint)
	}
}

// "No grant is held under it" has to be true, so the refusal is made only
// when nothing stands under the name whatever else the revoke narrowed itself
// by. A grant for a name nothing answers to any more, missed by --agent or by
// --exact, is the plain sentence naming what the revoke was narrowed to — and
// never a hint that offers back the name that was just typed.
func TestARevokeNarrowedPastAGrantForAnUnregisteredNameIsNotToldNoGrantIsHeld(t *testing.T) {
	setup(t)
	if verr := core.Issue(core.Grant{Target: "gone.thing", Agent: "claude",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour)}, true); verr != nil {
		t.Fatal(verr)
	}
	for name, values := range map[string]map[string]any{
		"another agent":          {"target": "gone.thing", "agent": "codex"},
		"exact, naming no agent": {"target": "gone.thing", "exact": true},
	} {
		body := run(t, revokeH, values).(view.Text).Body
		if !strings.HasPrefix(body, "No active grant") {
			t.Errorf("%s: body = %q, want the plain no-grant sentence", name, body)
		}
	}
	if grants, _ := core.Load(); len(grants) != 1 {
		t.Errorf("a revoke that matched nothing changed the store: %+v", grants)
	}
}

// The operator channel's revoke is the same revoke on another machine, and the
// mistyped name that read as all clear there is the costlier one: the server is
// the one other people's agents are on. It is refused from the server's own
// catalogue, as a dry run and as the real thing, with the command that takes
// the nearest grant back — which says whose server.
func TestTheOperatorChannelsRevokeRefusesAMistypedNameToo(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "agent": "claude"})
	revoke := RevokeRemote("claude", catalog)

	for _, write := range []bool{true, false} {
		_, verr := revoke(operatorid.RevokeSpec{Target: "kv.gett"}, write)
		if verr == nil || verr.Code != "grant.unknowntarget" {
			t.Fatalf("write=%v: error = %v, want grant.unknowntarget", write, verr)
		}
		if !strings.Contains(verr.Hint, "did you mean kv.get") || !strings.Contains(verr.Hint, "--server <name>") {
			t.Errorf("write=%v: hint %q does not name the nearest grant and the server", write, verr.Hint)
		}
	}
	if grants, _ := core.Load(); len(grants) != 1 {
		t.Errorf("a refused revoke changed the store: %+v", grants)
	}

	// What a real name is told is as before, a grant for it taken back or not.
	if out, verr := revoke(operatorid.RevokeSpec{Target: "kv.env"}, true); verr != nil || out.Revoked != 0 {
		t.Errorf("a real name with no grant: %+v, %v", out, verr)
	}
	if out, verr := revoke(operatorid.RevokeSpec{Target: "kv.get"}, true); verr != nil || out.Revoked != 1 {
		t.Errorf("a real name with a grant: %+v, %v", out, verr)
	}
}
