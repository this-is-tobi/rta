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
