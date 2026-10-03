package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

var (
	bigOnce sync.Once
	bigPath string
	bigErr  error
)

// big builds the plugin that answers with as many bytes as it is asked for.
func big(t *testing.T) string {
	t.Helper()
	bigOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rta-plugin-big-*")
		if err != nil {
			bigErr = err
			return
		}
		bigPath = filepath.Join(dir, BinaryName("big"))
		if out, err := exec.Command("go", "build", "-o", bigPath, "./testdata/bigplugin").CombinedOutput(); err != nil {
			bigErr = err
			t.Logf("building the big plugin: %s", out)
		}
	})
	if bigErr != nil {
		t.Fatalf("building the big plugin: %v", bigErr)
	}
	return bigPath
}

func dump(t *testing.T, bytes, limit int) (view.View, error) {
	t.Helper()
	h := New()
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), big(t))
	if err != nil {
		t.Fatalf("opening the plugin: %v", err)
	}
	var run plugin.Capability
	for _, cap := range c.Declared.Capabilities {
		if cap.ID == "big.dump" {
			run = cap
		}
	}
	req := plugin.NewRequest(map[string]any{"bytes": bytes}, false, false).WithResultLimit(limit)
	return run.Run(context.Background(), req)
}

// A plugin that answered with 100 MB took the server to 1.68 GB, because the
// host held the answer whole in the transport's buffer and again at every step
// that followed, and go-plugin lifts gRPC's own limit to the largest there is.
// The limit a call is given is handed to gRPC, which refuses from the length
// the message declares: the answer is not allocated, and what the caller is
// told is its size and how to ask for less.
func TestAnAnswerOverTheLimitIsRefusedBeforeItIsHeld(t *testing.T) {
	const answer = 64 << 20
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := dump(t, answer, 1<<20)
	runtime.ReadMemStats(&after)

	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "core.result.toolarge" {
		t.Fatalf("an answer of 64 MiB over a 1 MiB limit came back as %v, want core.result.toolarge", err)
	}
	if verr.Hint == "" {
		t.Error("the refusal says nothing of how to ask for less")
	}
	if held := after.TotalAlloc - before.TotalAlloc; held > 16<<20 {
		t.Errorf("the host allocated %d MiB for an answer it refused", held>>20)
	}
}

func TestAnAnswerUnderTheLimitIsReturned(t *testing.T) {
	v, err := dump(t, 100<<10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; len(body) != 100<<10 {
		t.Fatalf("the answer is %d bytes, want %d", len(body), 100<<10)
	}
}

// A surface that gave the call no limit, the CLI, is not bounded by this.
func TestACallGivenNoLimitIsNotBounded(t *testing.T) {
	v, err := dump(t, 6<<20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; len(body) != 6<<20 {
		t.Fatalf("the answer is %d bytes, want %d", len(body), 6<<20)
	}
}
