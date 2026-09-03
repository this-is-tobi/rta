package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// fakeQdrant answers the REST paths these capabilities call, in Qdrant's own
// envelope, so a handler can be driven end to end without an instance. The
// recorded requests are the interesting half: what this plugin asks for is as
// much part of its behaviour as what it prints.
type fakeQdrant struct {
	*httptest.Server
	// bodies records each request's path and decoded body, in order.
	bodies []recorded
}

type recorded struct {
	path string
	body map[string]any
}

func newFakeQdrant(t *testing.T, routes map[string]string) *fakeQdrant {
	t.Helper()
	f := &fakeQdrant{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// EscapedPath, not Path: Path is already decoded, so a name escaped
		// into one segment and a name that broke out of it look identical
		// there. What went over the wire is the thing being asserted.
		rec := recorded{path: r.URL.EscapedPath()}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&rec.body)
		}
		f.bodies = append(f.bodies, rec)

		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":{"error":"Not found"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeQdrant) endpoint() string { return strings.TrimPrefix(f.URL, "http://") }

func (f *fakeQdrant) lastBody(t *testing.T) map[string]any {
	t.Helper()
	if len(f.bodies) == 0 {
		t.Fatal("no request was made")
	}
	return f.bodies[len(f.bodies)-1].body
}

func req(t *testing.T, capID string, values map[string]any) plugin.Request {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == capID {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
		}
	}
	t.Fatalf("no capability %q", capID)
	return plugin.Request{}
}

func reqAt(t *testing.T, f *fakeQdrant, capID string, values map[string]any) plugin.Request {
	t.Helper()
	values["endpoint"] = f.endpoint()
	return req(t, capID, values)
}

// Every shared connection input must be Local. These fields together name
// which instance a call reaches and as whom, and an MCP caller may not choose
// that — an agent that could would point rta at an instance of its own and
// have the host supply the operator's key beside it.
func TestEveryConnectionInputIsLocal(t *testing.T) {
	for _, f := range connFields() {
		if !f.Local {
			t.Errorf("%s: connection input is not Local — an MCP caller could redirect this call", f.Name)
		}
	}
}

func TestOnlySecretsUseEnvFallback(t *testing.T) {
	for _, f := range connFields() {
		if f.EnvFallback && f.Type != plugin.Secret {
			t.Errorf("%s: non-secret input declares EnvFallback (%s); a destination must come from a caller or config",
				f.Name, f.Type)
		}
	}
}

func TestEveryCapabilityIsNoPreview(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if !c.NoPreview {
			t.Errorf("%s: NoPreview = false, want true — every capability here reaches off the box", c.ID)
		}
	}
}

// **The line this plugin draws, pinned.** Only the capability that returns
// stored points is a write, and it needs a grant naming it. Everything else
// describes collections and returns none of their contents.
func TestOnlyThePointReadIsAWrite(t *testing.T) {
	want := map[string]struct {
		safety plugin.Safety
		grant  bool
	}{
		"qdrant.overview":        {plugin.Read, false},
		"qdrant.collection.list": {plugin.Read, false},
		"qdrant.collection.show": {plugin.Read, false},
		"qdrant.points.count":    {plugin.Read, false},
		"qdrant.points.scroll":   {plugin.Write, true},
		// The pair with no nameable blast radius: both refuse MCP outright,
		// so neither carries NeedsGrant — a grant that can never be exercised
		// over the surface grants gate would be a standing entry in `grant
		// list` that means nothing (keys.backup's line, via pg.dump).
		"qdrant.dump":    {plugin.Write, false},
		"qdrant.restore": {plugin.Destructive, false},
	}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		seen[c.ID] = true
		expect, ok := want[c.ID]
		if !ok {
			t.Errorf("%s: not accounted for in this test's table", c.ID)
			continue
		}
		if c.Safety != expect.safety {
			t.Errorf("%s: Safety = %s, want %s", c.ID, c.Safety, expect.safety)
		}
		if c.NeedsGrant != expect.grant {
			t.Errorf("%s: NeedsGrant = %v, want %v", c.ID, c.NeedsGrant, expect.grant)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s: declared in this test's table but not in Plugin()", id)
		}
	}
}

func TestEveryCapabilityIsRunnableAndDescribed(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.Run == nil {
			t.Errorf("%s: no Run", c.ID)
		}
		if strings.TrimSpace(c.Description) == "" {
			t.Errorf("%s: no Description — `rta explain` has nothing to print", c.ID)
		}
		if !strings.HasPrefix(c.ID, Plugin().Name+".") {
			t.Errorf("%s: capability IDs must be namespaced by %q", c.ID, Plugin().Name)
		}
	}
}

