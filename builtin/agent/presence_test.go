package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Whether a server parks or refuses a call that needs a grant nobody
// issued, and where its paths are confined, were in the client's config
// file and on no screen of rta's.
func TestTheConnectedTableSaysWhetherAServerAsks(t *testing.T) {
	isolate(t)
	if err := session.Start(session.Record{
		ID: session.NewID(), Agent: "claude", Since: time.Now(), PID: os.Getpid(),
		Consent: true, Roots: []string{"/srv/app", "/tmp/scratch"},
	}); err != nil {
		t.Fatal(err)
	}
	v, err := run(t, "agent.overview", map[string]any{"detail": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range v.(view.Sections).Items {
		if s.ID != "connected" {
			continue
		}
		row := strings.Join(s.View.(view.Table).Rows[0], " | ")
		if !strings.Contains(row, "asks") || !strings.Contains(row, "/srv/app, /tmp/scratch") {
			t.Fatalf("row = %s", row)
		}
		return
	}
	t.Fatal("no connected section")
}
