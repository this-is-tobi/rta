package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The grant roster is a table, and the tile draws one that does not fit its
// box as one line per grant with the cells joined, which is what a glance
// wants, while the roster itself stays a table that its row keys walk. A
// roster turned into text to fit a third of a screen would have kept the first
// and lost the second.
func TestTheRealRosterIsGlancedAsOneLinePerGrantAndStaysATable(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	m := Model{reg: realRegistry(t)}
	now := time.Now()
	for target, ttl := range map[string]time.Duration{
		"note.add": 15 * time.Minute, "net.dns": 30 * time.Minute, "kv.get": time.Hour} {
		if verr := grant.Issue(grant.Grant{Target: target, Agent: "claude",
			Issued: now, Expires: now.Add(ttl)}, true); verr != nil {
			t.Fatal(verr)
		}
	}
	list, _ := m.reg.Capability("grant.list")
	v, err := list.Run(context.Background(), plugin.NewRequest(nil, false, true).WithSurface(plugin.SurfaceTUI))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(view.Table); !ok {
		t.Fatalf("grant.list answered %s, want the roster table its rows are walked on", view.TypeOf(v))
	}
	want := map[string]string{"note.add": "15m", "net.dns": "30m", "kv.get": "1h"}
	for _, inner := range []int{36, 45, 60} {
		lines := strings.Split(plain(tileBody(v, inner)), "\n")
		for _, line := range lines {
			if w := len([]rune(line)); w > inner {
				t.Errorf("at %d cells a line is %d wide: %q", inner, w, line)
			}
		}
		for target, expires := range want {
			named := 0
			for _, line := range lines {
				if !strings.Contains(line, target) {
					continue
				}
				named++
				if !strings.Contains(line, expires) {
					t.Errorf("at %d cells the line for %s does not say it ends in %s: %q", inner, target, expires, line)
				}
			}
			if named != 1 {
				t.Errorf("at %d cells %s is named on %d lines, want one:\n%s", inner, target, named, strings.Join(lines, "\n"))
			}
		}
	}
}
