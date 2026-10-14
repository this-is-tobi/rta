package tui

import (
	"slices"
	"strconv"
	"strings"

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
