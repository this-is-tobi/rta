package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/render/theme/themetest"
	"github.com/this-is-tobi/rta/pkg/view"
)

// One of each kind of grade, in words and in numbers, with names that cannot be
// mistaken for the words they sit beside: the colour of each is read from the
// escape sequence in front of it, and the table of what it must be is the
// setting's definition (themetest.Drawn).
var (
	gradedWords = map[string]theme.StatusKind{
		"ready": theme.StatusGood, "pending": theme.StatusWarn, "NotReady": theme.StatusBad,
		"disabled": theme.StatusMuted, "zzz": theme.StatusNeutral,
	}
	gradedUsage = map[string]theme.StatusKind{
		"31%": theme.StatusGood, "84%": theme.StatusWarn, "96%": theme.StatusBad, "n/a": theme.StatusNeutral,
	}
)

func gradedTable() view.Table {
	return view.Table{
		Columns: []view.Column{
			{Name: "node"}, {Name: "state", Kind: view.KindStatus}, {Name: "disk", Kind: view.KindUsage},
			{Name: "class"},
		},
		Rows: [][]string{
			{"alpha", "ready", "31%", "longhorn-retain-replicated"},
			{"bravo", "pending", "84%", "longhorn-retain-replicated"},
			{"charlie", "NotReady", "96%", "longhorn-retain-replicated"},
			{"delta", "disabled", "n/a", "longhorn-retain-replicated"},
			{"echo", "zzz", "n/a", "longhorn-retain-replicated"},
		},
	}
}

func assertGraded(t *testing.T, out string, mode theme.Grades, cells map[string]theme.StatusKind) {
	t.Helper()
	for cell, kind := range cells {
		if cell == "n/a" {
			continue // twice in the table, which is not a word this reads the first of
		}
		if got, want := themetest.ColourBefore(t, out, cell), themetest.Drawn(mode, kind); got != want {
			t.Errorf("%q is drawn in %q under status: %s, want %q", cell, got, mode, want)
		}
	}
}

// The table, the record layout a narrow terminal gets instead, and a key/value
// page are three renderers of the same grade, and each is held to the setting
// through its real render function with the colour on.
func TestTheCommandLineDrawsEveryGradeAsTheSettingSays(t *testing.T) {
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		t.Run("a table", func(t *testing.T) {
			out := renderColoured(t, gradedTable(), 100)
			assertGraded(t, out, mode, gradedWords)
			assertGraded(t, out, mode, gradedUsage)
			if !strings.Contains(out, "╭") {
				t.Fatalf("not drawn as a grid at 100 cells:\n%s", out)
			}
		})
		t.Run("the record layout", func(t *testing.T) {
			out := renderColoured(t, gradedTable(), 30)
			if strings.Contains(out, "╭") {
				t.Fatalf("still a grid at 30 cells:\n%s", out)
			}
			assertGraded(t, out, mode, gradedWords)
			assertGraded(t, out, mode, gradedUsage)
		})
		t.Run("a key/value page", func(t *testing.T) {
			page := view.KeyValue{Pairs: []view.Pair{
				{Key: "context", Value: "lab"},
				{Key: "cluster", Value: "did not answer — connection refused"},
				{Key: "nodes", Value: "could not be read — forbidden"},
				{Key: "api", Value: "ready"},
				{Key: "extra", Value: "disabled"},
			}}
			out := renderColoured(t, page, 100)
			for value, kind := range map[string]theme.StatusKind{
				"did not answer — connection refused": theme.StatusBad,
				"could not be read — forbidden":       theme.StatusWarn,
				"ready":                               theme.StatusGood,
				"disabled":                            theme.StatusMuted,
				"lab":                                 theme.StatusNeutral,
			} {
				if got, want := themetest.ColourBefore(t, out, value), themetest.Drawn(mode, kind); got != want {
					t.Errorf("%q is drawn in %q under status: %s, want %q", value, got, mode, want)
				}
			}
		})
	})
}

// The safety class of a capability and the trust state of a plugin are drawn in a
// status colour and are not grades: they say what a call may do and whether code is
// trusted, so `rta plugin list`, `rta explain` and the grant tables keep the colour of
// a `destructive` or an `untrusted` under every setting, in a grid, in the record
// layout and in a key/value page.
func TestASafetyOrTrustCueKeepsItsColourUnderEverySetting(t *testing.T) {
	cues := map[string]theme.StatusKind{
		"read": theme.StatusGood, "write": theme.StatusWarn, "destructive": theme.StatusBad,
		"untrusted": theme.StatusWarn, "failed to start": theme.StatusBad,
	}
	names := map[string]string{
		"read": "alpha", "write": "bravo", "destructive": "charlie", "untrusted": "delta", "failed to start": "echo",
	}
	table := view.Table{Columns: []view.Column{
		{Name: "plugin"}, {Name: "can", Kind: view.KindStatus}, {Name: "summary"},
	}}
	page := view.KeyValue{}
	for word, name := range names {
		table.Rows = append(table.Rows, []string{name, word, "longhorn-retain-replicated"})
		page.Pairs = append(page.Pairs, view.Pair{Key: name, Value: word})
	}
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		for name, out := range map[string]string{
			"a table":           renderColoured(t, table, 100),
			"the record layout": renderColoured(t, table, 30),
			"a key/value page":  renderColoured(t, page, 100),
		} {
			for word, kind := range cues {
				if got, want := themetest.ColourBefore(t, out, word), themetest.Drawn(theme.GradesAll, kind); got != want {
					t.Errorf("%s: %q is drawn in %q under status: %s, want %q", name, word, got, mode, want)
				}
			}
		}
	})
}

// Nothing is hidden: the word, the number and the layout are the same under every
// setting, and the setting only takes colour from what grades. A renderer that
// dropped a quieted cell, or re-laid the grid for it, would fail here and not in
// the colour table above.
func TestQuietingTheGradesChangesNoTextAndNoLayout(t *testing.T) {
	theme.Apply(nil)
	t.Cleanup(func() { theme.Apply(nil) })
	widths := []int{100, 30}
	want := make([]string, len(widths))
	for i, width := range widths {
		want[i] = ansi.Strip(renderColoured(t, gradedTable(), width))
	}
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		for i, width := range widths {
			if got := ansi.Strip(renderColoured(t, gradedTable(), width)); got != want[i] {
				t.Errorf("width %d: status: %s changed the text or the layout:\n%s\nwant\n%s", width, mode, got, want[i])
			}
		}
	})
}

// NO_COLOR and --no-color are asked first: whatever the setting, a render with the
// colour off carries no escape sequence at all.
func TestNoColourWinsOverEverySetting(t *testing.T) {
	themetest.Each(t, func(t *testing.T, mode theme.Grades) {
		for _, v := range []view.View{gradedTable(), view.KeyValue{Pairs: []view.Pair{{Key: "api", Value: "NotReady"}}}} {
			var buf bytes.Buffer
			if err := Render(&buf, v, Options{NoColor: true, Width: 100}); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf.String(), "\x1b[") {
				t.Errorf("status: %s with NoColor still emitted an escape sequence:\n%q", mode, buf.String())
			}
			if !strings.Contains(buf.String(), "NotReady") {
				t.Errorf("the word is missing from the uncoloured render:\n%s", buf.String())
			}
		}
	})
}