// **Vectors are a second decision, and the default is no.**
//
// An embedding is not a hash: inversion attacks recover substantial parts of
// the source text from embeddings alone. So --vectors must default to off even
// inside the write tier, and the request this plugin sends has to reflect that
// — asking for them and then not printing them would still have read them.
func TestVectorsAreNotRequestedUnlessAskedFor(t *testing.T) {
	f := newFakeQdrant(t, map[string]string{
		"/collections/docs/points/scroll": `{"result":{"points":[
			{"id":1,"payload":{"text":"hello"},"vector":[0.1,0.2,0.3]}
		],"next_page_offset":null}}`,
	})

	if _, err := runPointsScroll(context.Background(),
		reqAt(t, f, "qdrant.points.scroll", map[string]any{"collection": "docs"})); err != nil {
		t.Fatal(err)
	}
	if got := f.lastBody(t)["with_vector"]; got != false {
		t.Errorf("with_vector = %v by default — an embedding is close to the source text, "+
			"so this must be asked for", got)
	}

	if _, err := runPointsScroll(context.Background(),
		reqAt(t, f, "qdrant.points.scroll", map[string]any{"collection": "docs", "vectors": true})); err != nil {
		t.Fatal(err)
	}
	if got := f.lastBody(t)["with_vector"]; got != true {
		t.Errorf("with_vector = %v when --vectors was passed", got)
	}
}

// Payload columns carry whatever was indexed, so every one is redacted and the
// id is not: which points were read is what the record is for, and what they
// contained is not something to leave in a scrollback.
func TestPayloadColumnsAreRedactedAndTheIDIsNot(t *testing.T) {
	f := newFakeQdrant(t, map[string]string{
		"/collections/docs/points/scroll": `{"result":{"points":[
			{"id":1,"payload":{"text":"a support ticket","customer":"acme"},"vector":[0.1]}
		],"next_page_offset":null}}`,
	})
	v, err := runPointsScroll(context.Background(),
		reqAt(t, f, "qdrant.points.scroll", map[string]any{"collection": "docs", "vectors": true}))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)

	for _, col := range []string{"text", "customer", "Vector"} {
		if !slices.Contains(tbl.Redacted, col) {
			t.Errorf("column %q is not redacted: %v", col, tbl.Redacted)
		}
	}
	if slices.Contains(tbl.Redacted, "ID") {
		t.Error("the id is redacted — that hides which points were read from the record")
	}
}

// A collection name is caller-supplied and Qdrant accepts a wide range of
// them, so a raw concatenation is a path traversal: a name of "../cluster"
// would reach a different endpoint entirely.
func TestCollectionNamesCannotEscapeTheirPath(t *testing.T) {
	f := newFakeQdrant(t, map[string]string{})
	_, _ = runCollectionShow(context.Background(),
		reqAt(t, f, "qdrant.collection.show", map[string]any{"collection": "../cluster"}))

	if len(f.bodies) == 0 {
		t.Fatal("no request was made")
	}
	got := f.bodies[0].path
	if strings.Contains(got, "/cluster") && !strings.Contains(got, "collections") {
		t.Fatalf("a collection name escaped its path: %q", got)
	}
	if got != "/collections/..%2Fcluster" {
		t.Errorf("path = %q, want the name escaped into one segment", got)
	}
}

// Qdrant reports vector configuration two ways, and both are ordinary. A
// struct fitting one silently reports nothing for the other.
func TestBothVectorConfigShapesAreRead(t *testing.T) {
	single := describeVectors(map[string]any{"size": float64(1536), "distance": "Cosine"})
	if len(single) != 1 || !strings.Contains(single[0].Value, "1536 dimensions") {
		t.Errorf("unnamed vector config not read: %+v", single)
	}
	if !strings.Contains(single[0].Value, "Cosine") {
		t.Errorf("distance metric not read: %+v", single)
	}

	named := describeVectors(map[string]any{
		"text":  map[string]any{"size": float64(768), "distance": "Dot"},
		"image": map[string]any{"size": float64(512), "distance": "Euclid"},
	})
	if len(named) != 2 {
		t.Fatalf("named vector configs not read: %+v", named)
	}
	// Sorted, because a map's order would reshuffle the rows between calls.
	if named[0].Key != "vector image" || named[1].Key != "vector text" {
		t.Errorf("named vectors are not in a stable order: %+v", named)
	}
}

// Qdrant omits these fields entirely on a collection it has not finished
// loading. Rendering that as 0 would say the collection is empty when it is
// merely not ready — two states with very different next steps.
func TestAnUnreportedCountIsNotZero(t *testing.T) {
	if got := countText(nil); got != "not reported" {
		t.Errorf("countText(nil) = %q — an unloaded collection would read as empty", got)
	}
	zero := int64(0)
	if got := countText(&zero); got != "0" {
		t.Errorf("countText(0) = %q, want 0", got)
	}
}

// JSON has one number type, so an integer id arrives as a float. Printing
// 4.2e+01 gives an id column nobody can match against anything.
func TestIntegerPayloadValuesDoNotRenderAsFloats(t *testing.T) {
	if got := payloadCell(float64(42)); got != "42" {
		t.Errorf("payloadCell(42) = %q, want 42", got)
	}
	if got := payloadCell(float64(1.5)); got != "1.5" {
		t.Errorf("payloadCell(1.5) = %q, want 1.5", got)
	}
	// Nested values are compact JSON rather than Go's map[a:1], which nothing
	// can parse and nobody writes.
	if got := payloadCell(map[string]any{"a": float64(1)}); got != `{"a":1}` {
		t.Errorf("payloadCell(map) = %q, want compact JSON", got)
	}
}

