package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// colourOf is the foreground colour a style paints with, as the sequence a
// terminal receives, for comparing against what a border was drawn in.
func colourOf(t *testing.T, style lipgloss.Style) string {
	t.Helper()
	return fgBefore(t, style.Render("x"), "x")
}

// borderOf is the colour a rendered panel's top-left corner is drawn in.
func borderOf(t *testing.T, panel string) string {
	t.Helper()
	return fgBefore(t, panel, "╭")
}

func nodesTile(cells ...string) tile {
	t := view.Table{Columns: []view.Column{{Name: "node"}, {Name: "status", Kind: view.KindStatus}}}
	for i, c := range cells {
		t.Rows = append(t.Rows, []string{fmt.Sprintf("node-%d", i), c})
	}
	return tile{cap: plugin.Capability{ID: "kube.node.list"}, view: t}
}

// **Trouble reached no colour the eye lands on.** Every tile border was the same
// faint grey in an ok, a down and a slow run: the kube tile with a NotReady node
// and an expired certificate had one coloured cell at most, and the cell that
// carried the verdict was often below the cut. The border says it, in the two
// colours a section header already uses for the same thing, so a dashboard of
// tiles shows which one is asking before anything is read.
func TestATileBorderIsDrawnInTheGradeOfWhatItSays(t *testing.T) {
	quiet := colourOf(t, theme.Border)
	warn, bad := colourOf(t, theme.WarnText), colourOf(t, theme.BadText)

	for name, tc := range map[string]struct {
		tile tile
		want string
	}{
		"a node not ready": {nodesTile("Ready", "NotReady"), bad},
		"a bad word":       {nodesTile("Ready", "expired"), bad},
		"a warning":        {nodesTile("Ready", "Terminating"), warn},
		"bad beats warn":   {nodesTile("Terminating", "NotReady", "Ready"), bad},
		"all fine":         {nodesTile("Ready", "Ready"), quiet},
		"nothing graded":   {tile{cap: plugin.Capability{ID: "x.facts"}, view: view.KeyValue{Pairs: []view.Pair{{Key: "a", Value: "b"}}}}, quiet},
		"a page that says its backend is down": {tile{
			cap: plugin.Capability{ID: "docker.overview"},
			view: view.KeyValue{Pairs: []view.Pair{
				{Key: "daemon", Value: "did not answer — Cannot connect to the Docker daemon"},
			}},
		}, bad},
		"a failed run": {tile{cap: plugin.Capability{ID: "db.status"}, err: view.Errorf("db.conn.refused", "nothing is listening")}, bad},
		"not yet run":  {tile{cap: plugin.Capability{ID: "db.status"}}, quiet},
		"nothing to say from here": {tile{
			cap: plugin.Capability{ID: "git.overview"},
			err: view.Errorf("git.notarepo", "not inside a git repository"),
		}, quiet},
	} {
		if got := borderOf(t, renderTile(tc.tile, 40, 8, false, false)); got != tc.want {
			t.Errorf("%s: the border is %s, want %s", name, got, tc.want)
		}
	}
}

// The grade is the whole answer's and not the rows that fit: a tile cuts at its
// height, and the failing row is the last one more often than not.
func TestATileWithItsFailingRowBelowTheCutStillDrawsTheBorderForIt(t *testing.T) {
	cells := make([]string, 30)
	for i := range cells {
		cells[i] = "Ready"
	}
	cells[len(cells)-1] = "NotReady"
	panel := renderTile(nodesTile(cells...), 40, 8, false, false)
	if strings.Contains(plain(panel), "NotReady") {
		t.Fatalf("the fixture shows the failing row, which is not what it tests:\n%s", plain(panel))
	}
	if got, want := borderOf(t, panel), colourOf(t, theme.BadText); got != want {
		t.Errorf("the border is %s, want the bad colour %s: the row that says so is cut", got, want)
	}
}

// Focus keeps the selection visible: the primary colour wins over the grade, as
// it does for a tile with calls waiting, and the grade is still in the words.
func TestTheSelectedTileIsStillDrawnInThePrimaryColourWhateverItSays(t *testing.T) {
	panel := renderTile(nodesTile("NotReady"), 40, 8, true, false)
	if got, want := borderOf(t, panel), colourOf(t, lipgloss.NewStyle().Foreground(theme.Primary)); got != want {
		t.Errorf("the selected tile's border is %s, want the primary colour %s", got, want)
	}
}

// Without colour the border says nothing, and the grade has to be in words: the
// cells that carry it are the same words in the same place, the focus is the
// heavy glyphs it always was, and nothing about the border adds a glyph or a
// word that only colour could have explained.
func TestAGradedTileUnderNoColourSaysItInWordsAndKeepsItsFrame(t *testing.T) {
	bad := nodesTile("Ready", "NotReady")
	rounded := plain(renderTile(bad, 40, 8, false, true))
	heavy := plain(renderTile(bad, 40, 8, true, true))
	if !strings.Contains(rounded, "NotReady") || !strings.HasPrefix(rounded, "╭─ kube.node.list") {
		t.Errorf("a bad tile without colour:\n%s", rounded)
	}
	if !strings.HasPrefix(heavy, "┏━ kube.node.list") {
		t.Errorf("the selected bad tile lost its heavy frame:\n%s", heavy)
	}
	assertExact(t, renderTile(bad, 40, 8, false, true), 40, 8)
	if plain(renderTile(nodesTile("Ready"), 40, 8, false, true))[:20] != rounded[:20] {
		t.Errorf("the grade changed the frame's glyphs")
	}
}

// The folded header already rolled a tile's grade up, from the same words; it
// reads a NotReady node as a bad tile now that the words are graded.
func TestAFoldedHeaderCountsANotReadyNodeAsBad(t *testing.T) {
	if g, _ := tileGrade(nodesTile("Ready", "NotReady")); g != gradeBad {
		t.Errorf("a tile with a NotReady node grades %v, want bad", g)
	}
	if g, _ := tileGrade(nodesTile("Ready", "Unknown")); g != gradeWarn {
		t.Errorf("a tile with an Unknown node grades %v, want warn", g)
	}
}
