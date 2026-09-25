package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdnet "net"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// TestMain relaxes isBlockedIP for this package's whole test binary.
//
// Every test in this package (this file and its siblings) reaches its
// target through an httptest.Server, which binds to loopback — exactly
// what dialGuarded refuses by default. These tests are about the plugin's
// request handling, not its address policy, and need a loopback server to
// reach at all. The guard's own default behavior is what ssrf_test.go
// tests, restoring isBlockedIP deliberately around each of those cases.
func TestMain(m *testing.M) {
	isBlockedIP = func(stdnet.IP) bool { return false }
	os.Exit(m.Run())
}

func req(values map[string]any) plugin.Request {
	if values["timeout"] == nil {
		values["timeout"] = 5
	}
	return plugin.NewRequest(values, false, false)
}

func pairsOf(t *testing.T, v view.View) map[string]string {
	t.Helper()
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("want KeyValue, got %s", view.TypeOf(v))
	}
	m := map[string]string{}
	for _, p := range kv.Pairs {
		m[p.Key] = p.Value
	}
	return m
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSafetyClasses(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		switch c.ID {
		case "http.get", "http.head", "http.status":
			if c.Safety != plugin.Read {
				t.Errorf("%s should be read", c.ID)
			}
		default:
			if c.Safety != plugin.Write {
				t.Errorf("%s should be write", c.ID)
			}
		}
	}
}

func TestGetJSON(t *testing.T) {
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"hello": "world"})
	}))
	defer srv.Close()

	v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	pairs := pairsOf(t, v)
	if !strings.HasPrefix(pairs["status"], "200") {
		t.Errorf("status = %q", pairs["status"])
	}
	if !strings.Contains(pairs["body"], "\"hello\": \"world\"") {
		t.Errorf("json not pretty-printed:\n%s", pairs["body"])
	}
	if pairs["time"] == "" {
		t.Error("missing timing")
	}
}

// A grant covers exactly the URL named (Scope: "url"), checked once by the
// MCP bridge before Run ever executes. Following a redirect automatically
// would let a call authorized for one destination actually reach whatever a
// 3xx response pointed at instead — no second grant check involved, and
// with no way for the operator who issued the grant to have known. This
// pins the fix: the redirect target's handler must never run, and its
// Location must be visible instead of silently chased.
func TestRedirectsAreNeverFollowedAutomatically(t *testing.T) {
	var forbiddenWasHit bool
	forbidden := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		forbiddenWasHit = true
		w.Write([]byte("secret metadata"))
	}))
	defer forbidden.Close()

	redirector := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		stdhttp.Redirect(w, r, forbidden.URL, stdhttp.StatusFound)
	}))
	defer redirector.Close()

	v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": redirector.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if forbiddenWasHit {
		t.Fatal("the redirect target was actually requested — a grant scoped to the redirector's URL was defeated")
	}
	pairs := pairsOf(t, v)
	if !strings.HasPrefix(pairs["status"], "302") {
		t.Errorf("status = %q, want the redirect response itself (302), not whatever it points at", pairs["status"])
	}
	if pairs["location (not followed)"] != forbidden.URL {
		t.Errorf("location (not followed) = %q, want %q", pairs["location (not followed)"], forbidden.URL)
	}
}

func TestAuthAndHeaders(t *testing.T) {
	var gotAuth, gotCustom string
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCustom = r.Header.Get("X-Custom")
	}))
	defer srv.Close()

	_, err := doRequest(context.Background(), "GET", req(map[string]any{
		"url":    srv.URL,
		"bearer": "tok123",
		"header": []string{"X-Custom: yes"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotCustom != "yes" {
		t.Errorf("custom header = %q", gotCustom)
	}
}

func TestBasicAuth(t *testing.T) {
	var user, pass string
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		user, pass, _ = r.BasicAuth()
	}))
	defer srv.Close()
	_, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL, "basic": "alice:s3cret"}))
	if err != nil {
		t.Fatal(err)
	}
	if user != "alice" || pass != "s3cret" {
		t.Errorf("basic auth = %q:%q", user, pass)
	}
}

