package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/plugintrust"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// trustHello approves the example plugin's artifact for this test, in a data
// directory of its own.
//
// Every LoadInto test needs it, and that is the point of the gate rather than
// a cost of it: a binary on $PATH is not consent, so a test that wants a
// plugin loaded has to say so exactly as an operator would. Isolated per test
// so nothing here can read or write the developer's own trust file.
func trustHello(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	id, err := Identify(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	if verr := plugintrust.Add(id.Digest, "hello", id.Path); verr != nil {
		t.Fatal(verr)
	}
}

func touch(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

// The prefix rule has to be decidable in one direction, because both tiers
// live on the same $PATH: rta-plugin-* is the SDK tier and everything else
// matching rta-* is the exec tier. A binary counted under both would be
// reported twice with two different sets of guarantees.
func TestDiscoveryTakesSdkPluginsAndLeavesTheExecTierAlone(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "rta-plugin-hello"), 0o755)
	touch(t, filepath.Join(dir, "rta-plugin-pg"), 0o755)
	touch(t, filepath.Join(dir, "rta-legacy"), 0o755)  // exec tier
	touch(t, filepath.Join(dir, "rtaplugin-x"), 0o755) // no separator: not ours
	touch(t, filepath.Join(dir, "unrelated"), 0o755)
	touch(t, filepath.Join(dir, "rta-plugin-notexec"), 0o644) // not executable
	if err := os.Mkdir(filepath.Join(dir, "rta-plugin-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	got := Discover()
	want := map[string]bool{"hello": true, "pg": true}
	if len(got) != len(want) {
		t.Fatalf("discovered %v, want exactly %v", names(got), want)
	}
	for _, f := range got {
		if !want[f.Name] {
			t.Errorf("discovered %q, which is not an SDK plugin", f.Name)
		}
		if filepath.Dir(f.Path) != dir {
			t.Errorf("%q resolved to %q, outside the search dir", f.Name, f.Path)
		}
	}
}

// $PATH order decides, the way a shell decides, so a user build shadowing a
// packaged one behaves the way `which` says it will.
func TestTheFirstMatchOnPathWins(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	touch(t, filepath.Join(first, "rta-plugin-hello"), 0o755)
	touch(t, filepath.Join(second, "rta-plugin-hello"), 0o755)
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)

	got := Discover()
	if len(got) != 1 {
		t.Fatalf("discovered %v, want one", names(got))
	}
	if filepath.Dir(got[0].Path) != first {
		t.Errorf("resolved to %q, want the first $PATH entry %q", got[0].Path, first)
	}
}

// A missing or unreadable $PATH entry is ordinary — stale entries are in
// everybody's shell profile — and must not stop discovery of the rest.
func TestAnUnreadablePathEntryIsSkipped(t *testing.T) {
	good := t.TempDir()
	touch(t, filepath.Join(good, "rta-plugin-hello"), 0o755)
	t.Setenv("PATH", filepath.Join(t.TempDir(), "does-not-exist")+
		string(os.PathListSeparator)+good)
	if got := Discover(); len(got) != 1 {
		t.Errorf("discovered %v, want the one real plugin", names(got))
	}
}

// A namespace collision must cost the user the plugin, never the built-in.
// The registry refuses the second claim on a namespace, and the process
// behind it has to be torn down rather than left running for capabilities
// nothing can now reach.
func TestACollidingPluginIsRefusedWithoutTakingDownTheRegistry(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "hello", Summary: "the incumbent",
		Capabilities: []plugin.Capability{{
			ID: "hello.builtin", Summary: "already here", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "built-in"}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	linked := filepath.Join(dir, "rta-plugin-hello")
	if err := os.Symlink(hello(t), linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("PATH", dir)

	trustHello(t)
	h := New(nil)
	t.Cleanup(h.CloseAll)
	problems := h.LoadInto(context.Background(), reg)
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly the collision", problems)
	}

	// The built-in survived intact.
	if _, ok := reg.Capability("hello.builtin"); !ok {
		t.Error("the incumbent capability was lost")
	}
	if _, ok := reg.Capability("hello.greet"); ok {
		t.Error("the colliding plugin's capability was registered anyway")
	}
	// And its process is not left running for capabilities nothing can reach.
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.running) != 0 {
		t.Errorf("%d process(es) left running after a refused registration", len(h.running))
	}
}

// The whole point, at the level the user experiences it: a binary on $PATH
// becomes capabilities in the registry, with working handlers.
func TestADiscoveredPluginBecomesUsableCapabilities(t *testing.T) {
	dir := t.TempDir()
	linked := filepath.Join(dir, "rta-plugin-hello")
	if err := os.Symlink(hello(t), linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("PATH", dir)

	reg := registry.New()
	trustHello(t)
	h := New(nil)
	t.Cleanup(h.CloseAll)
	if problems := h.LoadInto(context.Background(), reg); len(problems) != 0 {
		t.Fatalf("loading: %v", problems)
	}

	c, ok := reg.Capability("hello.greet")
	if !ok {
		t.Fatal("hello.greet did not reach the registry")
	}
	v, err := c.Run(context.Background(),
		plugin.NewRequest(map[string]any{"name": "registry", "lang": "es"}, false, false))
	if err != nil {
		t.Fatalf("running a registered plugin capability: %v", err)
	}
	if text, _ := v.(view.Text); text.Body != "Hola, registry!" {
		t.Errorf("body = %q", text.Body)
	}
}

func names(found []Found) []string {
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.Name)
	}
	return out
}

