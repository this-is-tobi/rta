package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// What `rta http get URL > file` and `| jq .` receive is the response, whole,
// and a response rta could not read whole is a failure: the 4 KiB a screen
// shows used to be all a pipe got, with exit 0 and a note on stderr only.
func TestHTTPGetIntoAPipeWritesTheWholeBodyAndFailsWhenItWasCut(t *testing.T) {
	text := strings.Repeat("a line of text in the file\n", 1100)
	tooLong := strings.Repeat("0123456789abcdef\n", (1<<20)/17+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if r.URL.Path == "/long" {
			_, _ = w.Write([]byte(tooLong))
			return
		}
		_, _ = w.Write([]byte(text))
	}))
	defer srv.Close()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, err := run(t, reg, "http", "get", srv.URL+"/text", "--local-network")
	if code := ExitCode(err); code != 0 || out != text {
		t.Errorf("a body of %d bytes: exit %d, stdout %d bytes (%v)", len(text), code, len(out), err)
	}
	if !strings.Contains(errOut, "# status: 200 OK") {
		t.Errorf("the status is not on stderr:\n%s", errOut)
	}

	out, errOut, err = run(t, reg, "http", "get", srv.URL+"/long", "--local-network")
	if code := ExitCode(err); code != 1 {
		t.Errorf("a body past the cap: exit %d (%v), want 1", code, err)
	}
	if want := tooLong[:1<<20]; out != want && out != want+"\n" {
		t.Errorf("a body past the cap: stdout is %d bytes, want the first %d", len(out), 1<<20)
	}
	if !strings.Contains(errOut, "http.body.truncated") || !strings.Contains(errOut, "1 MiB") {
		t.Errorf("stderr does not say the body was cut at the cap:\n%s", errOut)
	}
}

// A dry run is how a person checks where a token would go before it goes there,
// and `http get` sent the request: its --dry-run is accepted by every command,
// and the POST form already prints "nothing was sent".
func TestHTTPGetDryRunSendsNothing(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}

	for _, verb := range []string{"get", "head"} {
		out, _, err := run(t, reg, "http", verb, srv.URL+"/typo", "--local-network",
			"--bearer", "tok-secret-123", "--dry-run")
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if !strings.Contains(out, "nothing was sent") || strings.Contains(out, "tok-secret-123") {
			t.Errorf("%s: a dry run should say nothing was sent and mask the token:\n%s", verb, out)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("--dry-run sent %d requests", n)
	}
}
