package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// overviewOn runs agent.overview as the surface sf asks for it, with detail
// when the page is the one enter opens.
func overviewOn(t *testing.T, sf plugin.Surface, values map[string]any) view.View {
	t.Helper()
	c := capability(t, "agent.overview")
	v, err := c.Run(t.Context(), plugin.NewRequest(
		plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false).WithSurface(sf))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func pairsOf(t *testing.T, v view.View) []view.Pair {
	t.Helper()
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("overview is %T, want a KeyValue", v)
	}
	return kv.Pairs
}

func keysOf(pairs []view.Pair) []string {
	keys := make([]string, len(pairs))
	for i, p := range pairs {
		keys[i] = p.Key
	}
	return keys
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

// activityOf is the detail page's activity section: every line of the overview,
// the quiet ones too.
func activityOf(t *testing.T, sf plugin.Surface) []view.Pair {
	t.Helper()
	sections, ok := overviewOn(t, sf, map[string]any{"detail": true}).(view.Sections)
	if !ok {
		t.Fatal("the detail page is not sections")
	}
	for _, s := range sections.Items {
		if s.ID == "activity" {
			return pairsOf(t, s.View)
		}
	}
	t.Fatal("no activity section on the detail page")
	return nil
}

func refuse(t *testing.T, capID, code string, records ...string) {
	t.Helper()
	if err := agentlog.Append(agentlog.Entry{Cap: capID, Records: records, Outcome: agentlog.Refused,
		Auth: agentlog.Blocked, Code: code}); err != nil {
		t.Fatal(err)
	}
}

func openSession(t *testing.T, agent string) {
	t.Helper()
	if err := session.Start(session.Record{ID: session.NewID(), Agent: agent, Client: agent + " 1.0",
		Since: time.Now(), PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
}

// The first line of the overview is the verdict, led by a word the theme
// grades, and on a machine where nothing is the matter it is the whole of the
// news: the lines that are zero and none by default stay out of the compact
// answer, since a zero on a tile reads as a thing somebody is meant to watch.
func TestACalmOverviewIsAVerdictAndTheFactsThatMove(t *testing.T) {
	isolate(t)
	for sf, want := range map[plugin.Surface][]string{
		plugin.SurfaceCLI: {"status", "waiting on you", "needs your grant", "connected now", "calls in the last hour", "last call"},
		plugin.SurfaceTUI: {"status", "waiting on you", "needs grant", "connected now", "last hour", "last call"},
	} {
		pairs := pairsOf(t, overviewOn(t, sf, nil))
		if !slices.Equal(keysOf(pairs), want) {
			t.Errorf("%v: rows = %v, want %v", sf, keysOf(pairs), want)
		}
		if got := pairs[0].Value; got != "ok" || theme.ClassifyStatus(got) != theme.StatusGood {
			t.Errorf("%v: status = %q, want ok, which the theme draws as good", sf, got)
		}
	}
}

func TestACallWaitingOnYouIsTheVerdictAndTheRowsBelowIt(t *testing.T) {
	isolate(t)
	park(t, "kv.get", "db-password")
	park(t, "kv.set", "db-password")
	refuse(t, "kv.get", "core.grant.required", "api/stripe")

	pairs := pairsOf(t, overviewOn(t, plugin.SurfaceTUI, nil))
	if got := valueOf(t, pairs, "status"); got != "warn — 2 waiting" || theme.ClassifyStatus(got) != theme.StatusWarn {
		t.Errorf("tile: status = %q, want the worst fact in a few words, led by a word the theme draws as a warning", got)
	}
	cli := pairsOf(t, overviewOn(t, plugin.SurfaceCLI, nil))
	if got := valueOf(t, cli, "status"); got != "warn — 2 calls waiting on you" {
		t.Errorf("CLI: status = %q", got)
	}
	if got := keysOf(pairs)[:3]; !slices.Equal(got, []string{"status", "waiting on you", "needs grant"}) {
		t.Errorf("the first rows are %v, want what asks something of the operator right under the verdict", got)
	}
	// The dashboard reads the number off this line, and the key it promises.
	if got := valueOf(t, pairs, "waiting on you"); got != "2 — press w to answer" {
		t.Errorf("waiting on you = %q", got)
	}
}

func TestARefusalForWantOfAGrantLeadsWithPending(t *testing.T) {
	isolate(t)
	refuse(t, "kv.get", "core.grant.required", "api/stripe")

	pairs := pairsOf(t, overviewOn(t, plugin.SurfaceCLI, nil))
	got := valueOf(t, pairs, "needs your grant")
	if !strings.HasPrefix(got, "pending 1 — kv.get api/stripe") || theme.ClassifyStatus(got) != theme.StatusWarn {
		t.Errorf("needs your grant = %q, want it to lead with the word the theme draws as a warning", got)
	}
	if want := "warn — 1 call needs your grant"; valueOf(t, pairs, "status") != want {
		t.Errorf("status = %q, want %q", valueOf(t, pairs, "status"), want)
	}
}

func TestEveryKindOfRefusalFollowsTheGrantRow(t *testing.T) {
	isolate(t)
	refuse(t, "kv.get", "core.grant.required", "api/stripe")
	refuse(t, "fs.tree", "core.path.outside")
	refuse(t, "nosuch.tool", "core.mcp.unknown")
	if err := agentlog.Append(agentlog.Entry{Cap: "sys.cpu", Outcome: agentlog.Ran, Auth: agentlog.Live}); err != nil {
		t.Fatal(err)
	}

	pairs := pairsOf(t, overviewOn(t, plugin.SurfaceCLI, nil))
	want := []string{"status", "waiting on you", "needs your grant", "refused otherwise", "malformed or unknown calls",
		"connected now", "calls in the last hour", "you approved live", "last call"}
	if !slices.Equal(keysOf(pairs), want) {
		t.Errorf("rows = %v, want %v", keysOf(pairs), want)
	}
	if got := valueOf(t, pairs, "refused otherwise"); !strings.HasPrefix(got, "1 — fs.tree") {
		t.Errorf("refused otherwise = %q", got)
	}
}

// A tile is a third of the screen wide: the sentences that tell a person at a
// terminal what to type are the CLI's and the detail page's, and a tile says
// the one word they are about.
func TestOnATileTheConnectedRowIsOneWordAndTheHintsStayOnTheCLI(t *testing.T) {
	isolate(t)
	refuse(t, "kv.get", "core.grant.required", "api/stripe")
	refuse(t, "fs.tree", "core.path.outside")

	tile := pairsOf(t, overviewOn(t, plugin.SurfaceTUI, nil))
	if got := valueOf(t, tile, "connected now"); got != "none" {
		t.Errorf("tile: connected now = %q, want none", got)
	}
	// The key column is as wide as the longest key, and a tile has thirty-five
	// cells: the long keys go short there, and the refusals are counted, not named.
	if got := valueOf(t, tile, "needs grant"); got != "pending 1" {
		t.Errorf("tile: needs grant = %q, want the count, led by the pending word", got)
	}
	if got := valueOf(t, tile, "refused"); got != "1" {
		t.Errorf("tile: refused = %q", got)
	}
	for _, p := range tile {
		if n := utf8.RuneCountInString(p.Key); n > 14 {
			t.Errorf("tile: key %q is %d characters, wider than the key column a tile can spare", p.Key, n)
		}
	}
	for _, p := range tile {
		if strings.Contains(p.Value, "rta ") || strings.Contains(p.Value, "agent.log") || strings.Contains(p.Value, "lists them") {
			t.Errorf("tile: %s = %q carries a hint a tile has no room for", p.Key, p.Value)
		}
	}

	cli := pairsOf(t, overviewOn(t, plugin.SurfaceCLI, nil))
	if got := valueOf(t, cli, "connected now"); !strings.HasPrefix(got, "none — no client has an rta server open") ||
		!strings.Contains(got, "rta mcp install claude") {
		t.Errorf("CLI: connected now = %q, want the sentence and the install command", got)
	}
	if got := valueOf(t, cli, "needs your grant"); !strings.Contains(got, "`rta agent log --refused` lists them") {
		t.Errorf("CLI: needs your grant = %q, want where to read them", got)
	}

	// The page enter opens on a tile is not a tile: it keeps the sentences.
	detail := activityOf(t, plugin.SurfaceTUI)
	if got := valueOf(t, detail, "connected now"); !strings.Contains(got, "mcp install") {
		t.Errorf("detail: connected now = %q, want the sentence", got)
	}
}

// A client that is attached is its name and its calls on a tile: how many there
// are is the number of names, and "1 — claude (0 calls)" wraps in the room a tile
// leaves, where "claude (0 calls)" does not.
func TestOnATileTheConnectedClientsAreTheirNamesAndCalls(t *testing.T) {
	isolate(t)
	openSession(t, "claude")
	if got := valueOf(t, pairsOf(t, overviewOn(t, plugin.SurfaceTUI, nil)), "connected now"); got != "claude (0 calls)" {
		t.Errorf("tile: connected now = %q", got)
	}
	if got := valueOf(t, pairsOf(t, overviewOn(t, plugin.SurfaceCLI, nil)), "connected now"); got != "1 — claude (0 calls)" {
		t.Errorf("CLI: connected now = %q, want the count", got)
	}
	if got := valueOf(t, pairsOf(t, overviewOn(t, plugin.SurfaceTUI, nil)), "last call"); got != "none yet" {
		t.Errorf("tile: last call = %q on an empty record", got)
	}
	if got := valueOf(t, pairsOf(t, overviewOn(t, plugin.SurfaceCLI, nil)), "last call"); got != "nothing recorded yet" {
		t.Errorf("CLI: last call = %q on an empty record", got)
	}
}

func TestATilesValuesFitTheRoomATileLeavesBesideAKey(t *testing.T) {
	isolate(t)
	openSession(t, "claude")
	park(t, "kv.get", "db-password")
	refuse(t, "kv.get", "core.grant.required", "api/stripe")
	for _, p := range pairsOf(t, overviewOn(t, plugin.SurfaceTUI, nil)) {
		if n := utf8.RuneCountInString(p.Value); n > 42 {
			t.Errorf("tile: %s = %q is %d characters, past what a third of a 120-column screen leaves", p.Key, p.Value, n)
		}
	}
}

func TestTheQuietRowsLeaveTheCompactAnswerAndStayOnTheDetailPage(t *testing.T) {
	isolate(t)
	activity := activityOf(t, plugin.SurfaceCLI)
	want := []string{"status", "waiting on you", "needs your grant", "connected now", "locked", "roles in force",
		"calls in the last hour", "you approved live", "last call"}
	if !slices.Equal(keysOf(activity), want) {
		t.Errorf("detail activity rows = %v, want every row there is, quiet ones too: %v", keysOf(activity), want)
	}
	if got := valueOf(t, activity, "locked"); got != "nothing" {
		t.Errorf("locked = %q", got)
	}

	lock(t, "claude")
	pairs := pairsOf(t, overviewOn(t, plugin.SurfaceCLI, nil))
	if got := valueOf(t, pairs, "locked"); !strings.HasPrefix(got, "claude") {
		t.Errorf("locked = %q, want the frozen agent named in the compact answer once there is one", got)
	}
	if got := valueOf(t, pairs, "status"); got != "ok" {
		t.Errorf("status = %q: a lock the operator set is a fact to see, not a fault", got)
	}
}

func TestAQueueThatCannotBeReadIsSaidByTheVerdictAndTheRow(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file modes do not deny the owner here")
	}
	isolate(t)
	if err := os.MkdirAll(consent.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(consent.Dir(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(consent.Dir(), 0o700) })

	pairs := pairsOf(t, overviewOn(t, plugin.SurfaceTUI, nil))
	if got := valueOf(t, pairs, "status"); got != "warn — waiting queue could not be read" {
		t.Errorf("status = %q", got)
	}
	if got := valueOf(t, pairs, "waiting on you"); !strings.HasPrefix(got, "could not be read — ") ||
		theme.ClassifyStatus(got) != theme.StatusWarn {
		t.Errorf("waiting on you = %q, want it to say the queue could not be read, in the words the theme draws as a warning", got)
	}
}

func TestARecordThatCannotBeWrittenIsAFailOnTheVerdictAndTheTile(t *testing.T) {
	isolate(t)
	park(t, "kv.get", "db-password")
	if err := agentlog.Append(agentlog.Entry{Cap: "sys.cpu", Outcome: agentlog.Ran, Auth: agentlog.Open}); err != nil {
		t.Fatal(err)
	}
	// The scratch file the record's writability is tried with has a directory
	// where it goes, and the try removes what it leaves, so each look needs its own.
	blockTheRecord := func() {
		t.Helper()
		if err := os.Mkdir(filepath.Join(filepath.Dir(agentlog.Path()), "agent-log.probe"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	blockTheRecord()
	tile := pairsOf(t, overviewOn(t, plugin.SurfaceTUI, nil))
	if got := valueOf(t, tile, "status"); got != "fail — recording cannot be written" || theme.ClassifyStatus(got) != theme.StatusBad {
		t.Errorf("status = %q, want the worse fact to lead over a call waiting", got)
	}
	if got := valueOf(t, tile, "recording"); got != "fail — cannot be written" {
		t.Errorf("tile: recording = %q", got)
	}
	blockTheRecord()
	cli := pairsOf(t, overviewOn(t, plugin.SurfaceCLI, nil))
	if got := valueOf(t, cli, "recording"); !strings.HasPrefix(got, "fail — cannot be written") || !strings.Contains(got, "refused until it can") ||
		strings.Contains(got, agentlog.Path()) {
		t.Errorf("CLI: recording = %q, want the sentence and no path", got)
	}
}

// The compact answer is eight rows or fewer in every state an operator meets
// in a day: nothing going on, a call waiting, a client attached, and refusals
// of each kind at once.
func TestTheCompactAnswerIsEightRowsOrFewer(t *testing.T) {
	isolate(t)
	check := func(label string) {
		t.Helper()
		for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI} {
			if n := len(pairsOf(t, overviewOn(t, sf, nil))); n > 8 {
				t.Errorf("%s on %v: %d rows", label, sf, n)
			}
		}
	}
	check("calm")
	openSession(t, "claude")
	park(t, "kv.get", "db-password")
	check("one client attached and one call waiting")
	refuse(t, "kv.get", "core.grant.required", "api/stripe")
	refuse(t, "fs.tree", "core.path.outside")
	refuse(t, "nosuch.tool", "core.mcp.unknown")
	check("a refusal of each kind")
}
