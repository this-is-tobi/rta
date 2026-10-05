package http

import (
	"context"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

type seen struct {
	contentType, accept, userAgent, body string
	userAgentSent                        bool
}

func sentBy(t *testing.T, method string, values map[string]any, surface ...plugin.Surface) (seen, error) {
	t.Helper()
	var got seen
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		b, _ := io.ReadAll(r.Body)
		_, got.userAgentSent = r.Header["User-Agent"]
		got.contentType, got.accept, got.userAgent, got.body =
			r.Header.Get("Content-Type"), r.Header.Get("Accept"), r.Header.Get("User-Agent"), string(b)
	}))
	defer srv.Close()
	values["url"] = srv.URL
	r := req(values)
	if len(surface) > 0 {
		r = r.WithSurface(surface[0])
	}
	_, err := doRequest(context.Background(), method, r)
	return got, err
}

// A JSON body went out with no Content-Type and no Accept, so the API behind
// most REST endpoints rejected it or ignored it, and posting JSON — the main job
// of a REST client — took a --header of thirty-odd characters every time.
func TestAJSONBodyIsSentAsJSON(t *testing.T) {
	for _, body := range []string{`{"a":1}`, "  [1, 2]\n", `{}`} {
		got, err := sentBy(t, "POST", map[string]any{"data": body})
		if err != nil {
			t.Fatal(err)
		}
		if got.contentType != "application/json" {
			t.Errorf("%q: Content-Type %q, want application/json", body, got.contentType)
		}
		if got.body != body {
			t.Errorf("%q: the body was changed on the way: %q", body, got.body)
		}
	}
}

// Anything that is not a JSON object or array is more likely a form, a token
// exchange or a line of text than a JSON scalar, and is sent as it is with no
// type claimed for it.
func TestABodyThatIsNotAJSONDocumentGetsNoContentType(t *testing.T) {
	for _, body := range []string{"grant_type=client_credentials&client_id=x", "42", `"text"`, "true", `{"a":`, "hello"} {
		got, err := sentBy(t, "POST", map[string]any{"data": body})
		if err != nil {
			t.Fatal(err)
		}
		if got.contentType != "" {
			t.Errorf("%q: Content-Type %q, want none", body, got.contentType)
		}
	}
}

