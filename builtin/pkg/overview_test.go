package pkg

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

const (
	brewBehind = `{"formulae":[{"name":"jq","installed_versions":["1.7"],"current_version":"1.8"},` +
		`{"name":"wget","installed_versions":["1.21"],"current_version":"1.24"}],"casks":[]}`
	brewCurrent = `{"formulae":[],"casks":[]}`
)

// machine is a fake with brew, apt and mise on it, each answering as scripted.
func machine(brew string, apt fakeAnswer) *fake {
	return &fake{
		bins: map[string]bool{"brew": true, "apt-get": true, "mise": true},
		answers: map[string]fakeAnswer{
			"brew outdated --json=v2": {out: brew},
			"apt list --upgradable":   apt,
			"mise outdated --json":    {out: "{}"},
		},
	}
}

var aptCurrent = fakeAnswer{out: "Listing...\n"}

// readsOS puts a scripted OS state where the overview reads one, so that the
// rows are the same on macOS and on Linux.
func readsOS(t *testing.T, st osState) {
	t.Helper()
	old := osReader
	osReader = func(context.Context) osState { return st }
	t.Cleanup(func() { osReader = old })
}

func overviewOn(t *testing.T, sf plugin.Surface, values map[string]any) view.View {
	t.Helper()
	r := req(t, "pkg.overview", values).WithSurface(sf)
	v, err := overviewWith(t.Context(), r, newRegistryClient())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func compact(t *testing.T, sf plugin.Surface) []view.Pair {
	t.Helper()
	return overviewOn(t, sf, nil).(view.KeyValue).Pairs
}

func keysOf(pairs []view.Pair) []string {
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.Key
	}
	return out
}

func valueOf(t *testing.T, pairs []view.Pair, key string) string {
	t.Helper()
	for _, p := range pairs {
		if p.Key == key {
			return p.Value
		}
	}
	t.Fatalf("no %q row in %v", key, keysOf(pairs))
	return ""
}

// summaryOn is the detail page's summary: every row there is.
func summaryOn(t *testing.T, sf plugin.Surface) []view.Pair {
	t.Helper()
	sections, ok := overviewOn(t, sf, map[string]any{"detail": true}).(view.Sections)
	if !ok {
		t.Fatal("the detail page is not sections")
	}
	for _, s := range sections.Items {
		if s.ID == "summary" {
			return s.View.(view.KeyValue).Pairs
		}
	}
	t.Fatal("no summary section")
	return nil
}