// A vector rendered in full fills a terminal with numbers nobody can read. The
// summary keeps the dimensionality, which is the part somebody is checking.
func TestVectorsRenderAsASummaryRatherThanThousandsOfFloats(t *testing.T) {
	raw := json.RawMessage(`[0.1,0.2,0.3,0.4,0.5]`)
	got := vectorSummary(raw)
	if !strings.HasPrefix(got, "5d") {
		t.Errorf("vectorSummary = %q, want the dimensionality first", got)
	}
	if strings.Count(got, ",") > 2 {
		t.Errorf("vectorSummary = %q — the whole vector was printed", got)
	}

	named := json.RawMessage(`{"text":[0.1,0.2],"image":[0.3]}`)
	if got := vectorSummary(named); !strings.Contains(got, "image:1d") || !strings.Contains(got, "text:2d") {
		t.Errorf("named vectors not summarised: %q", got)
	}
}

// 6334 is the gRPC port, one character from 6333, and it accepts the
// connection before failing to answer. Nothing about the resulting error says
// so, which is why the hint has to.
func TestTheGRPCPortMixupIsNamedInTheHint(t *testing.T) {
	r := req(t, "qdrant.overview", map[string]any{"endpoint": "qdrant.internal:6334"})
	got := malformed(r)
	if !strings.Contains(got.Hint, "6334") {
		t.Errorf("the hint does not mention the gRPC port: %q", got.Hint)
	}
}

// Each HTTP failure must produce a distinct code and a hint naming the next
// step. A 401 against an instance with no key configured is the one people
// actually hit, and it fails the same way as a wrong key.
func TestHTTPFailuresAreClassified(t *testing.T) {
	r := req(t, "qdrant.overview", map[string]any{"endpoint": "qdrant.internal:6333"})
	cases := []struct {
		code int
		want string
	}{
		{http.StatusUnauthorized, "qdrant.auth.failed"},
		{http.StatusForbidden, "qdrant.auth.failed"},
		{http.StatusNotFound, "qdrant.notfound"},
		{http.StatusTooManyRequests, "qdrant.ratelimited"},
		{http.StatusServiceUnavailable, "qdrant.unavailable"},
		{http.StatusInternalServerError, "qdrant.request.failed"},
	}
	for _, c := range cases {
		got := classifyStatus(c.code, []byte(`{"status":{"error":"detail here"}}`), r)
		if got.Code != c.want {
			t.Errorf("%d classified as %q, want %q", c.code, got.Code, c.want)
		}
		if strings.TrimSpace(got.Hint) == "" {
			t.Errorf("%d has no hint — the code alone does not say what to do next", c.code)
		}
	}
}

// Qdrant puts the reason in the body and it is usually the useful half, but
// some of these run to kilobytes.
func TestErrorDetailIsExtractedAndBounded(t *testing.T) {
	got := qdrantErrorText([]byte(`{"status":{"error":"Collection ` + "`x`" + ` does not exist"}}`))
	if !strings.Contains(got, "does not exist") {
		t.Errorf("the reason was dropped: %q", got)
	}
	long := qdrantErrorText([]byte(strings.Repeat("x", 5000)))
	if len(long) > 320 {
		t.Errorf("an unbounded body reached the error: %d chars", len(long))
	}
	if got := qdrantErrorText(nil); got != "no detail given" {
		t.Errorf("an empty body produced %q", got)
	}
}

// A response body is attacker-controlled in the sense that matters here:
// whatever is at that endpoint decides how much to send. An unbounded ReadAll
// is how a wrong endpoint — or a hostile one — becomes an out-of-memory rather
// than an error message.
func TestAResponseIsNeverReadWithoutABound(t *testing.T) {
	original := maxResponseBytes
	maxResponseBytes = 512
	t.Cleanup(func() { maxResponseBytes = original })

	// Far more than the lowered bound, and valid JSON up to the cut so that a
	// failure here is the bound working rather than the parser giving up.
	huge := `{"result":{"collections":[` +
		strings.Repeat(`{"name":"padding-padding-padding"},`, 200) +
		`{"name":"last"}]}}`
	f := newFakeQdrant(t, map[string]string{"/collections": huge})

	var out collectionsResponse
	verr := get(context.Background(), reqAt(t, f, "qdrant.collection.list", map[string]any{}), "/collections", &out)
	if verr == nil {
		t.Fatalf("a %d-byte body was read whole under a %d-byte bound", len(huge), maxResponseBytes)
	}
	// It fails as malformed, which is correct: a truncated body is not JSON.
	// The point is that it fails rather than allocating whatever it was sent.
	if verr.Code != "qdrant.response.malformed" {
		t.Errorf("code = %q, want the truncated body to be reported as malformed", verr.Code)
	}
}
