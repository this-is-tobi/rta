package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func gitRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{Name: "git", Summary: "git", Capabilities: []plugin.Capability{{
		ID: "git.overview", Summary: "the repository at a glance", Safety: plugin.Read, Idempotent: true,
		Inputs: []plugin.Field{{Name: "path", Type: plugin.String, Help: "a directory"}},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			return nil, view.Errorf("git.notarepo", "%s is not inside a git repository", "/home/somebody/projects").
				WithHint("run this against a directory inside a git repository")
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// The landing screen is opened from wherever the shell is, which is usually not
// a repository, and the first thing a newcomer saw was a red ERROR badge on a
// tile they never asked for. The tile says what is true instead — there is no
// repository here — in the muted voice an empty notebook has.
// The docker and kube overviews answer with a key/value page, not an error, when
// their backend is not there, and a section of them folded to a header that said
// nothing: the rollup read a key/value page only for the calls the agent tile has
// waiting. It reads the words a value begins with, the ones a status cell is
// coloured by, so what is red on the tile is what the header counts. A value that
// is a good word says nothing: "up 3 days" is a fact, and the header says ok only
// when a tile said so.
func TestAKeyValueTileThatSaysItsBackendIsDownGradesBad(t *testing.T) {
	for name, tc := range map[string]struct {
		pairs []view.Pair
		want  grade
	}{
		"down":      {[]view.Pair{{Key: "context", Value: "lab"}, {Key: "cluster", Value: "did not answer — connection refused"}}, gradeBad},
		"refused":   {[]view.Pair{{Key: "cluster", Value: "answered, and refused — namespaces is forbidden"}}, gradeBad},
		"partial":   {[]view.Pair{{Key: "cluster", Value: "answering"}, {Key: "nodes", Value: "could not be read — forbidden"}}, gradeWarn},
		"worst":     {[]view.Pair{{Key: "nodes", Value: "could not be read — forbidden"}, {Key: "cluster", Value: "was never contacted — no kubeconfig"}}, gradeBad},
		"facts":     {[]view.Pair{{Key: "context", Value: "lab"}, {Key: "namespaces", Value: "12"}}, gradeNone},
		"a fact up": {[]view.Pair{{Key: "uptime", Value: "up 3 days"}, {Key: "mode", Value: "read-only"}}, gradeNone},
		// Vault's overview leads with its seal state, the one thing it is for.
		"sealed":        {[]view.Pair{{Key: "state", Value: "sealed — 0 of 1 unseal key shares given — 1.14.4"}}, gradeBad},
		"unreadable":    {[]view.Pair{{Key: "state", Value: "unreadable — profile prod refused"}}, gradeWarn},
		"a sealed name": {[]view.Pair{{Key: "engine", Value: "sealed-secrets — controller"}}, gradeNone},
	} {
		if got := viewGrade(view.KeyValue{Pairs: tc.pairs}); got != tc.want {
			t.Errorf("%s: graded %v, want %v", name, got, tc.want)
		}
	}
}

func TestTheGitTileOutsideARepositoryIsAQuietSentenceNotAnError(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m := filled(t, New(gitRegistry(t), config.Dashboard{}, nil), 80, 24)
	frame := plain(m.View().Content)
	if strings.Contains(frame, "ERROR") || strings.Contains(frame, "HINT") || strings.Contains(frame, "git.notarepo") {
		t.Errorf("the tile draws the error badge:\n%s", frame)
	}
	if !strings.Contains(frame, "Not inside a git repository") {
		t.Errorf("the tile does not say there is no repository here:\n%s", frame)
	}
}

// A tile that was given a directory asked about that directory, and a directory
// that is not a repository is a mistake worth a badge. Enter opens the error
// whole in either case, which is where its hint is.
func TestAGitTileGivenADirectoryStillReportsTheError(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	dash := config.Dashboard{Tiles: []config.Tile{{ID: "git.overview", With: map[string]any{"path": "/srv/not-a-repo"}}}}
	m := filled(t, New(gitRegistry(t), dash, nil), 80, 24)
	frame := plain(m.View().Content)
	if !strings.Contains(frame, "ERROR") || !strings.Contains(frame, "git.notarepo") {
		t.Errorf("a tile pointed at a directory lost its error:\n%s", frame)
	}
}

func parked(n string) view.View {
	return view.KeyValue{Pairs: []view.Pair{
		{Key: waitingKey, Value: n},
		{Key: "connected now", Value: "1"},
	}}
}

func TestWaitingCallsReadsTheCountTheAgentTileStates(t *testing.T) {
	for _, tc := range []struct {
		v    view.View
		want int
	}{
		{parked("0"), 0},
		{parked("2 — press w to answer"), 2},
		{parked("1 — see rta agent pending"), 1},
		{parked("unreadable — the queue is not readable"), 0},
		{view.KeyValue{Pairs: []view.Pair{{Key: "connected now", Value: "3"}}}, 0},
		{view.Text{Body: waitingKey + " 4"}, 0},
		{nil, 0},
	} {
		if got := waitingCalls(tc.v); got != tc.want {
			t.Errorf("waitingCalls(%v) = %d, want %d", tc.v, got, tc.want)
		}
	}
}

// A call that is parked has a clock on it, and the tile that says so is not
// always on the first row of a dashboard somebody arranged. The badge is in the
// tile's own border and in words, so it survives a terminal with no colour.
func TestATileWithParkedCallsSaysSoInItsBorder(t *testing.T) {
	waiting := tile{cap: plugin.Capability{ID: "agent.overview"}, view: parked("2 — press w to answer")}
	quiet := tile{cap: plugin.Capability{ID: "agent.overview"}, view: parked("0")}
	if got := plain(renderTile(waiting, 44, 8, false, false)); !strings.Contains(strings.SplitN(got, "\n", 2)[0], "● 2 waiting") {
		t.Errorf("the border does not say two calls are waiting:\n%s", got)
	}
	if got := plain(renderTile(quiet, 44, 8, false, false)); strings.Contains(got, "waiting\n") || strings.Contains(strings.SplitN(got, "\n", 2)[0], "●") {
		t.Errorf("a tile with nothing parked carries the badge:\n%s", got)
	}
	if got := plain(renderTile(waiting, 44, 8, true, true)); !strings.Contains(strings.SplitN(got, "\n", 2)[0], "● 2 waiting") {
		t.Errorf("selecting the tile hid the badge:\n%s", got)
	}
}

// "press a to add one" is the first instruction on a fresh install's dashboard,
// and nothing is selected there: the search bar holds every letter, so the key
// the tile named started a search for it. An empty tile that names one of its
// own keys says to select it first until it is selected, and the opened list,
// where the key answers at once, keeps the plugin's sentence as written.
func TestAnEmptyTileThatNamesItsOwnKeySaysToSelectItFirst(t *testing.T) {
	notes := tile{
		cap: plugin.Capability{ID: "note.list", Actions: []plugin.Action{{Key: "a", Label: "add", Target: "note.add"}}},
		view: view.Table{
			Columns: []view.Column{{Name: "ID"}},
			Empty:   "Nothing here yet — press a to add one",
		},
	}
	unselected := ansi.Strip(renderTile(notes, 90, 6, false, false))
	if !strings.Contains(unselected, "select this tile, then press a to add one") {
		t.Errorf("an unselected tile promises a key that is a letter of the search:\n%s", unselected)
	}
	selected := ansi.Strip(renderTile(notes, 90, 6, true, false))
	if !strings.Contains(selected, "press a to add one") || strings.Contains(selected, "select this tile") {
		t.Errorf("the selected tile, where the key answers:\n%s", selected)
	}

	// Only a key the capability declares for its tile, and only an empty list.
	notes.cap.Actions = nil
	if got := ansi.Strip(renderTile(notes, 90, 6, false, false)); strings.Contains(got, "select this tile") {
		t.Errorf("a key the tile does not declare was qualified:\n%s", got)
	}
	notes.cap.Actions = []plugin.Action{{Key: "a", Label: "add", Target: "note.add"}}
	notes.view = view.Table{
		Columns: []view.Column{{Name: "ID"}},
		Rows:    [][]string{{"1"}},
		Empty:   "Nothing here yet — press a to add one",
	}
	if got := ansi.Strip(renderTile(notes, 90, 6, false, false)); strings.Contains(got, "select this tile") {
		t.Errorf("a tile with rows was qualified:\n%s", got)
	}
}

// The sentence is a plugin's own words, so it is read the way a person reads
// it: a capital at the start of a sentence, a key that is a symbol, and a
// sentence that already says to select a tile are all ordinary, and each left
// the promise standing, or said twice, when the match was a `\b` pattern for a
// word character.
func TestTheKeyAnEmptyTilePromisesIsFoundTheWayAPersonReadsIt(t *testing.T) {
	for _, tc := range []struct{ key, empty, want string }{
		{"a", "Press a to add one.", "Select this tile, then press a to add one."},
		{"+", "Nothing here — press + to add one", "Nothing here — select this tile, then press + to add one"},
		{"a", "Select a tile, then press a to add one", "Select a tile, then press a to add one"},
		{"a", "Nothing here — press ab to add one", "Nothing here — press ab to add one"},
		{"a", "Nothing here — depress a lever", "Nothing here — depress a lever"},
		{"a", "Nothing here yet — press a to add one", "Nothing here yet — select this tile, then press a to add one"},
		{"A", "Nothing here yet — press a to add one", "Nothing here yet — press a to add one"},
		{"a", "Nothing here yet…press a to add one", "Nothing here yet…select this tile, then press a to add one"},
		{"a", "No note selected — press a to add one", "No note selected — select this tile, then press a to add one"},
		{"a", "Nothing here — press aé to add one", "Nothing here — press aé to add one"},
	} {
		tl := tile{
			cap:  plugin.Capability{ID: "x.list", Actions: []plugin.Action{{Key: tc.key, Label: "add", Target: "x.add"}}},
			view: view.Table{Columns: []view.Column{{Name: "ID"}}, Empty: tc.empty},
		}
		got := keyPromised(tl, false).(view.Table).Empty
		if got != tc.want {
			t.Errorf("key %q, %q is drawn as %q, want %q", tc.key, tc.empty, got, tc.want)
		}
	}
}

// The rewrite runs for every empty tile on every frame, and compiling a pattern
// from each declared key each time was the dearest thing a frame did for a tile
// with nothing in it.
func TestTheEmptyTileRewriteAllocatesLittleOnEveryFrame(t *testing.T) {
	tl := tile{
		cap: plugin.Capability{ID: "x.list", Actions: []plugin.Action{
			{Key: "u", Label: "update", Target: "x.edit"}, {Key: "t", Label: "toggle", Target: "x.toggle"},
			{Key: "d", Label: "done", Target: "x.done"}, {Key: "a", Label: "add", Target: "x.add"},
		}},
		view: view.Table{Columns: []view.Column{{Name: "ID"}}, Empty: "Nothing here yet — press a to add one"},
	}
	if allocs := testing.AllocsPerRun(50, func() { _ = keyPromised(tl, false) }); allocs > 8 {
		t.Errorf("keyPromised allocated %.0f times for one empty tile, want a handful", allocs)
	}
}

// The dashboard reads the count off the line the agent tile labels
// waitingKey, and the plugin is a different package: a rewording on either side
// would silently stop the badge, which is a failure nobody would see until a
// call expired unanswered. The real tile has to carry the line, as a number.
func TestTheAgentTilesWaitingLineIsTheOneTheDashboardReads(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m, _ := realModel(t, 100, 40)
	m = filled(t, m, 100, 40)
	for _, tl := range m.tiles {
		if tl.cap.ID != "agent.overview" {
			continue
		}
		kv, ok := tl.view.(view.KeyValue)
		if !ok {
			t.Fatalf("agent.overview answered %T, which the badge cannot read", tl.view)
		}
		for _, p := range kv.Pairs {
			if p.Key == waitingKey {
				if p.Value != "0" {
					t.Errorf("a clean sandbox has %q parked", p.Value)
				}
				return
			}
		}
		t.Fatalf("agent.overview has no %q line: %v", waitingKey, kv.Pairs)
	}
	t.Fatal("the landing dashboard has no agent.overview tile")
}