func TestPostBody(t *testing.T) {
	var received string
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		received = string(b)
	}))
	defer srv.Close()
	_, err := doRequest(context.Background(), "POST", req(map[string]any{"url": srv.URL, "data": `{"a":1}`}))
	if err != nil {
		t.Fatal(err)
	}
	if received != `{"a":1}` {
		t.Errorf("body = %q", received)
	}
}

func TestBadHeaderIsCoded(t *testing.T) {
	_, err := doRequest(context.Background(), "GET", req(map[string]any{
		"url": "http://127.0.0.1:1", "header": []string{"no-colon"},
	}))
	ve := view.AsError(err, "x")
	if ve.Code != "http.header.invalid" {
		t.Errorf("want http.header.invalid, got %+v", ve)
	}
}

func TestConnectionFailureIsCoded(t *testing.T) {
	_, err := doRequest(context.Background(), "GET", req(map[string]any{"url": "http://127.0.0.1:1"}))
	ve := view.AsError(err, "x")
	if ve.Code != "http.request.failed" || ve.Hint == "" {
		t.Errorf("want coded failure with hint, got %+v", ve)
	}
}

func TestBodyTruncation(t *testing.T) {
	big := strings.Repeat("x", 10000)
	out, cut := formatBody([]byte(big), "text/plain", false)
	if len(out) != maxShown || !cut || strings.Trim(out, "x") != "" {
		t.Errorf("large body = %d bytes, cut %v: want the first %d bytes and nothing else", len(out), cut, maxShown)
	}
	// A cut at a byte offset could land inside a character; it must not.
	accented := strings.Repeat("é", 3000) // two bytes each
	if out, _ := formatBody([]byte(accented), "text/plain", false); !utf8.ValidString(out) {
		t.Error("truncation split a character")
	}
}

// A JSON body is re-indented and nothing else: every number, every key in the
// order the server sent it, every character as it was escaped. Round-tripping
// it through map[string]any changed the id, sorted the keys and escaped the
// ampersand.
func TestAJSONBodyIsShownAsTheServerSentIt(t *testing.T) {
	got, _ := formatBody([]byte(`{"zeta":1,"id":9007199254740993,"big":12345678901234567890,"q":"a&b<c>"}`+"\n"), "application/json", false)
	want := "{\n  \"zeta\": 1,\n  \"id\": 9007199254740993,\n  \"big\": 12345678901234567890,\n  \"q\": \"a&b<c>\"\n}"
	if got != want {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}
}

