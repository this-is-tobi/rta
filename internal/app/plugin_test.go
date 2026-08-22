package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/builtin/all"
	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk/sdktest"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The M2 acceptance gate, as close to mechanically as it can be checked:
// scaffold a plugin, build it, and load it through the real host. If the
// template stops compiling — a renamed field in pkg/plugin, a changed handler
// signature — this fails, rather than a stranger discovering it.
//
// It is the most valuable test in this file by a distance. A scaffold is a
// copy of the SDK's surface frozen at the moment it was written, and nothing
// else in the tree references it, so it is exactly the kind of code that rots
// without anybody noticing until somebody new arrives.
func TestTheScaffoldBuildsAndLoads(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a binary")
	}
	root := repoRoot(t)
	dir := filepath.Join(t.TempDir(), "rta-plugin-probe")

	s := scaffold{
		Name:    "probe",
		Binary:  pluginhost.Prefix + "probe",
		Module:  "rta-plugin-probe",
		RtaMod:  rtaModule,
		RtaPath: root,
	}
	if err := s.write(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.go", "main_test.go", "go.mod", "README.md", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}

	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy on a fresh scaffold failed:\n%s", out)
	}
	binary := filepath.Join(dir, s.Binary)
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the scaffold does not compile:\n%s", out)
	}

	// The scaffold ships a conformance test, so it had better pass. An author
	// whose first `go test` fails on generated code learns to ignore it.
	suite := exec.Command("go", "test", "./...")
	suite.Dir = dir
	if out, err := suite.CombinedOutput(); err != nil {
		t.Fatalf("the scaffold fails its own conformance suite:\n%s", out)
	}

	// And it is a plugin rta accepts, not merely a program that builds.
	h := pluginhost.New(nil)
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), binary)
	if err != nil {
		t.Fatalf("the scaffolded plugin did not load: %v", err)
	}
	if c.Declared.Name != "probe" {
		t.Errorf("declared name = %q, want probe", c.Declared.Name)
	}
	if len(c.Declared.Capabilities) != 1 {
		t.Fatalf("capabilities = %d, want 1", len(c.Declared.Capabilities))
	}
	cap := c.Declared.Capabilities[0]
	if cap.ID != "probe.greet" {
		t.Errorf("capability id = %q", cap.ID)
	}
	v, err := cap.Run(context.Background(), plugin.NewRequest(map[string]any{"name": "world"}, false, false))
	if err != nil {
		t.Fatalf("running the scaffolded capability: %v", err)
	}
	if text, _ := v.(view.Text); !strings.Contains(text.Body, "world") {
		t.Errorf("body = %q", text.Body)
	}
}

// Nothing is written when anything would be overwritten. A scaffold that
// creates three files and refuses on the fourth leaves a directory that is
// neither empty nor a plugin, and the author has to work out which half is
// theirs.
func TestScaffoldingRefusesRatherThanOverwrites(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "main.go")
	if err := os.WriteFile(existing, []byte("package mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := scaffold{Name: "x", Binary: "rta-plugin-x", Module: "rta-plugin-x", RtaMod: rtaModule}
	err := s.write(dir)
	if err == nil {
		t.Fatal("an existing main.go was overwritten")
	}
	if ve, ok := err.(*view.Error); !ok || ve.Code != "plugin.exists" {
		t.Errorf("error = %v, want plugin.exists", err)
	}
	if data, _ := os.ReadFile(existing); string(data) != "package mine\n" {
		t.Error("the existing file was modified")
	}
	// And none of the others landed either.
	for _, name := range []string{"go.mod", "README.md", ".gitignore", "main_test.go"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s was written despite the refusal", name)
		}
	}
}

