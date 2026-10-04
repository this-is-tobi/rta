package pkg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// stalledRelease serves owner/repo's latest release over TLS as release, with
// every download answering a few bytes and then nothing more until the test
// ends or the returned channel is closed: a download under way for as long as
// the test says. started is sent to once the first bytes are on their way.
func stalledRelease(t *testing.T, release func(base string) string) (c *registryClient, started <-chan string, unstall chan struct{}) {
	t.Helper()
	begun := make(chan string, 4)
	unstall = make(chan struct{})
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/tool/releases/latest" {
			_, _ = w.Write([]byte(release(srv.URL)))
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/dl/") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("the first bytes of a download"))
		w.(http.Flusher).Flush()
		begun <- strings.TrimPrefix(r.URL.Path, "/dl/")
		select {
		case <-unstall:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-unstall:
		default:
			close(unstall)
		}
	})
	old := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = old })
	c = newRegistryClient()
	c.http = srv.Client()
	c.github = srv.URL
	return c, begun, unstall
}

// forceExitDuring settles the process and runs its exit hooks while a
// download is still under way, as a forced exit does (internal/app's signal
// handling), and returns what left reports afterwards, before the process is
// let resume. The download is not held off an exit, so the settle must not
// wait for it.
func forceExitDuring(t *testing.T, left func() []string) []string {
	t.Helper()
	settled := make(chan func(), 1)
	go func() { settled <- shutdown.Settle() }()
	var resume func()
	select {
	case resume = <-settled:
	case <-time.After(time.Second):
		t.Fatal("the process waited for a download still under way")
	}
	shutdown.Exiting()
	behind := left()
	resume()
	return behind
}

func named(t *testing.T, dir, prefix string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			out = append(out, e.Name())
		}
	}
	return out
}

// A forced exit taken while a tool's upgrade downloads it leaves no staged
// download behind. The staging directory sits beside the tool, on $PATH's own
// directory, and its removal was a deferred call os.Exit skips: the partial
// download stayed there, in a dot-directory nothing lists, for good.
func TestAnExitDuringAToolsDownloadRemovesWhatItStaged(t *testing.T) {
	goos, arch := here(t)
	name := "tool-" + goos + "-" + arch
	c, started, unstall := stalledRelease(t, func(base string) string {
		return `{"tag_name":"v1.0.0","assets":[{"name":"` + name + `","browser_download_url":"` + base + `/dl/` + name +
			`","size":64,"digest":"sha256:` + strings.Repeat("ab", 32) + `"}]}`
	})
	dest, before := placedAt(t, "tool")

	upgraded := make(chan *view.Error, 1)
	go func() {
		_, verr := installTool(context.Background(), plugin.SurfaceCLI, c, tool{Bin: "tool", Owner: "o", Repo: "tool"}, false, false)
		upgraded <- verr
	}()
	<-started
	if staged := named(t, filepath.Dir(dest), ".tool-"); len(staged) != 1 {
		t.Fatalf("staging directories during the download: %v, want one", staged)
	}
	if behind := forceExitDuring(t, func() []string { return named(t, filepath.Dir(dest), ".tool-") }); len(behind) != 0 {
		t.Errorf("the exit left %v behind", behind)
	}
	close(unstall)
	if verr := <-upgraded; verr == nil {
		t.Error("a download whose digest the release does not publish was placed")
	}
	if got, _ := os.ReadFile(dest); string(got) != before {
		t.Errorf("the working tool was replaced with %q", got)
	}
}

// The same for the checksums file a release without an API digest is read
// from: it is downloaded into the temporary directory, and removed there by a
// call an exit taken during the download never reached.
func TestAnExitDuringAChecksumsDownloadRemovesIt(t *testing.T) {
	goos, arch := here(t)
	name := "tool-" + goos + "-" + arch
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	c, started, unstall := stalledRelease(t, func(base string) string {
		return `{"tag_name":"v1.0.0","assets":[` +
			`{"name":"` + name + `","browser_download_url":"` + base + `/dl/` + name + `","size":64},` +
			`{"name":"checksums.txt","browser_download_url":"` + base + `/dl/checksums.txt","size":64}]}`
	})
	placedAt(t, "tool")

	upgraded := make(chan *view.Error, 1)
	go func() {
		_, verr := installTool(context.Background(), plugin.SurfaceCLI, c, tool{Bin: "tool", Owner: "o", Repo: "tool"}, false, false)
		upgraded <- verr
	}()
	if got := <-started; got != "checksums.txt" {
		t.Fatalf("the first download was %s, want the checksums file", got)
	}
	if staged := named(t, tmp, "rta-checksums-"); len(staged) != 1 {
		t.Fatalf("checksums files during the download: %v, want one", staged)
	}
	if behind := forceExitDuring(t, func() []string { return named(t, tmp, "rta-checksums-") }); len(behind) != 0 {
		t.Errorf("the exit left %v behind", behind)
	}
	close(unstall)
	if verr := <-upgraded; verr == nil {
		t.Error("a release whose checksums name nothing was installed")
	}
}