// Indenting is bounded by what it would write, not by what was read. Nesting
// 10000 deep is legal JSON, and every line inside it is indented 20000
// spaces, so 22 KB of brackets and zeros asked json.Indent for 220 MB, and a
// 1 MiB body for about 10 GB — an out-of-memory kill for `rta http get`, and
// for `rta mcp serve` with every other tool an agent had open. A body like
// that is shown as it was sent, the way a body that is not JSON is.
func TestDeepNestingIsNotIndentedWithoutBound(t *testing.T) {
	const depth = 10000
	body := strings.Repeat("[", depth) + strings.Repeat("0,", 1000) + "0" + strings.Repeat("]", depth)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got, _ := formatBody([]byte(body), "application/json", false)
	runtime.ReadMemStats(&after)
	if len(got) > 2*len(body) {
		t.Errorf("a %d-byte body became a %d-byte value", len(body), len(got))
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 64<<20 {
		t.Errorf("formatting a %d-byte body allocated %d bytes", len(body), alloc)
	}
	if !strings.HasPrefix(got, "[[[[") {
		t.Errorf("body = %.40q, want it shown as it was sent", got)
	}

	// Nesting an API actually sends is still laid out.
	nested := `{"a":{"b":{"c":[1,{"d":[]}]}}}`
	want := "{\n  \"a\": {\n    \"b\": {\n      \"c\": [\n        1,\n        {\n          \"d\": []\n        }\n      ]\n    }\n  }\n}"
	if got, _ := formatBody([]byte(nested), "application/json", false); got != want {
		t.Errorf("body =\n%s\nwant\n%s", got, want)
	}
}

// The measure is json.Indent's own count, so the bound is on what would be
// written and not on an estimate of it: for each body it says yes at exactly
// the size Indent writes and no a byte below.
func TestIndentFitsMeasuresWhatIndentWrites(t *testing.T) {
	for _, body := range []string{
		`{}`, `[]`, `0`, `"s"`, `[1,2,3]`, `{"a":{"b":{"c":[1,{"d":[]}]}}}`,
		`{"s":"a\"b,[{:","t":"\\"}`, ` { "x" : [ 1 , { } , [ ] ] , "y":null}`,
		// Whitespace after the value is copied, not dropped: the newline a
		// server ends a body with, and whatever else it sent there.
		"{\"a\":[1]}\n", " [ 1 , { \"x\" : null } ] ", "{} \r\n", "7\t",
		strings.Repeat("[", 50) + strings.Repeat("]", 50),
		strings.Repeat(`{"k":[`, 20) + "1" + strings.Repeat("]}", 20),
	} {
		var out bytes.Buffer
		if err := json.Indent(&out, []byte(body), "", "  "); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if !indentFits([]byte(body), out.Len()) || indentFits([]byte(body), out.Len()-1) {
			t.Errorf("%s: json.Indent writes %d bytes, and indentFits disagrees", body, out.Len())
		}
	}
}

// Text a renderer shows is text here: a right-to-left page with its marks, an
// RFC with its page breaks, a JSON body behind a byte order mark, and a body
// of Japanese cut by the 1 MiB cap partway through a character. Each was
// dumped as 256 bytes of hex. Characters are planted by code point: a source
// file may not hold an invisible one (internal/textclean's source guard).
func TestTextIsShownAsText(t *testing.T) {
	rlm := string(rune(0x200f))
	cjk := strings.Repeat("日本語", maxBody/9+1000)
	for name, tc := range map[string]struct {
		contentType, body, want string
	}{
		"rtl page":      {"text/html", "<p>שלום" + rlm + " (1)</p>", "שלום" + rlm},
		"form feed":     {"text/plain", "page one\fpage two", "page two"},
		"bom json":      {"application/json", "\xef\xbb\xbf" + `{"ok":true}`, "{\n  \"ok\": true\n}"},
		"cut mid-rune":  {"text/plain", cjk, "日本語日本語"},
		"cut json text": {"application/json", `{"a":"` + cjk + `"}`, `{"a":"日本語`},
	} {
		srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
			w.Header().Set("Content-Type", tc.contentType)
			w.Write([]byte(tc.body))
		}))
		v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}))
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		body := pairsOf(t, v)["body"]
		if strings.Contains(body, "not plain text") || !strings.Contains(body, tc.want) {
			t.Errorf("%s: body = %.200q, want text holding %q", name, body, tc.want)
		}
	}
}

// A body that is not text — an image, a gzip the server did not label — is
// dumped rather than printed, which showed a few stray letters.
func TestABinaryBodyIsDumped(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0x0d}
	got, _ := formatBody(png, "image/png", false)
	if !strings.HasPrefix(got, "12 bytes, not plain text:\n00000000  89 50 4e 47") {
		t.Errorf("body = %q", got)
	}
}

// A body labelled JSON that is not UTF-8 is not JSON, and is dumped like any
// other body that is not text. json.Indent does not check the encoding, so
// raw 8-bit CSI and OSC in a string went into the value as they came.
func TestAJSONBodyThatIsNotUTF8IsDumped(t *testing.T) {
	got, _ := formatBody([]byte("{\"a\":\"\x9b2J\x9d0;pwned\x9c\"}"), "application/json", false)
	for _, raw := range []byte{0x9b, 0x9c, 0x9d} {
		if strings.IndexByte(got, raw) >= 0 {
			t.Errorf("body = %q, carries the raw byte %#x", got, raw)
		}
	}
	if !strings.Contains(got, "not plain text") || !strings.Contains(got, "22 9b 32") {
		t.Errorf("body = %q, want the bytes dumped", got)
	}
}