// The written .gitignore has to cover both names a build can actually
// produce, not just the installed one — found by running `make plugins`
// against plugins/pg and plugins/eol, both scaffolded into a short
// plugins/<name> directory (this repo's own convention for a first-party
// plugin) rather than the default plugins/rta-plugin-<name>, where
// `go build ./...` names its output after the directory instead of
// {{.Binary}}. plugins/pg had been carrying that artifact as a tracked file
// since it was written, for exactly this reason.
func TestScaffoldedGitignoreCoversBothBuildArtifactNames(t *testing.T) {
	dir := t.TempDir()
	s := scaffold{Name: "eol", Binary: "rta-plugin-eol", Module: "rta-plugin-eol", RtaMod: rtaModule}
	if err := s.write(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, want := range []string{s.Binary, s.Name} {
		if !slices.Contains(lines, want) {
			t.Errorf(".gitignore = %q, missing a line for %q", data, want)
		}
	}
}

// The name becomes a cobra command, the prefix of every capability ID and
// part of a filename, so it has to satisfy all three.
func TestPluginNamesAreCheckedAgainstWhatTheyBecome(t *testing.T) {
	for _, ok := range []string{"pg", "kube", "my-plugin", "s3", "a"} {
		if verr := checkName(ok); verr != nil {
			t.Errorf("%q was refused: %v", ok, verr)
		}
	}
	for _, bad := range []string{
		"",          // nothing
		"My-Plugin", // a command nobody would type twice
		"my.plugin", // would make a capability ID with two dots
		"my plugin", // not one argv element
		"-leading",  // reads as a flag
		"9lives",    // a command starting with a digit
		"../escape", // part of a filename
		"a/b",       // ditto
		strings.Repeat("x", 40),
	} {
		if verr := checkName(bad); verr == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// The replace directive is what makes a scaffold build before rta is
// published, so an absent source has to be said out loud rather than emitted
// as a path that is not there.
func TestTheReplaceDirectiveIsOnlyEmittedWhenItPointsSomewhere(t *testing.T) {
	with := scaffold{Name: "x", Binary: "rta-plugin-x", Module: "m", RtaMod: rtaModule, RtaPath: "/somewhere/rta"}
	dir := t.TempDir()
	if err := with.write(dir); err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "replace "+rtaModule+" => /somewhere/rta") {
		t.Errorf("go.mod has no replace:\n%s", mod)
	}

	without := with
	without.RtaPath = ""
	other := t.TempDir()
	if err := without.write(other); err != nil {
		t.Fatal(err)
	}
	mod, err = os.ReadFile(filepath.Join(other, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mod), "replace") {
		t.Errorf("a replace was emitted with nothing to point at:\n%s", mod)
	}
	// And the author is told, rather than left to read a build error.
	steps := nextSteps(without, other)
	if !strings.Contains(steps, "--rta-source") {
		t.Errorf("the next steps do not say how to fix it:\n%s", steps)
	}
}

// localRta walks up, because scaffolding beside a checkout is as common as
// scaffolding inside one.
func TestLocalRtaFindsTheCheckoutFromBelow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module "+rtaModule+"\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := localRta(deep); got != root {
		t.Errorf("localRta(%q) = %q, want %q", deep, got, root)
	}
	// A directory with a different module must not match.
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "go.mod"),
		[]byte("module example.com/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := localRta(other); got == other {
		t.Errorf("localRta matched an unrelated module at %q", other)
	}
}

// repoRoot is the rta checkout these tests run inside.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("cannot find the rta checkout at %s: %v", dir, err)
	}
	return dir
}

// An error a command returns without printing it must reach the user.
//
// The rule this replaced could not hold: the top-level handler suppressed
// *every* *view.Error, on the assumption that runCapability — which prints
// its own — was the only thing producing one. It was not. `rta plugin dev`
// returns view.Errors of its own, and every one of them vanished, so a plugin
// that failed to compile exited 1 with nothing on the terminal at all.
//
// The default is now inverted: an unmarked view.Error is printed, and only
// the ones explicitly marked as already-rendered are suppressed. A new
// command that returns one gets it shown, which is the behaviour worth having
// by default because its failure mode is loud rather than silent.
func TestOnlyAlreadyPrintedErrorsAreSuppressed(t *testing.T) {
	raw := view.Errorf("plugin.dev.build", "the build failed").WithHint("run go mod tidy")

	if _, ok := any(raw).(RenderedError); ok {
		t.Fatal("a plain view.Error claims to have been rendered")
	}
	marked := Rendered(raw)
	if _, ok := marked.(RenderedError); !ok {
		t.Fatalf("Rendered returned %T", marked)
	}
	if Rendered(nil) != nil {
		t.Error("Rendered(nil) produced a non-nil error, which would fail a command that succeeded")
	}

	// The exit-code contract must see through the marker, or whether a
	// command printed its own error would change its exit code.
	if got := ExitCode(raw); got != 1 {
		t.Errorf("ExitCode(unmarked) = %d, want 1", got)
	}
	if got := ExitCode(marked); got != 1 {
		t.Errorf("ExitCode(marked) = %d, want 1", got)
	}
	confirm := view.Errorf(CodeConfirmRequired, "needs --yes")
	if got := ExitCode(Rendered(confirm)); got != 3 {
		t.Errorf("ExitCode(marked confirm-required) = %d, want 3", got)
	}
	// And the message survives the wrapper, since something has to print it.
	if marked.Error() != "the build failed" {
		t.Errorf("marked.Error() = %q", marked.Error())
	}
}