// The first row is the verdict, in a word the theme draws; the total and the
// reboot follow, because they are what the verdict is made of; and the managers
// that are fine are one row, where a machine with a dozen of them used to spend
// a dozen lines on the news that nothing was wrong.
func TestTheOverviewLeadsWithTheVerdictTheTotalAndTheReboot(t *testing.T) {
	install(t, machine(brewBehind, aptCurrent))
	readsOS(t, osState{})

	pairs := compact(t, plugin.SurfaceCLI)
	want := []string{"status", "total behind", "reboot", "brew", "managers"}
	if !slices.Equal(keysOf(pairs), want) {
		t.Fatalf("rows = %v, want %v", keysOf(pairs), want)
	}
	if got := valueOf(t, pairs, "status"); got != "warn — 2 packages behind" || theme.ClassifyStatus(got) != theme.StatusWarn {
		t.Errorf("status = %q", got)
	}
	for key, want := range map[string]string{
		"total behind": "2 packages, 0 tools", "reboot": "ok", "brew": "outdated 2", "managers": "2 current",
	} {
		if got := valueOf(t, pairs, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	// Nothing the matter is the one word.
	install(t, machine(brewCurrent, aptCurrent))
	pairs = compact(t, plugin.SurfaceCLI)
	if got := valueOf(t, pairs, "status"); got != "ok" || theme.ClassifyStatus(got) != theme.StatusGood {
		t.Errorf("status = %q on a machine with nothing behind", got)
	}
	if want := []string{"status", "total behind", "reboot", "managers"}; !slices.Equal(keysOf(pairs), want) {
		t.Errorf("rows = %v, want %v", keysOf(pairs), want)
	}
	if got := valueOf(t, pairs, "managers"); got != "3 current" {
		t.Errorf("managers = %q", got)
	}
}

// Nothing is lost by folding: the page enter opens lists every manager, the
// ones that are fine too, and says what the tools list is for.
func TestTheDetailPageKeepsEveryRowTheCompactAnswerFolded(t *testing.T) {
	install(t, machine(brewBehind, aptCurrent))
	readsOS(t, osState{})

	summary := summaryOn(t, plugin.SurfaceCLI)
	// The OS's own line is the machine's: macOS has "os updates" and Linux has
	// the kernel when it is behind, and neither is what this is about.
	got := slices.DeleteFunc(keysOf(summary), func(key string) bool { return key == "os updates" })
	want := []string{"status", "total behind", "reboot", "brew", "apt", "mise", "tools"}
	if !slices.Equal(got, want) {
		t.Errorf("summary rows = %v, want %v", got, want)
	}
	if got := valueOf(t, summary, "apt"); got != "ok" {
		t.Errorf("apt = %q", got)
	}
	if got := valueOf(t, summary, "tools"); !strings.HasPrefix(got, "none listed") {
		t.Errorf("tools = %q, want where to list them", got)
	}
	if slices.Contains(keysOf(compact(t, plugin.SurfaceCLI)), "tools") {
		t.Error("the compact answer has a tools row for a list nobody wrote")
	}
}

func TestAManagerThatCannotBeReadIsARowAndTheVerdictSaysSo(t *testing.T) {
	install(t, machine(brewBehind, fakeAnswer{code: 100, stderr: "E: Could not get lock /var/lib/dpkg/lock-frontend\n"}))
	readsOS(t, osState{})

	pairs := compact(t, plugin.SurfaceCLI)
	got := valueOf(t, pairs, "apt")
	if !strings.HasPrefix(got, "could not be read — apt: E: Could not get lock") || theme.ClassifyStatus(got) != theme.StatusWarn {
		t.Errorf("apt = %q, want it said in the words every overview uses", got)
	}
	if got := valueOf(t, pairs, "status"); got != "warn — apt could not be read" {
		t.Errorf("status = %q", got)
	}
	// What could not be read comes before what is behind: not knowing is the
	// part of the page the rest depends on.
	if want := []string{"status", "total behind", "reboot", "apt", "brew", "managers"}; !slices.Equal(keysOf(pairs), want) {
		t.Errorf("rows = %v, want %v", keysOf(pairs), want)
	}
	if got := valueOf(t, pairs, "managers"); got != "1 current" {
		t.Errorf("managers = %q, want the one that answered", got)
	}
}

// "os updates none offered" and "reboot ok" after softwareupdate did not answer
// were two claims about a machine nobody had looked at.
func TestWhenTheOSCouldNotBeReadTheRowsDoNotClaimNothingIsOwed(t *testing.T) {
	install(t, machine(brewCurrent, aptCurrent))
	readsOS(t, osState{Notes: []string{"softwareupdate did not answer within 25s"}})

	pairs := compact(t, plugin.SurfaceCLI)
	if got := valueOf(t, pairs, "os updates"); got != "unreadable — softwareupdate did not answer within 25s" ||
		theme.ClassifyStatus(got) != theme.StatusWarn {
		t.Errorf("os updates = %q", got)
	}
	if got := valueOf(t, pairs, "reboot"); got != "unknown" {
		t.Errorf("reboot = %q, want it unknown, not ok", got)
	}
	if got := valueOf(t, pairs, "status"); got != "warn — os updates could not be read" {
		t.Errorf("status = %q", got)
	}
	if slices.Contains(keysOf(pairs), "note") {
		t.Errorf("the note is a row of its own beside the claim it contradicts: %v", keysOf(pairs))
	}
	if want := []string{"status", "total behind", "reboot", "os updates", "managers"}; !slices.Equal(keysOf(pairs), want) {
		t.Errorf("rows = %v, want %v", keysOf(pairs), want)
	}
}

func TestARebootOwedOutranksPackagesBehind(t *testing.T) {
	install(t, machine(brewBehind, aptCurrent))
	readsOS(t, osState{RebootRequired: true, RebootReason: "an offered update restarts the machine"})

	pairs := compact(t, plugin.SurfaceCLI)
	if got := valueOf(t, pairs, "status"); got != "warn — reboot owed" {
		t.Errorf("status = %q", got)
	}
	if got := valueOf(t, pairs, "reboot"); got != "pending reboot — an offered update restarts the machine" ||
		theme.ClassifyStatus(got) != theme.StatusWarn {
		t.Errorf("reboot = %q", got)
	}
}

// Managers are asked at the same time: a machine with a dozen of them waited for
// each in turn, a minute apiece in the worst case, inside a tile that gets thirty
// seconds.
func TestTheManagersAreAskedAtTheSameTime(t *testing.T) {
	install(t, machine(brewCurrent, aptCurrent))
	readsOS(t, osState{})
	base := runCommand
	started, release := make(chan string, 8), make(chan struct{})
	runCommand = func(ctx context.Context, name string, args ...string) (string, string, error) {
		started <- name
		select {
		case <-release:
		case <-ctx.Done():
		}
		return base(ctx, name, args...)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		compact(t, plugin.SurfaceCLI)
	}()
	seen := map[string]bool{}
	for len(seen) < 3 {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(3 * time.Second):
			close(release)
			t.Fatalf("only %v were asked in three seconds: the others are waiting their turn", seen)
		}
	}
	close(release)
	<-done
}

// On a tile one deadline, shorter than the tile's own, bounds all of it: a
// manager that is still thinking is a row that says it could not be read, and
// the rest are answered, instead of a tile that timed out as a whole.
func TestASlowManagerOnATileIsUnreadableAndTheRestAreAnswered(t *testing.T) {
	f := machine(brewBehind, aptCurrent)
	install(t, f)
	readsOS(t, osState{})
	old := tileDeadline
	tileDeadline = 300 * time.Millisecond
	t.Cleanup(func() { tileDeadline = old })
	base := runCommand
	runCommand = func(ctx context.Context, name string, args ...string) (string, string, error) {
		if name == "apt" {
			<-ctx.Done()
			return "", "", ctx.Err()
		}
		return base(ctx, name, args...)
	}

	began := time.Now()
	pairs := compact(t, plugin.SurfaceTUI)
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("the tile took %v: the deadline did not bound it", took)
	}
	got := valueOf(t, pairs, "apt")
	if !strings.HasPrefix(got, "could not be read — apt did not answer within 300ms") || strings.Contains(got, "1m0s") {
		t.Errorf("apt = %q, want it to name the deadline that fired, not the minute a manager is otherwise given", got)
	}
	if got := valueOf(t, pairs, "brew"); got != "outdated 2" {
		t.Errorf("brew = %q: the manager that answered was lost with the one that did not", got)
	}
	if got := valueOf(t, pairs, "status"); got != "warn — apt could not be read" {
		t.Errorf("status = %q", got)
	}
}