// The 1 MiB cap on doRequest's own body read (maxBody) is separate from,
// and happens before, formatBody's display truncation above — what it cuts
// never reaches formatBody at all. Silently reporting the captured length
// as "size" reads as the whole response; this pins that the view says
// otherwise once the cap is actually hit.
func TestOversizedResponseIsMarkedTruncated(t *testing.T) {
	big := strings.Repeat("x", maxBody+1024)
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.Write([]byte(big))
	}))
	defer srv.Close()

	v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	pairs := pairsOf(t, v)
	if !strings.Contains(pairs["size"], "truncated") {
		t.Errorf("size = %q, want it to say the response was truncated", pairs["size"])
	}
}

// The page states one size, the response's own, and how much of it the body
// shows. It said "1048576 B (truncated, showing first 1 MiB)", then showed
// 4 KiB, then counted "1044480 more bytes" against the megabyte read: three
// sizes, none of them the 3 MiB the server declared. Without a length there
// is no size to state, only that it is more than was read. And a body of
// exactly 1 MiB is whole, which was called truncated for filling the read.
func TestTheSizeIsTheResponsesAndTheBodySaysHowMuchOfItIsShown(t *testing.T) {
	const threeMiB = 3 << 20
	for name, tc := range map[string]struct {
		length          int
		declare         bool
		size, bodyShown string
	}{
		"declared": {threeMiB, true, "3145728 B (truncated to the first 1 MiB)", "the first 4096 B of 3145728 B"},
		"chunked": {threeMiB, false, "more than 1048576 B (truncated to the first 1 MiB)",
			"the first 4096 B of more than 1048576 B"},
		"exactly the cap": {maxBody, true, "1048576 B", "the first 4096 B of 1048576 B"},
		"small":           {10000, true, "10000 B", "the first 4096 B of 10000 B"},
		"shown whole":     {4096, true, "4096 B", ""},
	} {
		srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
			w.Header().Set("Content-Type", "text/plain")
			if tc.declare {
				w.Header().Set("Content-Length", strconv.Itoa(tc.length))
			}
			_, _ = w.Write([]byte(strings.Repeat("A", tc.length)))
		}))
		v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}))
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		pairs := pairsOf(t, v)
		if pairs["size"] != tc.size {
			t.Errorf("%s: size = %q, want %q", name, pairs["size"], tc.size)
		}
		if pairs["body shown"] != tc.bodyShown {
			t.Errorf("%s: body shown = %q, want %q", name, pairs["body shown"], tc.bodyShown)
		}
		if body := pairs["body"]; strings.Trim(body, "A") != "" || len(body) != min(tc.length, 4096) {
			t.Errorf("%s: body is %d bytes holding %q, want the response's first bytes and nothing else",
				name, len(body), strings.Trim(body, "A"))
		}
	}
}

// rta's own words about a body are never part of the body value. The note
// that it was cut was appended to the text it described, and every renderer
// cleans a value with ansi.Strip, which reads an OSC or DCS left open as
// running to the end of the string — so a body holding ESC ] before the cut
// took the note with it, and read as complete, in pretty output, the TUI and
// to a model alike.
func TestATruncatedBodySaysSoWhateverTheBodyHolds(t *testing.T) {
	for name, intro := range map[string]string{
		"osc": "\x1b]0;", "dcs": "\x1bP", "apc": "\x1b_", "csi": "\x1b[",
	} {
		body := strings.Repeat("a", 4000) + intro + strings.Repeat("b", 5000)
		srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(body))
		}))
		v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}))
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var out bytes.Buffer
		if err := cli.Render(&out, v, cli.Options{Format: cli.Pretty, NoColor: true}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := fmt.Sprintf("the first 4096 B of %d B", len(body)); !strings.Contains(out.String(), want) {
			t.Errorf("%s: pretty output does not say %q", name, want)
		}
	}
}

// A body cut at the cap is shown as the start it is, even when that start
// parses as JSON on its own. Three megabytes of one number, cut to one, were
// laid out as a whole document: a megabyte of digits, no word beside it of
// how much was left out.
func TestABodyCutAtTheCapIsNotLaidOutAsJSON(t *testing.T) {
	body := strings.Repeat("7", 3<<20)
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	pairs := pairsOf(t, v)
	if want := fmt.Sprintf("the first %d B of %d B", maxShown, len(body)); pairs["body shown"] != want {
		t.Errorf("body shown = %q, want %q", pairs["body shown"], want)
	}
	if len(pairs["body"]) != maxShown {
		t.Errorf("body is %d bytes, want the first %d", len(pairs["body"]), maxShown)
	}
}