// The authoring guide's verb list must be the one sdktest enforces.
//
// It was not, and the failure ran in the worst direction: the guide listed
// cp, run, watch, backup and restore — none of them vocabulary — and omitted
// eleven words that are. An author following the page would have been warned
// by the very tool the page told them to trust, about words the page gave
// them.
//
// sdktest exports Vocabulary() so the list has exactly one home. Prose is a
// second home whether or not anybody intended it to be, so this is the test
// that makes it a copy rather than a fork.
func TestTheGuideAndTheToolAgreeOnTheVerbVocabulary(t *testing.T) {
	guide := filepath.Join(repoRoot(t), "docs", "writing-a-plugin.md")
	data, err := os.ReadFile(guide)
	if err != nil {
		t.Fatal(err)
	}
	_, after, found := strings.Cut(string(data), "The whole list:")
	if !found {
		t.Fatal("the guide no longer introduces the vocabulary with `The whole list:`")
	}
	_, block, _ := strings.Cut(after, "```")
	block, _, ok := strings.Cut(block, "```")
	if !ok {
		t.Fatal("no fenced block follows the vocabulary introduction")
	}

	documented := strings.Fields(block)
	sort.Strings(documented)
	enforced := sdktest.Vocabulary()
	sort.Strings(enforced)

	if !slices.Equal(documented, enforced) {
		t.Errorf("the guide and sdktest disagree:\n  guide:    %v\n  enforced: %v\n"+
			"  only in guide:    %v\n  only in sdktest: %v",
			documented, enforced,
			missing(documented, enforced), missing(enforced, documented))
	}
}

// missing returns the elements of a that are not in b.
func missing(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

// The README's headline count must be the catalogue's real size.
//
// It said 73 while the catalogue held 76, which is the same drift as the verb
// list and matters for the same reason: a number nothing checks is a number
// that is wrong shortly after it is written, and it is the first concrete
// claim anybody reads about the project.
//
// The test is deliberately brittle — adding a capability fails it. That is the
// point, and the fix is one word, named in the failure. Doing it any other way
// means the README states a number rta does not stand behind.
func TestTheREADMECountsTheCatalogueCorrectly(t *testing.T) {
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	plugins, caps := len(reg.Plugins()), len(reg.Capabilities())

	data, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%d built-in plugins, %d capabilities", plugins, caps)
	if !strings.Contains(string(data), want) {
		got := regexp.MustCompile(`\d+ built-in plugins, \d+ capabilities`).Find(data)
		t.Errorf("README says %q, the catalogue is %q", got, want)
	}
}

// `rta plugin dev` is the author's only view of what rta decided about their
// plugin, and which capability becomes the dashboard tile is a decision they
// cannot read off their own source: it falls out of safety classes, defaults,
// NoPreview and declaration order together. An author who wanted a different
// tile has no way to discover that `<plugin>.overview` is the lever unless
// the report names the one they got.
func TestDevReportNamesTheDashboardTile(t *testing.T) {
	p := plugin.Plugin{
		Name: "acme", Summary: "a third-party plugin", Capabilities: []plugin.Capability{
			{
				ID: "acme.debug-dump", Summary: "declared first, and nobody's idea of a glance",
				Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "goroutine 1 [running]"}, nil
				},
			},
			{
				ID: "acme.overview", Summary: "what acme looks like at a glance", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "all good"}, nil
				},
			},
		},
	}
	reg := registry.New()
	if err := reg.RegisterFrom(p, registry.Origin{Path: "/usr/local/bin/rta-plugin-acme", Digest: "sha256:acme"}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := cli.Render(&buf, devReport(reg, &pluginhost.Client{Declared: p}),
		cli.Options{Format: cli.Pretty, NoColor: true, Width: 100}); err != nil {
		t.Fatal(err)
	}
	// The value on that line, not merely somewhere in the report: every
	// capability is listed in the table below it, so "the report contains
	// acme.overview" is true no matter which tile it named.
	line := ""
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "dashboard tile") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("plugin dev report does not mention the dashboard tile:\n%s", buf.String())
	}
	if !strings.Contains(line, "acme.overview") {
		t.Errorf("dashboard tile line names something else: %q", strings.TrimSpace(line))
	}
}
