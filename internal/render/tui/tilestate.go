package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/view"
)

// What a tile says about its own state, read from what it returned.

// notHere names the error codes that mean a tile has nothing to say from where
// rta was started, which is a different thing from something being broken, with
// the sentence the tile draws in place of an error badge.
//
// git.overview is the case that made the list: the landing screen is opened
// from wherever the shell happens to be, which is usually not a repository, and
// the first thing a newcomer saw was a red ERROR badge and a hint about bare
// repositories on a tile they never asked for. The sentence is muted, the way an
// empty notebook's is, and the full error, with its hint, is what enter opens.
//
// Host-side, with the rest of what the landing screen decides (leftOff): the
// capability's error is right for `rta git overview`, which is asked about a
// directory, and wrong for a glance that was never asked.
var notHere = map[string]string{
	"git.notarepo": "Not inside a git repository — start rta in a checkout and this tile fills in.",
}

// quietSentence is what a tile draws instead of an error when it asked about
// "here" and here has no answer. A tile that was given inputs asked about
// something particular, and a particular directory that is not a repository is
// a mistake worth a badge.
func quietSentence(t tile) (string, bool) {
	if t.err == nil || len(t.values) > 0 {
		return "", false
	}
	say, ok := notHere[t.err.Code]
	return say, ok
}

// pausedAs is the error a tile draws: its own, or — once its credential has been
// refused credentialRefusals times in a row and it is no longer run — the same
// error under the sentence that says why it has stopped and the key that asks
// again. Said in the failed tile's own vocabulary, with the refusal beside it,
// because a tile that stopped on its own and said nothing would look like one
// that was fine, and the person has to know which credential it was.
func pausedAs(t tile) *view.Error {
	if !t.paused() {
		return t.err
	}
	hint := "r asks again once it is fixed"
	if t.err.Hint != "" {
		hint = t.err.Hint + " — " + hint
	}
	return &view.Error{Code: t.err.Code, Message: "paused — credentials refused: " + t.err.Message, Hint: hint}
}

// slowLaunch is the pace from which a tile is not run when the TUI opens: a
// capability that declares an hour or more between runs (Refresh) is saying
// that its answer moves by the hour and that a run costs something somebody
// else answers for, an OSV lookup per dependency, a handshake per host, a
// kubectl audit. The pace used to bound a running TUI and never a launch, so
// each start spent every tile's budget at once.
const slowLaunch = time.Hour

// deferSlowLaunches marks the tiles that wait out their first pace, from now, or
// until r. It is called once, as the TUI opens: a tile that joins the dashboard
// later was asked for by somebody who is looking at it, and is run.
//
// By what the capability declares and not by the pace the tile ends up with: a
// configuration that slows a cheap tile to an hour has not made its run costly,
// and an empty tile for an hour would be the price of having said so.
func deferSlowLaunches(tiles []tile, now time.Time) {
	for i, t := range tiles {
		if !t.search && t.cap.Run != nil && t.cap.Refresh >= slowLaunch {
			tiles[i].notBefore = now.Add(t.pace())
		}
	}
}

// deferred is whether this tile has been left unrun at launch and its first pace
// has not yet elapsed.
func (t tile) deferred(now time.Time) bool {
	return t.lastFired.IsZero() && now.Before(t.notBefore)
}

// notRunYet is what a deferred tile says, in the voice of a tile with nothing to
// report: muted, since nothing is wrong, and naming the key that runs it.
const notRunYet = "not run yet — r runs it"

// keptAnswer is the answer a tile is drawn from instead of its latest run: the
// last good one while the runs fail, or the latest one once it has gone old.
// Either way it is drawn dimmed, with when it was current.
//
// A tile used to be overwritten by every answer, success or failure, so a
// backend that went away turned every tile into an error badge with no word of
// when it had last been fine, and a run that took a minute left the old rows
// unchanged, looking current, for that minute. What the tile knew is still worth
// reading; what is wrong is that it cannot be told from what is true now.
type keptAnswer struct {
	view view.View
	at   time.Time
	// failed says the latest run failed, as against merely being long ago.
	failed bool
}

// keepAnswer is what the tile is drawn from, when that is not its latest answer,
// given the raw record of its answers and now.
//
// **Old is twice the pace**, not once: a run takes a while, and a tile whose
// next answer is on its way is not yet stale. A paused tile keeps its pause
// wording and nothing else, since what it has to say is that the credential was
// refused; and a failure with no good answer before it is drawn as it is, the
// error and its hint, which is the only thing the tile knows.
func keepAnswer(t tile, r returned, now time.Time) *keptAnswer {
	if t.search || t.cap.Run == nil || t.paused() {
		return nil
	}
	switch {
	case t.err != nil:
		if _, quiet := quietSentence(t); quiet || r.good == nil {
			return nil
		}
		return &keptAnswer{view: r.good, at: r.goodAt, failed: true}
	case t.view != nil && !r.at.IsZero() && now.Sub(r.at) > 2*t.pace():
		return &keptAnswer{view: t.view, at: r.at}
	}
	return nil
}

// says is the panel's right-hand side for a kept answer, in the fullest form
// that leaves the title room in a border width cells wide: `as of 14:02 · refresh
// failed`, then the briefer ones a tile a third of a screen wide can hold. The
// time carries its day when it is not today's, since yesterday's 14:02 is not.
func (k keptAnswer) says(now time.Time, titleWidth, width int) string {
	stamp := k.at.Local().Format("15:04")
	if y, m, d := k.at.Local().Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		stamp = k.at.Local().Format("Jan 2 15:04")
	}
	forms := []string{"as of " + stamp, stamp}
	if k.failed {
		forms = []string{"as of " + stamp + " · refresh failed", "as of " + stamp + " · failed", stamp + " · failed"}
	}
	for _, f := range forms {
		if width-(lipgloss.Width(f)+2)-7 >= titleWidth {
			return f
		}
	}
	return forms[len(forms)-1]
}