func TestNormalResponseIsNotMarkedTruncated(t *testing.T) {
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	pairs := pairsOf(t, v)
	if strings.Contains(pairs["size"], "truncated") {
		t.Errorf("size = %q, an ordinary response should not be marked truncated", pairs["size"])
	}
	if pairs["size"] != "5 B" {
		t.Errorf("size = %q, want %q", pairs["size"], "5 B")
	}
}

// A --dry-run that sends the request anyway is worse than none at all: it
// reports what "would" happen after it has already happened.
func TestDryRunSendsNothing(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		hits++
		w.WriteHeader(stdhttp.StatusOK)
	}))
	defer srv.Close()

	for _, method := range []string{"post", "put", "delete"} {
		v, err := doRequest(context.Background(), strings.ToUpper(method),
			plugin.NewRequest(map[string]any{"url": srv.URL, "data": "payload", "timeout": 5}, true, true))
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		kv, ok := v.(view.KeyValue)
		if !ok {
			t.Fatalf("%s dry run = %s", method, view.TypeOf(v))
		}
		if kv.Pairs[0].Key != "dry run" {
			t.Errorf("%s dry run does not lead with what it did: %+v", method, kv.Pairs[0])
		}
	}
	if hits != 0 {
		t.Fatalf("dry runs reached the server %d times", hits)
	}

	// …and a real run still does.
	if _, err := doRequest(context.Background(), "POST",
		plugin.NewRequest(map[string]any{"url": srv.URL, "timeout": 5}, false, true)); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("a real request reached the server %d times", hits)
	}
}

// A dry run prints the request for inspection, which must not turn it into a
// way to print the token you were about to send.
func TestDryRunMasksCredentials(t *testing.T) {
	v, err := doRequest(context.Background(), "POST",
		plugin.NewRequest(map[string]any{"url": "https://example.com", "bearer": "s3cr3t", "timeout": 5}, true, true))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(view.Envelope{View: view.Redact(v)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "s3cr3t") {
		t.Fatalf("the dry run echoed the credential: %s", raw)
	}
}

// The status reference is the one capability here that opens no socket: a
// card for a code, a table for a class or a range, and a coded refusal for
// anything else — with the text the standard uses, not a paraphrase.
func TestStatusReferenceIsOfflineAndComplete(t *testing.T) {
	v, err := runStatus(context.Background(), plugin.NewRequest(map[string]any{"code": "404"}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	card := v.(view.KeyValue)
	got := map[string]string{}
	for _, p := range card.Pairs {
		got[p.Key] = p.Value
	}
	if got["text"] != "Not Found" || !strings.Contains(got["class"], "4xx") || !strings.Contains(got["defined in"], "RFC 9110") {
		t.Errorf("404 card = %v", got)
	}

	v, err = runStatus(context.Background(), plugin.NewRequest(map[string]any{"code": "5xx"}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) == 0 || tbl.Rows[0][0] != "500" {
		t.Errorf("5xx table = %v", tbl.Rows)
	}
	for _, r := range tbl.Rows {
		if !strings.HasPrefix(r[0], "5") {
			t.Errorf("5xx table carries %v", r)
		}
	}

	v, err = runStatus(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	if all := v.(view.Table); all.Total < 60 {
		t.Errorf("every code: %d rows, want the whole of net/http's list", all.Total)
	}

	_, err = runStatus(context.Background(), plugin.NewRequest(map[string]any{"code": "teapot"}, false, false))
	if ve := view.AsError(err, "x"); ve.Code != "http.status.badcode" || ve.Hint == "" {
		t.Errorf("bad input = %+v", ve)
	}
	_, err = runStatus(context.Background(), plugin.NewRequest(map[string]any{"code": "299"}, false, false))
	if ve := view.AsError(err, "x"); ve.Code != "http.status.unknown" {
		t.Errorf("unknown code = %+v", ve)
	}
}
