package grant

import (
	"bytes"
	"context"
	"strings"
	"testing"

	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// reachCatalog is storeCatalog with a record to name: kv.get takes a key.
func reachCatalog() []plugin.Capability {
	caps := storeCatalog()
	caps[0].Scope = "key"
	caps[0].Inputs = []plugin.Field{{Name: "key", Type: plugin.String}}
	return caps
}

// rolesH is runRoles over the store catalogue, the way listH adapts runList.
func rolesH(ctx context.Context, req plugin.Request) (view.View, error) {
	return runRoles(ctx, req, reachCatalog)
}

const (
	breadthOfKV = "kv covers 3 capabilities an agent needs a grant for, 1 destructive (kv.rm) — name one to allow only it"
	revealsOfKV = "kv covers 2 capabilities that reveal stored values (kv.env, kv.get), each for every record it reaches"
)

// **A role is a list of grants, and `grant roles` is where it is read before it
// is issued.** A team's role of `kv` and `net` printed as those two words, and
// `--yes` after reading them issued everything either plugin could do; typed as
// `grant allow kv`, the same line says how many capabilities it covers, which
// are destructive and which reveal stored values.
func TestRolesSayHowFarAWholePluginLineReaches(t *testing.T) {
	configDir, _ := roleSetup(t)
	ownRole(t, configDir, "  oncall:\n    grants: [kv, kv.get db-password]\n")

	tbl := run(t, rolesH, map[string]any{"role": "oncall"}).(view.Table)
	var said []string
	for _, w := range tbl.Warnings {
		if !w.Advisory {
			t.Errorf("an advisory about a role is a warning of a complete answer: %+v", w)
		}
		said = append(said, w.Message)
	}
	got := strings.Join(said, "\n")
	for _, want := range []string{"oncall, kv: " + breadthOfKV, "oncall, kv: " + revealsOfKV} {
		if !strings.Contains(got, want) {
			t.Errorf("the warnings read\n%s\nwant %q", got, want)
		}
	}
	if strings.Contains(got, "db-password") {
		t.Errorf("a line that names a record, so one capability, was warned about: %s", got)
	}
	if len(tbl.Rows) != 1 {
		t.Errorf("the rows changed: %v", tbl.Rows)
	}
}

// The plan before anything is signed, the dry run and the receipt are the same
// lines, and carry the same words.
func TestARoleSaysHowFarItReachesBeforeAfterAndInsteadOfIssuing(t *testing.T) {
	configDir, _ := roleSetup(t)
	ownRole(t, configDir, "  oncall:\n    grants: [kv, kv.get db-password]\n")
	issueKV := func(values map[string]any, dry bool) view.Sections {
		t.Helper()
		v, err := runIssue(issueReq(values, dry, false), reachCatalog, builtIn)
		if err != nil {
			t.Fatal(err)
		}
		return v.(view.Sections)
	}

	dry := issueKV(map[string]any{"role": "oncall", "agent": "probe"}, true)
	if body := dry.Items[0].View.(view.Text).Body; !strings.Contains(body, "kv: "+breadthOfKV) || !strings.Contains(body, "kv: "+revealsOfKV) {
		t.Errorf("the dry run reads\n%s", body)
	}

	issued := issueKV(map[string]any{"role": "oncall", "agent": "probe"}, false)
	note := pair(receipt(issued), "note")
	if !strings.Contains(note, "kv: "+breadthOfKV) || !strings.Contains(note, "kv: "+revealsOfKV) {
		t.Errorf("the receipt reads\n%s", note)
	}
	if strings.Contains(note, "note: ") {
		t.Errorf("the advisory is introduced by its own word twice: %s", note)
	}

	// And the page read before the passphrase is asked for.
	iss, verr := planIssue(issueReq(map[string]any{"role": "oncall", "agent": "probe"}, false, false), reachCatalog)
	if verr != nil {
		t.Fatal(verr)
	}
	prepared, notes, verr := iss.build(plugin.SurfaceCLI, reachCatalog, builtIn, "test")
	if verr != nil {
		t.Fatal(verr)
	}
	var out bytes.Buffer
	iss.listBeforeSigning(&out, plugin.SurfaceCLI, prepared, make([]string, len(prepared)), notes, nil)
	for _, want := range []string{"kv for ", "kv.get db-password for ", "kv: " + breadthOfKV, "kv: " + revealsOfKV} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the page before the passphrase reads\n%s\nwant %q", out.String(), want)
		}
	}
}

// And the receipt of a grant issued on a server, which is that server's own
// catalogue's count.
func TestTheReceiptOfARemoteGrantSaysHowFarItReaches(t *testing.T) {
	setup(t)
	p, verr := PrepareRemote(reachCatalog, builtIn)(operatorid.IssueSpec{Target: "kv", Agent: "work"}, "alice")
	if verr != nil {
		t.Fatal(verr)
	}
	got := strings.Join(p.Notes, "\n")
	for _, want := range []string{"note: " + breadthOfKV, "note: " + revealsOfKV} {
		if !strings.Contains(got, want) {
			t.Errorf("the prepared grant's notes read\n%s\nwant %q", got, want)
		}
	}
	one, verr := PrepareRemote(reachCatalog, builtIn)(operatorid.IssueSpec{Target: "kv.get", Scope: "db-password", Agent: "work"}, "alice")
	if verr != nil {
		t.Fatal(verr)
	}
	if strings.Contains(strings.Join(one.Notes, "\n"), "covers") {
		t.Errorf("a grant on one capability and one record is warned about: %v", one.Notes)
	}
}