// dimmed is a tile body with its colours taken off and the muted one put on: an
// answer that is not current does not get to be green.
func dimmed(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = theme.Subtle.Render(ansi.Strip(line))
	}
	return strings.Join(lines, "\n")
}

// waitingKey is the label of the line agent.overview puts a count of parked
// calls under. The dashboard reads the number off it rather than asking the
// agent plugin, which it knows nothing else about; TestTheAgentTilesWaitingLineIsTheOneTheDashboardReads
// holds the two spellings together.
const waitingKey = "waiting on you"

// grade is how much a tile's answer asks for attention, which is the one thing
// worth carrying out of a tile that is not drawn: a section folded to its header
// still has to say that something inside it is wrong.
//
// **It is read from the answer on demand and not tracked.** A tile holds what its
// capability returned, kept since the last run (tileRaw), and the status columns
// and the warnings in it are already what the tile's panel colours. So a header
// that wants the worst of its tiles reads them as the panel does, when the header
// is drawn, and there is no second record of a tile's state to keep in step with
// the first. It costs a pass over each answer's rows per paint of a header, which
// is nothing next to rendering the panels a folded section does not draw.
type grade int

const (
	// gradeNone is an answer that grades nothing: a list of facts, a chart, a table
	// with no status or usage column. Not the same as fine, and never drawn as
	// "ok": a header that called a section ok because its tiles had nothing graded
	// to say would be claiming what no tile said.
	gradeNone grade = iota
	gradeGood
	gradeWarn
	gradeBad
)

// gradeOf maps the theme's classification onto the three that count.
func gradeOf(k theme.StatusKind) grade {
	switch k {
	case theme.StatusGood:
		return gradeGood
	case theme.StatusWarn:
		return gradeWarn
	case theme.StatusBad:
		return gradeBad
	}
	return gradeNone
}

// tileGrade is the worst a tile says about itself, and whether it has said
// anything yet. An error is bad, except the ones the panel itself draws as a
// quiet sentence (notHere): a tile that has nothing to say from here is not a
// tile with a problem. A tile that has not answered is not graded; the header
// says it is still loading instead.
func tileGrade(t tile) (g grade, answered bool) {
	switch {
	case t.err != nil:
		if _, quiet := quietSentence(t); quiet {
			return gradeNone, true
		}
		return gradeBad, true
	case t.view != nil:
		return viewGrade(t.view), true
	case t.deferred(time.Now()):
		// It has said all it will until it is run, and a header that read it as
		// still loading would say so for the hour.
		return gradeNone, true
	}
	return gradeNone, false
}

// viewGrade is the worst grade in a view: each status or usage cell, read by the
// words the table colours them by, a warning that says the answer is partial,
// the calls the agent tile says are waiting, and everything inside a composite.
func viewGrade(v view.View) grade {
	worst := gradeNone
	switch v := v.(type) {
	case view.Table:
		for col, c := range v.Columns {
			if !isGraded(c.Kind) {
				continue
			}
			for _, row := range v.Rows {
				if col < len(row) {
					worst = max(worst, cellGrade(c.Kind, row[col]))
				}
			}
		}
		worst = max(worst, warningsGrade(v.Warnings))
	case view.KeyValue:
		if waitingCalls(v) > 0 {
			worst = gradeWarn
		}
		// A page of facts has no status column, but the line that says its backend
		// is down is drawn red by the words it begins with, and what is red on a
		// tile is what its header counts. Only the bad and the partial: "up 3
		// days" is a good word and a fact, and a header says ok when a tile said
		// so. A masked value is not read, whatever it says.
		for _, p := range v.Pairs {
			if slices.Contains(v.Redacted, p.Key) {
				continue
			}
			if g := gradeOf(theme.ClassifyStatus(p.Value)); g == gradeWarn || g == gradeBad {
				worst = max(worst, g)
			}
		}
	case view.Sections:
		for _, s := range v.Items {
			if s.View != nil {
				worst = max(worst, viewGrade(s.View))
			}
		}
		worst = max(worst, warningsGrade(v.Warnings))
	}
	return worst
}

// cellGrade grades one cell of a graded column the way the table's own colours do.
func cellGrade(kind view.ColumnKind, cell string) grade {
	if kind == view.KindUsage {
		return gradeOf(theme.ClassifyUsage(cell))
	}
	return gradeOf(theme.ClassifyStatus(cell))
}

// warningsGrade is a warning when the answer says it is partial. An advisory
// warning sits beside a complete answer, by its own account, so it is not one.
func warningsGrade(warnings []view.Error) grade {
	for _, w := range warnings {
		if !w.Advisory {
			return gradeWarn
		}
	}
	return gradeNone
}

// waitingCalls is how many calls the tile says are parked waiting for an
// answer, 0 for a tile that says none, says nothing about it, or cannot read
// the queue — "unreadable — …" is not a count, and a badge for it would be the
// dashboard asserting what the tile did not.
func waitingCalls(v view.View) int {
	kv, ok := v.(view.KeyValue)
	if !ok {
		return 0
	}
	for _, p := range kv.Pairs {
		if p.Key != waitingKey {
			continue
		}
		digits, _, _ := strings.Cut(p.Value, " ")
		if n, err := strconv.Atoi(digits); err == nil && n > 0 {
			return n
		}
	}
	return 0
}