// By hand there is no deadline of the tile's, only the minute a command is given.
func TestOffATileTheManagersAreNotGivenTheTilesDeadline(t *testing.T) {
	install(t, machine(brewBehind, aptCurrent))
	readsOS(t, osState{})
	old := tileDeadline
	tileDeadline = time.Nanosecond
	t.Cleanup(func() { tileDeadline = old })

	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI} {
		if got := valueOf(t, summaryOn(t, sf), "brew"); got != "outdated 2" {
			t.Errorf("%v: brew = %q: the page that is not a tile ran on the tile's clock", sf, got)
		}
	}
	if got := valueOf(t, compact(t, plugin.SurfaceCLI), "brew"); got != "outdated 2" {
		t.Errorf("CLI: brew = %q: a terminal ran on the tile's clock", got)
	}
}

// A slow command names the limit it was held to, which is the page's deadline
// when that is the shorter, and the minute otherwise.
func TestADeadlineIsNamedByTheLimitThatFired(t *testing.T) {
	install(t, &fake{bins: map[string]bool{"slow": true}})
	runCommand = func(ctx context.Context, _ string, _ ...string) (string, string, error) {
		<-ctx.Done()
		return "", "", ctx.Err()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, verr := runStatus(ctx, "slow"); verr == nil || !strings.Contains(verr.Message, "did not answer within 200ms") {
		t.Errorf("runStatus under a 200ms page deadline = %+v", verr)
	}
	// And the minute otherwise, which is not waited for here.
	if got := budget(t.Context(), listTimeout); got != time.Minute {
		t.Errorf("a command with no page deadline is held to %v, want the minute", got)
	}
	tile, cancelTile := context.WithTimeout(t.Context(), tileDeadline)
	defer cancelTile()
	if got := budget(tile, listTimeout); got != tileDeadline {
		t.Errorf("a command on a tile is held to %v, want the tile's %v", got, tileDeadline)
	}
}

// A tool and a manager that could not be read are said in one word.
func TestToolsAreSaidTheWayManagersAre(t *testing.T) {
	c := newRegistryClient()
	c.tags = newTagCache(time.Now)
	c.web = latestTags(t, map[string]string{"junegunn/fzf": "v0.55.0"})
	f := machine(brewCurrent, aptCurrent)
	f.bins["fzf"], f.bins["bat"] = true, true
	f.answers["fzf --version"] = fakeAnswer{out: "0.50.0\n"}
	f.answers["bat --version"] = fakeAnswer{out: "bat 0.24.0\n"}
	install(t, f)
	readsOS(t, osState{})
	tools := map[string]any{"tools": []string{"fzf=github:junegunn/fzf", "bat=github:sharkdp/bat"}}

	r := req(t, "pkg.overview", tools).WithSurface(plugin.SurfaceCLI)
	v, err := overviewWith(t.Context(), r, c)
	if err != nil {
		t.Fatal(err)
	}
	pairs := v.(view.KeyValue).Pairs
	// bat has no release on the fake GitHub: nothing to hold the installed one against.
	if got := valueOf(t, pairs, "tools"); got != "outdated 1 of 2 (1 could not be read)" || theme.ClassifyStatus(got) != theme.StatusWarn {
		t.Errorf("tools = %q", got)
	}
	if got := valueOf(t, pairs, "total behind"); got != "0 packages, 1 tool" {
		t.Errorf("total behind = %q", got)
	}
	if got := valueOf(t, pairs, "status"); got != "warn — 1 tool could not be read" {
		t.Errorf("status = %q, want what could not be read named before what is behind", got)
	}

	// Only the one nothing could be held against.
	tools["tools"] = []string{"bat=github:sharkdp/bat"}
	r = req(t, "pkg.overview", tools).WithSurface(plugin.SurfaceCLI)
	v, err = overviewWith(t.Context(), r, c)
	if err != nil {
		t.Fatal(err)
	}
	pairs = v.(view.KeyValue).Pairs
	if got := valueOf(t, pairs, "tools"); got != "could not be read — 1 of 1" {
		t.Errorf("tools = %q", got)
	}
	if got := valueOf(t, pairs, "status"); got != "warn — 1 tool could not be read" {
		t.Errorf("status = %q", got)
	}
}

// An entry that is not bin=github:owner/repo is a mistake in the configuration,
// said at once and not after the managers have been asked.
func TestABadToolEntryIsRefusedBeforeAnythingIsAsked(t *testing.T) {
	f := machine(brewCurrent, aptCurrent)
	install(t, f)
	readsOS(t, osState{})
	r := req(t, "pkg.overview", map[string]any{"tools": []string{"not a tool"}}).WithSurface(plugin.SurfaceCLI)
	if _, err := overviewWith(t.Context(), r, newRegistryClient()); err == nil {
		t.Fatal("a bad entry was accepted")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ran) != 0 {
		t.Errorf("%v were run for a list that was refused", f.ran)
	}
}

func TestAMachineWithNoManagerSaysSo(t *testing.T) {
	install(t, &fake{})
	readsOS(t, osState{})
	pairs := compact(t, plugin.SurfaceCLI)
	if got := valueOf(t, pairs, "managers"); got != "none found on $PATH" {
		t.Errorf("managers = %q", got)
	}
	if got := valueOf(t, pairs, "status"); got != "warn — no package manager found" {
		t.Errorf("status = %q: nothing was asked, which is not the same as nothing behind", got)
	}
}