// Two names for one artifact must not unregister the one that loaded.
//
// Open is a cache keyed on (digest, confinement, argv) and carries no path,
// so a byte-identical copy — or a hardlink, or a busybox-style multicall
// binary — is deliberately one process and hands back the *same* *Client. The
// bug was that LoadInto treated that client as private to the current
// discovery: reg.Register refused the namespace on the second pass, and the
// client it then closed and forgot was the incumbent.
//
// The state that left is worse than either outcome. The plugin stayed
// registered and callable while vanishing from h.running, so Loaded() went
// empty, PluginOrigins() lost the namespace, and the MCP gate read a binary
// from $PATH as a built-in — accepting an unpinned --allow-destructive, which
// is exactly the artifact binding trust exists to make. CloseAll
// could no longer see the process either, so the one Client.live relaunched
// on the next call outlived rta.
func TestASecondNameForOneBinaryDoesNotUnregisterTheFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("copies and launches a binary")
	}
	src, err := os.ReadFile(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{Prefix + "hello", Prefix + "hellotwin"} {
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	trustHello(t)

	h := New(nil)
	t.Cleanup(h.CloseAll)
	reg := registry.New()
	problems := h.LoadInto(context.Background(), reg)

	// The duplicate is reported rather than silently ignored...
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly one (the duplicate)", problems)
	}
	if !strings.Contains(problems[0].Error(), "same binary") {
		t.Errorf("the duplicate is not explained as one: %v", problems[0])
	}

	// ...and the plugin that loaded is still tracked, which is the whole
	// point: Loaded() is what PluginOrigins() and CloseAll are built on.
	if got := len(h.Loaded()); got != 1 {
		t.Fatalf("Loaded() = %d, want 1: a registered plugin is untracked", got)
	}
	if _, ok := reg.Capability("hello.greet"); !ok {
		t.Fatal("hello.greet is not registered")
	}
	if h.Loaded()[0].Declared.Name != "hello" {
		t.Errorf("tracked plugin is %q", h.Loaded()[0].Declared.Name)
	}

	// And it still runs, so nothing was killed out from under the registry.
	c, _ := reg.Capability("hello.greet")
	values := plugin.Resolve(c, plugin.Inputs{Caller: map[string]any{"name": "twin"}})
	if _, err := c.Run(context.Background(), plugin.NewRequest(values, false, false)); err != nil {
		t.Errorf("the registered capability no longer runs: %v", err)
	}
}

// installAs puts the example plugin — which declares the namespace "hello" —
// on disk under a chosen filename, so the two can be made to disagree.
func installAs(t *testing.T, dir, filename string) string {
	t.Helper()
	body, err := os.ReadFile(hello(t))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, filename)
	if err := os.WriteFile(out, body, 0o755); err != nil {
		t.Fatal(err)
	}
	return out
}

// A binary's declared namespace must be the name it is installed under.
//
// Found.Name has said so since it was written — "a binary named rta-plugin-x
// declaring namespace kv is a collision to refuse, not a name to trust" — and
// nothing implemented it. RegisterFrom refuses a namespace that is already
// taken, which protects the built-ins because they register first and
// protects nothing between two plugins on $PATH. A file called
// rta-plugin-aaa-innocent declaring "acmedb" registered as acmedb, appeared
// in `rta plugin list` as acmedb, and served `rta acmedb …` with nothing
// connecting the two names — and discovery being $PATH order then
// alphabetical, it beat a correctly-named binary to the namespace.
func TestAPluginMayOnlyDeclareTheNamespaceItIsInstalledUnder(t *testing.T) {
	dir := t.TempDir()
	// The example plugin declares "hello"; installing it under another name
	// is exactly the disagreement.
	installAs(t, dir, "rta-plugin-aaa-innocent")
	t.Setenv("PATH", dir)
	trustHello(t)

	h := New(nil)
	t.Cleanup(h.CloseAll)
	reg := registry.New()
	problems := h.LoadInto(context.Background(), reg)

	if len(reg.Plugins()) != 0 {
		t.Fatalf("registered %v under a name it was not installed as", reg.Plugins())
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want one", problems)
	}
	msg := problems[0].Error()
	for _, want := range []string{"aaa-innocent", "hello", "rta-plugin-hello"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q: %s", want, msg)
		}
	}

	// The two remedies have to be different remedies. The first version
	// interpolated the declared name into both halves and read "rename it to
	// rta-plugin-hello, or install it as rta-plugin-hello" — two ways of
	// saying one thing, one of which the operator had just done. Found by
	// installing a plugin under the wrong name and reading what came back.
	first, second, ok := strings.Cut(msg, ", or ")
	if !ok {
		t.Fatalf("the message no longer offers two remedies: %s", msg)
	}
	if strings.Contains(second, "rta-plugin-hello") {
		t.Errorf("both remedies name the same file, so one of them is not a remedy: %s", msg)
	}
	// The second remedy is the other direction: change the declaration to
	// match the file it is installed as.
	if !strings.Contains(second, "aaa-innocent") {
		t.Errorf("the second remedy does not name the installed namespace: %s", second)
	}
	_ = first
}

