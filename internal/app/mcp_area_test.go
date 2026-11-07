package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// areaRegistry has four areas a client can be shown and one it never can: kube,
// note and vault each list tools, sys describes the machine, and lock holds only
// what the person at the terminal may use.
func areaRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	reg := registry.New()
	for _, p := range []plugin.Plugin{
		{Name: "kube", Summary: "Kubernetes", Capabilities: []plugin.Capability{
			{ID: "kube.pod.list", Summary: "list pods", Safety: plugin.Read, Run: run},
			{ID: "kube.node.list", Summary: "list nodes", Safety: plugin.Read, Run: run},
		}},
		{Name: "note", Summary: "notes", Capabilities: []plugin.Capability{
			{ID: "note.list", Summary: "list notes", Safety: plugin.Read, Run: run},
			{ID: "note.add", Summary: "add a note", Safety: plugin.Write, Run: run},
		}},
		{Name: "vault", Summary: "HashiCorp Vault", Capabilities: []plugin.Capability{
			{ID: "vault.kv.get", Summary: "read a secret", Safety: plugin.Write, NeedsGrant: true, Run: run},
		}},
		{Name: "sys", Summary: "host health", Capabilities: []plugin.Capability{
			{ID: "sys.overview", Summary: "host health", Safety: plugin.Read, HostSpecific: true, Run: run},
		}},
		{Name: "lock", Summary: "freeze an agent", Capabilities: []plugin.Capability{
			{ID: "lock.add", Summary: "freeze one", Safety: plugin.Write, HumanOnly: true, Run: run},
		}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

func registeredLine(t *testing.T, calls func() []string) string {
	t.Helper()
	got := calls()
	if len(got) != 1 {
		t.Fatalf("the client was run %d times: %v", len(got), got)
	}
	return got[0]
}

// The areas are registered where the client reads them, sorted and once each,
// so that the same areas in another order, or named twice, are the same
// registration and not a change to be replaced.
func TestMCPInstallRegistersTheAreasSortedAndOnce(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	out, errOut, err := run(t, areaRegistry(t), "mcp", "install", "claude", "--area", "vault", "--area", "Kube,vault", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got, want := registeredLine(t, calls), "mcp serve --as claude --consent --area kube --area vault"; !strings.HasSuffix(got, want) {
		t.Errorf("claude was run as %q, want a line ending %q", got, want)
	}
	pairs := answerPairs(t, out)
	if !strings.HasPrefix(pairs["areas"], "kube, vault") {
		t.Errorf("the receipt says the areas are %q, want kube, vault", pairs["areas"])
	}
	if !strings.Contains(pairs["next"], "ask it which rta tools it has") || strings.Contains(pairs["next"], "sys_overview") {
		t.Errorf("next = %q: a registration without sys cannot be tried with sys_overview", pairs["next"])
	}
}

// Nothing is registered that was not asked for: with no area the line is what it
// was, and the first call to try is the one it always was.
func TestMCPInstallWithoutAnAreaRegistersNoAreaLine(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	out, _, err := run(t, areaRegistry(t), "mcp", "install", "claude", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if got := registeredLine(t, calls); strings.Contains(got, "--area") {
		t.Errorf("claude was run as %q, want no --area", got)
	}
	pairs := answerPairs(t, out)
	if _, has := pairs["areas"]; has || !strings.Contains(pairs["next"], "sys_overview") {
		t.Errorf("a registration with no area answered %v", pairs)
	}
}

// Typed once and read months later by a server nobody is watching, so an area
// that is a typo, or has no tool to list, is refused here with the areas there
// are, and nothing is registered.
func TestMCPInstallRefusesAnAreaThatListsNothing(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	for name, tc := range map[string]struct{ args, hint string }{
		"a typo":                 {"kub", "did you mean kube?"},
		"nothing like an area":   {"zzzzzz", "the areas are kube, note, sys, vault"},
		"an area of a person's":  {"lock", "the areas are kube, note, sys, vault"},
		"a tool's name":          {"kube_pod_list", "the areas are kube, note, sys, vault"},
		"a blank among several":  {"kube,", "the areas are kube, note, sys, vault"},
		"one good and one wrong": {"kube,vualt", "did you mean vault?"},
	} {
		_, _, err := run(t, areaRegistry(t), "mcp", "install", "claude", "--area", tc.args)
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != "core.mcp.area" || !strings.Contains(ve.Hint, tc.hint) {
			t.Errorf("%s: err = %#v, want core.mcp.area with a hint holding %q", name, err, tc.hint)
		}
	}
	if len(calls()) != 0 {
		t.Errorf("claude was run despite a refused area: %v", calls())
	}
}

// The second run of the same registration says there is nothing to do, whatever
// order its areas are typed in.
func TestAnIdenticalRegistrationWithAreasIsLeftAlone(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	claudeHas(t, scopeLocal, "mcp", "serve", "--as", "claude", "--consent", "--area", "kube", "--area", "vault")

	out, errOut, err := run(t, areaRegistry(t), "mcp", "install", "claude", "--area", "vault", "--area", "kube", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if len(calls()) != 0 {
		t.Errorf("an identical registration ran the client: %v", calls())
	}
	if answerPairs(t, out)["already registered"] != "Claude Code" {
		t.Errorf("answered %s, want it to say it is already registered", out)
	}

	// And a different set of areas replaces it, which the answer says.
	if _, _, err := run(t, areaRegistry(t), "mcp", "install", "claude", "--area", "kube"); err != nil {
		t.Fatal(err)
	}
	all := calls()
	if got := all[len(all)-1]; !strings.HasSuffix(got, "--area kube") || strings.Contains(got, "vault") {
		t.Errorf("claude was run as %q, want the registration replaced with kube alone", got)
	}
}

// pflag reads --area "" as a list of nothing, which is no limit: the variable
// in --area "$AREAS" that is unset must not register, or start, a server that
// lists every area. Both commands refuse it.
func TestAnEmptyAreaIsRefusedAndNotTakenForEveryArea(t *testing.T) {
	calls := loggingClient(t, "claude", "exit 0")
	reg := areaRegistry(t)
	for _, command := range [][]string{{"mcp", "install", "claude"}, {"mcp", "serve", "--as", "probe"}} {
		_, _, err := run(t, reg, append(slices.Clone(command), "--area", "")...)
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != CodeUsage || !strings.Contains(ve.Message, "--area names no area") {
			t.Errorf("%v --area '': err = %#v, want a usage error saying so", command, err)
		}
	}
	if len(calls()) != 0 {
		t.Errorf("claude was run despite an empty area: %v", calls())
	}
}

// Both commands complete the areas there are for a tool to come from, with what
// owns each, and a repeated flag is not offered what it was given.
func TestTheAreaFlagCompletesTheAreasTheServerCanList(t *testing.T) {
	reg := areaRegistry(t)
	for _, command := range [][]string{{"mcp", "serve"}, {"mcp", "install", "claude"}} {
		got := values(complete(t, reg, append(slices.Clone(command), "--area", "")...))
		if want := []string{"kube", "note", "sys", "vault"}; !slices.Equal(got, want) {
			t.Errorf("%v --area completes to %v, want %v: lock holds nothing a tool can come from", command, got, want)
		}
		got = values(complete(t, reg, append(slices.Clone(command), "--area", "kube", "--area", "")...))
		if want := []string{"note", "sys", "vault"}; !slices.Equal(got, want) {
			t.Errorf("%v --area kube --area completes to %v, want %v", command, got, want)
		}
	}
	if got := complete(t, reg, "mcp", "serve", "--area", "v"); !slices.ContainsFunc(got, func(l string) bool { return strings.HasPrefix(l, "vault\tHashiCorp Vault") }) {
		t.Errorf("a completion never says what owns the area: %v", got)
	}
}

func TestBothCommandsTakeAnAreaAndServeSaysSo(t *testing.T) {
	root := NewRoot(areaRegistry(t), "test")
	for _, path := range [][]string{{"mcp", "serve"}, {"mcp", "install"}} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if f := cmd.Flags().Lookup("area"); f == nil || f.Value.Type() != "stringSlice" {
			t.Errorf("%v has no repeatable --area: %+v", path, f)
		}
	}
	serve, _, _ := root.Find([]string{"mcp", "serve"})
	areas, gate := strings.Index(serve.Long, "Areas: --area"), strings.Index(serve.Long, "Locality gate")
	if areas < 0 || gate < 0 || areas > gate {
		t.Errorf("mcp serve --help does not describe --area ahead of the locality gate (%d, %d):\n%s", areas, gate, serve.Long)
	}
}

// syncBuffer is a stderr the test reads while the server is still writing it.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// serveOverHTTP starts `rta mcp serve` over a registry and connects to it as a
// client would, returning the session and everything the server said on stderr
// by then. The server is a real command run in-process, so what is checked is
// what an operator and a client see rather than a struct handed to NewServer.
func serveOverHTTP(t *testing.T, reg *registry.Registry, extra ...string) (*sdk.ClientSession, func() string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("RTA_KV_PASSPHRASE", "")
	t.Setenv("RTA_KV_IDENTITY", "")
	const alice = "tok-a-0123456789abcdef"
	tokens := filepath.Join(dir, "tokens")
	if err := os.WriteFile(tokens, []byte("alice "+alice+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stderr := &syncBuffer{}
	cmd := NewRoot(reg, "test")
	cmd.SetOut(new(syncBuffer))
	cmd.SetErr(stderr)
	cmd.SetArgs(append([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0", "--token-file", tokens}, extra...))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("mcp serve did not stop cleanly: %v", err)
		}
	})

	listening := regexp.MustCompile(`listening on http://(\S+)`)
	var addr string
	for deadline := time.Now().Add(20 * time.Second); addr == ""; time.Sleep(10 * time.Millisecond) {
		if m := listening.FindStringSubmatch(stderr.String()); m != nil {
			addr = m[1]
		}
		select {
		case err := <-done:
			t.Fatalf("mcp serve stopped before it listened: %v\n%s", err, stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("mcp serve never listened:\n%s", stderr.String())
		}
	}

	connectCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	client := sdk.NewClient(&sdk.Implementation{Name: "probe-client", Version: "0"}, nil)
	session, err := client.Connect(connectCtx, &sdk.StreamableClientTransport{
		Endpoint:   "http://" + addr,
		HTTPClient: &http.Client{Transport: bearer{token: alice}},
	}, nil)
	if err != nil {
		t.Fatalf("connecting to mcp serve: %v\n%s", err, stderr.String())
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, stderr.String
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func listedNames(t *testing.T, s *sdk.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// An area the server was told about lists its tools and no others, the
// handshake names it alone, and the startup says how many tools that came to.
func TestServeListsOnlyTheAreasItIsToldAbout(t *testing.T) {
	session, stderr := serveOverHTTP(t, areaRegistry(t), "--area", "vault", "--area", "kube")
	if got, want := listedNames(t, session), []string{"kube_node_list", "kube_pod_list", "vault_kv_get"}; !slices.Equal(got, want) {
		t.Errorf("tools/list = %v, want %v: note is an area the server was not told about", got, want)
	}
	if said := session.InitializeResult().Instructions; !strings.HasSuffix(said, "the areas offered here are kube, vault.") {
		t.Errorf("the handshake ends %q, want it to name kube and vault alone", said)
	}
	if !strings.Contains(stderr(), "areas offered: kube, vault (3 tools); every other area is absent from tools/list") {
		t.Errorf("the startup never says what the server was limited to:\n%s", stderr())
	}
	if strings.Contains(stderr(), "names no area") {
		t.Errorf("a real area was reported as unknown:\n%s", stderr())
	}
}

// With no area, nothing changes: every area that can list a tool does, and the
// startup says nothing about areas.
func TestServeWithoutAnAreaListsEveryArea(t *testing.T) {
	session, stderr := serveOverHTTP(t, areaRegistry(t))
	// Over HTTP, so without the one that describes the machine this runs on.
	if got, want := listedNames(t, session), []string{"kube_node_list", "kube_pod_list", "note_add", "note_list", "vault_kv_get"}; !slices.Equal(got, want) {
		t.Errorf("tools/list = %v, want %v", got, want)
	}
	if strings.Contains(stderr(), "areas offered") {
		t.Errorf("a server limited to nothing says it was:\n%s", stderr())
	}
}

// An area that lists nothing is said on stderr, once, with what was meant, and
// the server comes up and serves the rest: the operator who typed it is the one
// present to read it, and a registration shared between machines is allowed to
// name a plugin one of them lacks.
func TestServeWarnsOfAnUnknownAreaAndServesTheRest(t *testing.T) {
	session, stderr := serveOverHTTP(t, areaRegistry(t), "--area", "kube", "--area", "vualt")
	if got, want := listedNames(t, session), []string{"kube_node_list", "kube_pod_list"}; !slices.Equal(got, want) {
		t.Errorf("tools/list = %v, want the one area that exists, %v", got, want)
	}
	if want := "rta: --area vualt names no area this server has, so it offers nothing for it — did you mean vault?"; !strings.Contains(stderr(), want) {
		t.Errorf("the startup never says %q:\n%s", want, stderr())
	}
	if !strings.Contains(stderr(), "areas offered: kube, vualt (2 tools)") {
		t.Errorf("the startup does not count the tools that are offered:\n%s", stderr())
	}
}