func TestEveryRequestAsksForJSONAndNamesItself(t *testing.T) {
	session.SetSelf("1.2.3")
	t.Cleanup(func() { session.SetSelf("") })
	got, err := sentBy(t, "GET", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if got.accept != "application/json, */*" || got.userAgent != "rta/1.2.3" {
		t.Errorf("Accept %q, User-Agent %q", got.accept, got.userAgent)
	}
	if got.contentType != "" {
		t.Errorf("a request with no body claimed a Content-Type: %q", got.contentType)
	}
}

func TestARunWithNoVersionStillNamesItself(t *testing.T) {
	session.SetSelf("")
	got, err := sentBy(t, "GET", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if got.userAgent != "rta" {
		t.Errorf("User-Agent %q, want rta", got.userAgent)
	}
}

// A header the caller wrote is theirs, whatever case they spelled its name in,
// and an empty one is how a default is taken away.
func TestAnExplicitHeaderWinsOverTheDefaults(t *testing.T) {
	got, err := sentBy(t, "POST", map[string]any{
		"data": `{"a":1}`,
		"header": []string{
			"content-type: application/vnd.api+json", "ACCEPT: text/csv", "User-Agent: curl/8",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.contentType != "application/vnd.api+json" || got.accept != "text/csv" || got.userAgent != "curl/8" {
		t.Errorf("Content-Type %q, Accept %q, User-Agent %q", got.contentType, got.accept, got.userAgent)
	}

	got, err = sentBy(t, "POST", map[string]any{"data": `{"a":1}`, "header": []string{"User-Agent:", "Accept:"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.userAgentSent || got.accept != "" {
		t.Errorf("an emptied header came back: User-Agent %q (sent %v), Accept %q", got.userAgent, got.userAgentSent, got.accept)
	}
}

// The dry run is how a request is checked before it leaves, so it shows what
// would be sent, defaults included.
func TestTheDefaultHeadersAreShownInADryRun(t *testing.T) {
	r := req(map[string]any{"url": "http://example.com/x", "data": `{"a":1}`})
	r.DryRun = true
	v, err := doRequest(context.Background(), "POST", r)
	if err != nil {
		t.Fatal(err)
	}
	pairs := pairsOf(t, v)
	if pairs["header:Content-Type"] != "application/json" || pairs["header:Accept"] != "application/json, */*" ||
		!strings.HasPrefix(pairs["header:User-Agent"], "rta") {
		t.Errorf("the dry run showed %v", pairs)
	}
}

func bodyFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A payload of any size is a file in practice, and `--data "$(cat f)"` is
// quoted by the shell and kept in its history. A path, or /dev/stdin for a
// pipe, is the way every other file input of this client already works.
func TestTheBodyCanComeFromAFile(t *testing.T) {
	const payload = "{\n  \"name\": \"x\"\n}\n"
	got, err := sentBy(t, "PUT", map[string]any{"data-file": bodyFile(t, payload)})
	if err != nil {
		t.Fatal(err)
	}
	if got.body != payload || got.contentType != "application/json" {
		t.Errorf("body %q, Content-Type %q", got.body, got.contentType)
	}
}

func TestABodyGivenTwiceOrUnreadableIsRefused(t *testing.T) {
	_, err := sentBy(t, "POST", map[string]any{"data": "a", "data-file": bodyFile(t, "b")})
	if verr := view.AsError(err, "x"); err == nil || verr.Code != "http.data.twice" {
		t.Errorf("a body as a value and as a file: %v, want http.data.twice", err)
	}
	_, err = sentBy(t, "POST", map[string]any{"data-file": filepath.Join(t.TempDir(), "absent")})
	if verr := view.AsError(err, "x"); err == nil || verr.Code != "http.data.file" {
		t.Errorf("a file that is not there: %v, want http.data.file", err)
	}
	big := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(big, make([]byte, maxRequestBody+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = sentBy(t, "POST", map[string]any{"data-file": big})
	if verr := view.AsError(err, "x"); err == nil || verr.Code != "http.data.file" {
		t.Errorf("a file past the cap: %v, want http.data.file", err)
	}
}

// `--data @payload.json` and `--data @-` are how curl takes a body from a file or
// a pipe, and sent as written they were the text "@payload.json": a server's
// complaint about its body and nothing about the command line. Where the name is
// a file here, or is the dash, it is refused with the flag that does it. Text
// that merely starts with @ is a body, and the agent's surface never looks at
// the disk at all.
func TestCurlsFileFormIsRefusedWithTheFlagThatSendsAFile(t *testing.T) {
	file := bodyFile(t, `{"a":1}`)
	for name, c := range map[string]struct {
		data string
		hint string
	}{
		"a file that is there": {"@" + file, "--data-file: " + file},
		"the dash for a pipe":  {"@-", "--data-file: /dev/stdin"},
	} {
		_, err := sentBy(t, "POST", map[string]any{"data": c.data})
		verr := view.AsError(err, "x")
		if err == nil || verr.Code != "http.data.atfile" || !strings.Contains(verr.Hint, c.hint) {
			t.Errorf("%s: %v, hint %q, want http.data.atfile naming %q", name, err, verr.Hint, c.hint)
		}
	}

	for name, data := range map[string]string{
		"a file that is not there": "@" + filepath.Join(t.TempDir(), "absent"),
		"a mention":                "@tobi ping",
		"a directory":              "@" + t.TempDir(),
		"a lone @":                 "@",
	} {
		got, err := sentBy(t, "POST", map[string]any{"data": data})
		if err != nil || got.body != data {
			t.Errorf("%s: body %q, %v, want it sent as written", name, got.body, err)
		}
	}

	got, err := sentBy(t, "POST", map[string]any{"data": "@" + file}, plugin.SurfaceMCP)
	if err != nil || got.body != "@"+file {
		t.Errorf("over MCP: body %q, %v, want the agent's text as written", got.body, err)
	}
}

// `-` is what curl and most of what a person has typed before read a pipe from,
// and "open -: no such file or directory" gave no way from there to /dev/stdin,
// which is how every Path input of rta takes one.
func TestADashForAPipeIsToldTheSpellingRtaTakes(t *testing.T) {
	_, err := sentBy(t, "POST", map[string]any{"data-file": "-"})
	verr := view.AsError(err, "x")
	if err == nil || verr.Code != "http.data.file" || !strings.Contains(verr.Hint, "/dev/stdin") {
		t.Errorf("--data-file -: %v, hint %q", err, verr.Hint)
	}
	_, err = sentBy(t, "POST", map[string]any{"bearer-file": "-"})
	verr = view.AsError(err, "x")
	if err == nil || verr.Code != "http.auth.file" || !strings.Contains(verr.Hint, "/dev/stdin") {
		t.Errorf("--bearer-file -: %v, hint %q", err, verr.Hint)
	}
	_, err = sentBy(t, "POST", map[string]any{"data-file": filepath.Join(t.TempDir(), "absent")})
	if verr := view.AsError(err, "x"); err == nil || verr.Hint != "" {
		t.Errorf("a file that is not there got the pipe hint: %v", err)
	}
}

// A path is a place on this machine, and an agent that could name one would
// send any file the server can read to a host of its own choosing: the input is
// Local, so no agent's schema offers it. Only the capabilities that take a
// body have it.
func TestTheBodyFileInputIsNeverOfferedToAnAgent(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		var field *plugin.Field
		for i, f := range c.Inputs {
			if f.Name == "data-file" {
				field = &c.Inputs[i]
			}
		}
		withBody := c.ID == "http.post" || c.ID == "http.put"
		switch {
		case withBody && (field == nil || !field.Local || field.Type != plugin.Path):
			t.Errorf("%s: data-file is %+v, want a Local path", c.ID, field)
		case !withBody && field != nil:
			t.Errorf("%s takes no body and has data-file", c.ID)
		}
	}
}
