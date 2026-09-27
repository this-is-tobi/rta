package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The grant list the TUI shows marks a grant whose plugin build no longer
// answers, and says beside it that issuing it again is the fix — the pane
// draws what the CLI draws — and the row still acts on its grant: the mark
// sits in a column of its own, and the capability stays the row's first.
//
// The build answering for kv is the rta binary itself, so a grant bound to
// a plugin artifact for it is one whose build was replaced: the state an
// upgrade leaves every grant on a plugin in.
func TestTheGrantListMarksAGrantOnAReplacedBuild(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg := realRegistry(t)
	now := time.Now()
	if verr := grant.Issue(grant.Grant{Target: "kv.get", Scope: "db", Agent: "claude",
		Digest: "5dae737f8845c0ffee0123456789abcdef0123456789abcdef0123456789abcd",
		Issued: now, Expires: now.Add(time.Hour)}, true); verr != nil {
		t.Fatal(verr)
	}
	list, _ := reg.Capability("grant.list")
	v, err := list.Run(context.Background(), plugin.NewRequest(nil, false, true).WithSurface(plugin.SurfaceTUI))
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	shown, _ := sized.(Model).Update(resultMsg{cap: list, view: v})
	pane := shown.(Model).resultView()
	for _, want := range []string{"5dae737f8845 (replaced)", "grant.artifact.replaced", "issue it again after the upgrade"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the grant list pane does not say %q:\n%s", want, pane)
		}
	}

	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("grant.list answered %s, want the roster table", view.TypeOf(v))
	}
	var revoke capAction
	for _, a := range capActions(reg, "grant.list") {
		if a.cap.ID == "grant.revoke" {
			revoke = a
		}
	}
	base, ok := (Model{reg: reg}).actionSeed(revoke, tbl)
	if !ok || base["target"] != "kv.get" || base["scope"] != "db" {
		t.Errorf("x on the marked row seeded %v, want its grant", base)
	}
}
