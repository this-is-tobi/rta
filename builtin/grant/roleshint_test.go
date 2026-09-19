package grant

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/role"
	"github.com/this-is-tobi/rta/pkg/view"
)

// One failure, one answer, whichever surface reached it.
//
// role.unknown is raised from two places — internal/role.Find, behind
// `grant issue --role`, and runRoles, behind `grant roles <name>` — and the
// two had drifted into different hints for the identical sentence. Find's
// named the three files a `roles:` block can live in; runRoles' had shrunk
// to "`rta grant roles` lists them", which is the command the operator had
// just run and answers a question they had not asked. The half that was
// missing is the useful half, so both now say it, from one function.
func TestAnUnknownRoleGetsTheSameAnswerOnEverySurface(t *testing.T) {
	roleSetup(t)
	_, err := runRoles(context.Background(), req(map[string]any{"role": "nosuchrole"}))
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("err = %T (%v), want a *view.Error", err, err)
	}
	if verr.Code != "role.unknown" {
		t.Fatalf("code = %q, want role.unknown", verr.Code)
	}
	// Where a role comes from is the fact that turns "no role named X" into
	// a next step, and it is what the thin copy left out.
	if !strings.Contains(verr.Hint, "roles:") {
		t.Errorf("hint = %q, want it to name where roles are defined", verr.Hint)
	}
	// Named from `grant roles` itself, so it has to be unambiguous that the
	// listing is the bare command rather than the one that just failed.
	if strings.Contains(verr.Hint, "rta grant roles") && !strings.Contains(verr.Hint, "no argument") {
		t.Errorf("hint = %q, want the listing pointer to say it takes no argument", verr.Hint)
	}
	// Both surfaces, one wording.
	_, issueErr := role.Find("nosuchrole")
	if issueErr == nil {
		t.Fatal("role.Find accepted a name nothing defines")
	}
	if issueErr.Hint != verr.Hint {
		t.Errorf("grant issue hint = %q,\ngrant roles hint = %q — same failure, two answers",
			issueErr.Hint, verr.Hint)
	}
}
