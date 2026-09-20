package agent

import (
	"os"
	"runtime"
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

// A session store rta cannot read is not a session store with nothing open.
// Before this, session.List()'s error was discarded inside openSessions and
// an unreadable directory rendered identically to "nothing is connected" —
// the exact question an operator opens the dashboard to ask during an
// incident, answered with confidence from a state that was actually unknown.
func TestConnectedNowIsUnreadableWhenTheSessionStoreCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not deny the owner here")
	}
	isolate(t)
	if err := session.Start(session.Record{
		ID: session.NewID(), Agent: "claude", Since: time.Now(), PID: os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(session.Dir(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(session.Dir(), 0o700) })

	v, err := run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := overviewPair(t, v, "connected now"); !strings.HasPrefix(got, "unreadable") {
		t.Fatalf("connected now = %q, want it to say the session store could not be read", got)
	}
}
