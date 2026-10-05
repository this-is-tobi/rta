package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/consent"
)

// `w` was the agent tile's key, so it did nothing until that tile was the
// selected one. It is now the queue's from wherever a screen's letters are
// commands, and the first thing on the landing screen when a call waits.
func TestWOpensTheQueueFromTheLandingScreenWhileACallWaits(t *testing.T) {
	m, _ := realModel(t, 100, 30)
	m = learn(t, m, oneCall())
	if m.selected != 0 {
		t.Fatalf("the test needs the cold start, selected is %d", m.selected)
	}
	opened := press(t, m, "w")
	if opened.current.ID != queueCapability || opened.query != "" || opened.searchEditing {
		t.Errorf("w opened %q with %q in the bar (editing %v), want the queue", opened.current.ID, opened.query, opened.searchEditing)
	}
	if opened.origin != modeDashboard {
		t.Errorf("esc from the queue would go to %v, want the dashboard", opened.origin)
	}
	if bar := footerOf(m.dashboardView(), footerMaxLines); !strings.Contains(bar, "w answer") {
		t.Errorf("the footer does not offer w while a call waits:\n%s", bar)
	}
}

func TestWIsALetterWhenNothingWaitsAndWhenTheBarIsBeingTypedInto(t *testing.T) {
	quiet, _ := realModel(t, 100, 30)
	if got := press(t, quiet, "w"); got.query != "w" || got.current.ID == queueCapability {
		t.Errorf("w with nothing parked left query %q on %q, want the first letter of a search", got.query, got.current.ID)
	}
	busy := learn(t, quiet, oneCall())
	typing := press(t, busy, "/")
	if got := press(t, typing, "w"); got.query != "w" || got.current.ID == queueCapability {
		t.Errorf("w in an open bar left query %q on %q, want a letter of the query", got.query, got.current.ID)
	}
}

func TestWOpensTheQueueFromAResultAndAPaneAndNotFromAForm(t *testing.T) {
	var done []int
	result := learn(t, listResult(t, listRegistry(t, &done)), oneCall())
	// No agent plugin in this registry: nothing for w to open, and the line
	// does not promise it.
	if got := press(t, result, "w"); got.mode != result.mode {
		t.Errorf("w left %v for %v with no queue to open", result.mode, got.mode)
	}
	if line := firstLine(result.View().Content); strings.Contains(line, "w to answer") {
		t.Errorf("the line promises a key that opens nothing: %s", line)
	}

	base, _ := realModel(t, 100, 30)
	base.selected = 1
	plugins := learn(t, press(t, base, "p"), oneCall())
	if got := press(t, plugins, "w"); got.current.ID != queueCapability {
		t.Errorf("w in the plugin inventory opened %q", got.current.ID)
	}

	form, _ := realModel(t, 100, 30)
	form.selected = 1
	form = learn(t, press(t, form, "t"), oneCall())
	if form.mode != modeTheme {
		t.Fatalf("t opened %v, want the theme editor", form.mode)
	}
	if line := firstLine(form.View().Content); !strings.Contains(line, "esc, then w") {
		t.Errorf("the line over a form should say how to reach the queue: %s", line)
	}
	if got := press(t, form, "w"); got.mode != modeTheme || got.current.ID == queueCapability {
		t.Errorf("w in a form left %v on %q, want a letter typed into it", got.mode, got.current.ID)
	}
}

// The queue is read on its own clock and not from a tile, so a call that parks
// while some other screen is up reaches the line without a key.
func TestTheQueueIsReadOffDiskAndCleanedOnTheWay(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if got := readWaiting(); len(got) != 0 {
		t.Fatalf("an empty data directory read as %v", got)
	}
	parked, err := consent.Ask(consent.Call{
		Cap: "note.rm", Safety: "destructive", Scopes: []string{"2"}, Agent: "claude",
		Preview: "would remove note 2\x1b]52;c;ZXZpbA==\x07: third note\nsecond line", Why: "no grant",
	}, time.Minute)
	if err != nil {
		t.Fatalf("parking a call: %v", err)
	}
	defer parked.Close()
	got := readWaiting()
	if len(got) != 1 {
		t.Fatalf("read %d calls, want the one parked", len(got))
	}
	if got[0].agent != "claude" || got[0].call != "note.rm 2" {
		t.Errorf("read %+v", got[0])
	}
	if strings.ContainsAny(got[0].would, "\x1b\x07\n") || !strings.HasPrefix(got[0].would, "would remove note 2") {
		t.Errorf("the preview came through as %q, want one clean line", got[0].would)
	}
}

// The key is the capability's to declare, and the queue does not take it from
// a tile that has given it a meaning of its own.
func TestWIsLeftToATileThatDeclaresItForSomethingElse(t *testing.T) {
	m, _ := realModel(t, 100, 30)
	m = learn(t, m, oneCall())
	tile := -1
	for i, tl := range m.tiles {
		if !tl.search && tl.cap.ID == "agent.overview" {
			tile = i
		}
	}
	if tile < 0 {
		t.Fatal("no agent tile in this catalogue")
	}
	m.selected = tile
	if !m.answersWaiting() {
		t.Fatal("the agent tile's own w is the queue, which is what answers")
	}
	m.tiles[tile].actions = append([]capAction{}, m.tiles[tile].actions...)
	for i := range m.tiles[tile].actions {
		if m.tiles[tile].actions[i].key == "w" {
			m.tiles[tile].actions[i].cap.ID = "demo.whois"
		}
	}
	if m.answersWaiting() {
		t.Error("the queue took w from a tile that declares it for another capability")
	}
}