// The honest case still works, and this is the half that would make the rule
// worthless if it broke.
func TestAPluginInstalledUnderItsOwnNamespaceLoads(t *testing.T) {
	dir := t.TempDir()
	installAs(t, dir, "rta-plugin-hello")
	t.Setenv("PATH", dir)
	trustHello(t)

	h := New(nil)
	t.Cleanup(h.CloseAll)
	reg := registry.New()
	if problems := h.LoadInto(context.Background(), reg); len(problems) != 0 {
		t.Fatalf("a correctly-named plugin reported %v", problems)
	}
	if len(reg.Plugins()) != 1 || reg.Plugins()[0].Name != "hello" {
		t.Fatalf("registered %v, want exactly hello", reg.Plugins())
	}
}

// Two copies under one name is ordinary — a local build in front of a
// packaged one — so it stays first-wins rather than becoming an error. What
// changed is that the loser is recorded instead of dropped, because "why is
// it still running the old one" is otherwise an afternoon with `which -a`.
func TestShadowedCopiesAreRecordedOnTheWinner(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	installAs(t, first, "rta-plugin-hello")
	installAs(t, second, "rta-plugin-hello")
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)

	found := Discover()
	if len(found) != 1 {
		t.Fatalf("Discover returned %d entries, want the winner only", len(found))
	}
	if filepath.Dir(found[0].Path) != first {
		t.Errorf("winner is %s, want the one earlier on $PATH", found[0].Path)
	}
	if len(found[0].Shadowed) != 1 || filepath.Dir(found[0].Shadowed[0]) != second {
		t.Errorf("shadowed = %v, want the later copy", found[0].Shadowed)
	}
}

// A $PATH with a directory in it twice is ordinary — a profile sourced twice,
// a version manager prepending its bin on every shell. It cannot change which
// copy of a plugin wins, because the first occurrence already decided, so the
// only thing a second walk can produce is a file recorded as a shadow of
// itself. `rta doctor` printed exactly that: "2 further copy on $PATH not
// used", followed by one path, listed twice.
func TestADirectoryTwiceOnPathIsNotAPluginTwice(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, Prefix+"dup")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Named three ways that are the same directory, plus one genuine repeat.
	t.Setenv("PATH", strings.Join([]string{dir, dir + "/", dir}, string(os.PathListSeparator)))

	found := Discover()
	var dup *Found
	for i := range found {
		if found[i].Name == "dup" {
			dup = &found[i]
		}
	}
	if dup == nil {
		t.Fatal("the plugin was not discovered at all")
	}
	if len(dup.Shadowed) != 0 {
		t.Errorf("Shadowed = %v, want none — every entry is the same directory, "+
			"so there is no second copy to be shadowed by anything", dup.Shadowed)
	}
}

// The managed store's bin/ is discovered without being on $PATH — and after
// every $PATH entry, so a copy the operator put on $PATH shadows the managed
// one the ordinary, reported way rather than rta's store outranking a
// deliberate local build.
func TestTheManagedStoreIsDiscoveredLast(t *testing.T) {
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	managed := filepath.Join(data, "plugins", "bin")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(managed, "rta-plugin-hello"), 0o755)

	// Alone, the managed copy is found.
	t.Setenv("PATH", t.TempDir())
	found := Discover()
	if len(found) != 1 || found[0].Path != filepath.Join(managed, "rta-plugin-hello") {
		t.Fatalf("found = %+v, want the managed copy", found)
	}

	// Beside a $PATH copy, the $PATH copy wins and the managed one is the
	// recorded shadow.
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "rta-plugin-hello"), 0o755)
	t.Setenv("PATH", dir)
	found = Discover()
	if len(found) != 1 || found[0].Path != filepath.Join(dir, "rta-plugin-hello") {
		t.Fatalf("found = %+v, want the $PATH copy first", found)
	}
	if len(found[0].Shadowed) != 1 || found[0].Shadowed[0] != filepath.Join(managed, "rta-plugin-hello") {
		t.Fatalf("shadowed = %v, want the managed copy reported", found[0].Shadowed)
	}
}
