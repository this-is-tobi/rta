package grant

import (
	"context"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A plugin build's digest, as the registry records one, and the build that
// replaced it.
const (
	helloBuild   = "5dae737f8845c0ffee0123456789abcdef0123456789abcdef0123456789abcd"
	helloRebuilt = "9f1c2e3d4b5a60718293a4b5c6d7e8f90123456789abcdef0123456789abcdef"
)

// answering is a registry in which hello is the external plugin built as
// digest, and everything else is built in; with digest empty, nothing
// answers for hello at all.
func answering(digest string) func(string) (string, bool) {
	return func(ns string) (string, bool) {
		if ns != "hello" {
			return "", true
		}
		return digest, digest != ""
	}
}

// helloGrants stands a grant on the built-in kv.get beside one on hello.wipe
// issued against helloBuild.
func helloGrants(t *testing.T) {
	t.Helper()
	now := time.Now()
	for _, g := range []core.Grant{
		{Target: "kv.get", Scope: "db-password", Agent: "test", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "hello.wipe", Agent: "test", Digest: helloBuild, Issued: now, Expires: now.Add(time.Hour)},
	} {
		if verr := core.Issue(g, true); verr != nil {
			t.Fatal(verr)
		}
	}
}

func listWith(t *testing.T, artifact func(string) (string, bool), detail bool) view.Table {
	t.Helper()
	v, err := runList(context.Background(), req(map[string]any{"detail": detail}), catalog, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if !detail {
		return listed(t, v)
	}
	for _, s := range v.(view.Sections).Items {
		if s.ID == "granted" {
			return listed(t, s.View)
		}
	}
	t.Fatal("the detail page has no granted section")
	return view.Table{}
}

func hasColumn(tbl view.Table, name string) bool {
	for _, c := range tbl.Columns {
		if c.Name == name {
			return true
		}
	}
	return false
}

// rowFor is the row whose Capability is target.
func rowFor(t *testing.T, tbl view.Table, target string) int {
	t.Helper()
	for i := range tbl.Rows {
		if cell(t, tbl, i, "Capability") == target {
			return i
		}
	}
	t.Fatalf("no row for %s in %q", target, tbl.Rows)
	return -1
}

// --detail names the build each grant is bound to: the short digest of a
// plugin's artifact, and "built in" for a namespace the rta binary answers
// for itself. The compact roster, the dashboard tile's, leaves the column
// out while every grant's build is the one answering.
func TestTheDetailPageNamesTheBuildEachGrantIsBoundTo(t *testing.T) {
	setup(t)
	helloGrants(t)
	tbl := listWith(t, answering(helloBuild), true)
	if got := cell(t, tbl, rowFor(t, tbl, "hello.wipe"), "Artifact"); got != helloBuild[:12] {
		t.Errorf("hello.wipe's artifact reads %q, want the short digest %q", got, helloBuild[:12])
	}
	if got := cell(t, tbl, rowFor(t, tbl, "kv.get"), "Artifact"); got != "built in" {
		t.Errorf("kv.get's artifact reads %q, want built in", got)
	}
	if len(tbl.Warnings) != 0 {
		t.Errorf("a roster whose builds all answer warns %v", tbl.Warnings)
	}
	if compact := listWith(t, answering(helloBuild), false); hasColumn(compact, "Artifact") {
		t.Errorf("the compact roster shows the Artifact column with nothing to report: %v", compact.Columns)
	}
}

// Upgrading a plugin invalidates the grants standing on it, and the roster
// said nothing: each read as live while every call it was issued for was
// refused. It is marked where it stands, on the compact roster too, and the
// warning beside the rows says the fix is issuing it again — coded, so -o
// json carries it.
func TestAGrantOnAReplacedPluginIsMarkedWithTheFix(t *testing.T) {
	setup(t)
	helloGrants(t)
	for _, detail := range []bool{false, true} {
		tbl := listWith(t, answering(helloRebuilt), detail)
		if got := cell(t, tbl, rowFor(t, tbl, "hello.wipe"), "Artifact"); got != helloBuild[:12]+" (replaced)" {
			t.Errorf("detail=%v: hello.wipe's artifact reads %q, want its build marked replaced", detail, got)
		}
		if got := cell(t, tbl, rowFor(t, tbl, "kv.get"), "Artifact"); got != "built in" {
			t.Errorf("detail=%v: kv.get's artifact reads %q, want built in, unmarked", detail, got)
		}
		if cell(t, tbl, rowFor(t, tbl, "hello.wipe"), "Capability") != "hello.wipe" || tbl.Columns[0].Name != "Capability" {
			t.Errorf("detail=%v: the capability is no longer the row's first cell: %v", detail, tbl.Columns)
		}
		w := warning(t, tbl, "grant.artifact.replaced")
		if !w.Advisory {
			t.Errorf("detail=%v: the warning is not advisory, so a roster missing nothing is headed partial", detail)
		}
		for _, want := range []string{"1 grant was issued on a plugin that has been replaced", "authorizes nothing", "hello.wipe"} {
			if !strings.Contains(w.Message, want) {
				t.Errorf("detail=%v: the warning reads %q, want it to say %q", detail, w.Message, want)
			}
		}
		for _, want := range []string{"issue it again after the upgrade", "`rta grant allow`", "`rta grant renew` moves a deadline"} {
			if !strings.Contains(w.Hint, want) {
				t.Errorf("detail=%v: the hint reads %q, want it to say %q", detail, w.Hint, want)
			}
		}
	}
	raw, err := view.Marshal(view.Envelope{View: view.Redact(listWith(t, answering(helloRebuilt), false))})
	if err != nil || !strings.Contains(string(raw), `"grant.artifact.replaced"`) || !strings.Contains(string(raw), "(replaced)") ||
		!strings.Contains(string(raw), `"advisory":true`) {
		t.Errorf("json = %s (%v), want the mark and the coded warning", raw, err)
	}
}

// A grant on a plugin nothing answers for now is marked apart from one on a
// replaced build: the fix starts with the plugin loading at all.
func TestAGrantOnAPluginThatDoesNotLoadIsMarkedSo(t *testing.T) {
	setup(t)
	helloGrants(t)
	tbl := listWith(t, answering(""), false)
	if got := cell(t, tbl, rowFor(t, tbl, "hello.wipe"), "Artifact"); got != helloBuild[:12]+" (not loaded)" {
		t.Errorf("hello.wipe's artifact reads %q, want its build marked not loaded", got)
	}
	w := warning(t, tbl, "grant.artifact.gone")
	if !w.Advisory {
		t.Error("the warning is not advisory, so a roster missing nothing is headed partial")
	}
	if !strings.Contains(w.Message, "hello.wipe") || !strings.Contains(w.Hint, "rta plugin list") {
		t.Errorf("the warning reads %q / %q, want the grant named and where to look", w.Message, w.Hint)
	}
}

// A remote roster is judged by the server that holds it: this machine's
// plugins say nothing about that server's, so no build is marked here.
func TestARemoteRosterMarksNoBuild(t *testing.T) {
	tbl := grantsTable([]core.Grant{{Target: "hello.wipe", Digest: helloBuild, Expires: time.Now().Add(time.Hour)}}, nil, nil, false)
	if hasColumn(tbl, "Artifact") || len(tbl.Warnings) != 0 {
		t.Errorf("a remote roster = %v / %v, want no Artifact column and no warning", tbl.Columns, tbl.Warnings)
	}
}

// renew moves a deadline and never rebinds a grant, so renewing one whose
// plugin was replaced printed a fresh deadline on a grant that covers no
// call. It says so, and what fixes it.
func TestRenewSaysWhichGrantsNoLongerMatchTheirPlugin(t *testing.T) {
	setup(t)
	helloGrants(t)
	v, err := runRenew(context.Background(), req(nil), answering(helloRebuilt))
	if err != nil {
		t.Fatal(err)
	}
	body := v.(view.Text).Body
	for _, want := range []string{"note: 1 of these is bound to a plugin build that no longer answers (hello.wipe)",
		"so the deadline moved and it still authorizes nothing — `rta grant allow` issues it again"} {
		if !strings.Contains(body, want) {
			t.Errorf("renew said %q, want it to say %q", body, want)
		}
	}
	if v, _ := runRenew(context.Background(), req(nil), answering(helloBuild)); strings.Contains(v.(view.Text).Body, "no longer answers") {
		t.Errorf("renew warned with every build answering: %q", v.(view.Text).Body)
	}
}

func warning(t *testing.T, tbl view.Table, code string) view.Error {
	t.Helper()
	for _, w := range tbl.Warnings {
		if w.Code == code {
			return w
		}
	}
	t.Fatalf("no %s warning in %v", code, tbl.Warnings)
	return view.Error{}
}
