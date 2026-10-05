package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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
