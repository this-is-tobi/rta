package lock

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A lock refused for one of its values names the input that holds it — the
// kind, the principal's name, the note, the window — so the lock form opens
// again on that box in the middle of an incident, and the remote pre-check
// names the same one the local command does. `all` beside a name is two
// inputs that disagree, and names neither.
func TestALockRefusalNamesTheInputItIsAbout(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml")) // never the machine's own remotes.yaml
	for _, c := range []struct {
		capID  string
		values map[string]any
		code   string
		input  string
	}{
		{"lock.add", map[string]any{"kind": "agnet", "name": "claude"}, "core.lock.kind", "kind"},
		{"lock.add", map[string]any{"name": " "}, "core.lock.name", "name"},
		{"lock.add", map[string]any{"name": "not a name"}, "grant.agent.charset", "name"},
		{"lock.add", map[string]any{"kind": "credential", "name": " padded"}, "core.lock.name", "name"},
		{"lock.add", map[string]any{"name": "claude", "note": strings.Repeat("n", 300)}, "core.lock.note", "note"},
		{"lock.add", map[string]any{"name": "claude", "ttl": "soon"}, "core.lock.ttl", "ttl"},
		{"lock.add", map[string]any{"name": "claude", "ttl": "soon", "server": "work"}, "core.lock.ttl", "ttl"},
		{"lock.add", map[string]any{"name": "claude", "all": true}, "core.lock.all", ""},
		{"lock.rm", map[string]any{}, "core.lock.name", "name"},
		{"lock.rm", map[string]any{"kind": "agnet", "name": "claude"}, "core.lock.kind", "kind"},
		{"lock.rm", map[string]any{"kind": "agnet", "name": "claude", "server": "work"}, "core.lock.kind", "kind"},
	} {
		refusing := capByID(t, c.capID)
		_, err := refusing.Run(context.Background(), req(c.values))
		verr, ok := err.(*view.Error)
		switch {
		case !ok || verr == nil || verr.Code != c.code:
			t.Errorf("%s %v: err = %v, want %s", c.capID, c.values, err, c.code)
		case verr.Input != c.input:
			t.Errorf("%s %v: %s names the input %q, want %q", c.capID, c.values, c.code, verr.Input, c.input)
		case c.input != "":
			if _, ok := refusing.ErrorInput(verr); !ok {
				t.Errorf("%s %v: %s names %q, which %s does not declare", c.capID, c.values, c.code, c.input, c.capID)
			}
		}
	}
}
