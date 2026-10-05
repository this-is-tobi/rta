package sdktest

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// --- (h) wording ----------------------------------------------------------

// The most an agent is made to read of one capability's author-written words,
// and of one input's. A description is paid for by every session that lists the
// tools, whether or not it ever calls this one, and it is read by something
// with no terminal and no pipe. Both numbers are measured from rta's own
// catalogue and not chosen for roundness: the median capability's summary and
// description are about four hundred bytes, the longest were nearly two
// thousand, and what the long ones held was detail a person reads in `--help`
// and a model never acts on.
const (
	agentTextBudget = 800
	inputHelpBudget = 160
)

// timeWords end the name of an input that is a length of time. The catalogue
// has more than twenty of them, and each is an Int whose unit lives in its help.
var timeWords = []string{"timeout", "wait", "ttl", "interval", "delay", "duration", "age"}

// timeUnit is a unit of time written out, as an input's help states one.
var timeUnit = regexp.MustCompile(`(?i)\b(?:milli|micro)?seconds?\b|\bminutes?\b|\bhours?\b|\bdays?\b|\bms\b`)

// limitSynonyms are the names other plugins give the input that caps how many
// rows a list returns, which the catalogue calls limit.
var limitSynonyms = []string{"max-results", "max-items", "page-size", "pagesize", "per-page", "top", "num", "rows"}

// checkWording holds what a capability tells an agent, and how it names its
// inputs, to what the catalogue already does. The agent text, the help of
// every input an agent can give, and the summary are the words a model chooses
// a tool by, and a model reads all of them on every connection.
//
// Errors are the findings with no intended case, held by rta's own tests for
// its own catalogue: wording that speaks to a person at a terminal when the
// reader has none, an agent text that outgrows its budget or opens by saying
// its summary again, one that names an input the agent cannot give, an input
// that is a length of time and never says in what unit. Notes are the
// conventions an author may have a reason to differ from: how a summary is
// written, a different word for the input the catalogue calls limit.
//
// A HumanOnly capability is never a tool, so what an agent reads is nobody's
// and only its input names are held.
func checkWording(t reporter, p plugin.Plugin, cfg config) {
	t.Helper()

	for _, c := range p.Capabilities {
		if cfg.skipped(RuleWording, c.ID) {
			continue
		}
		checkSummaryStyle(t, c)
		checkInputNames(t, c)
		if !c.HumanOnly {
			checkAgentWords(t, c)
		}
	}
}

// checkSummaryStyle notes a summary written unlike the catalogue's: every one
// of rta's starts with a capital and ends without a full stop, so the list
// `rta` prints reads as one voice.
func checkSummaryStyle(t reporter, c plugin.Capability) {
	t.Helper()

	first, _ := firstRune(c.Summary)
	switch {
	case unicode.IsLower(first):
		t.Logf("sdktest: %s: %s summary starts with a lowercase letter; the catalogue's start with a capital: %q",
			RuleWording, c.ID, c.Summary)
	case strings.HasSuffix(strings.TrimSpace(c.Summary), "."):
		t.Logf("sdktest: %s: %s summary ends with a full stop; the catalogue's end without one: %q",
			RuleWording, c.ID, c.Summary)
	}
}

func firstRune(s string) (rune, bool) {
	for _, r := range s {
		return r, true
	}
	return 0, false
}

// checkInputNames holds the names an input is given, and the unit a time says.
//
// A time that is a number is the quiet mistake: `--timeout 30` is half a
// minute in one capability and thirty milliseconds in another, and the unit
// lives in a help string, where an agent reads it as prose and a form never
// shows it. One whose help does not say is an error; one that does is a note
// to declare a plugin.Duration, which carries the unit with the value.
func checkInputNames(t reporter, c plugin.Capability) {
	t.Helper()

	for _, f := range c.Inputs {
		words := strings.Split(f.Name, "-")
		isTime := (f.Type == plugin.Int || f.Type == plugin.Float) && slices.Contains(timeWords, words[len(words)-1])
		switch {
		case isTime && !timeUnit.MatchString(f.Help):
			t.Errorf("sdktest: %s: %s input %q is a length of time given as a bare %s and does not say in what "+
				"unit: a model sends 30 to a field that wants milliseconds as readily as one that wants seconds. "+
				"Declare it plugin.Duration, which is written with its unit (30s, 5m), or say the unit in its help",
				RuleWording, c.ID, f.Name, f.Type)
		case isTime:
			t.Logf("sdktest: %s: %s input %q is a length of time given as a bare %s; declare it plugin.Duration so "+
				"the unit travels with the value and not in its help", RuleWording, c.ID, f.Name, f.Type)
		case slices.Contains(limitSynonyms, f.Name):
			t.Logf("sdktest: %s: %s input %q caps how many rows come back; rta spells that limit", RuleWording,
				c.ID, f.Name)
		}
	}
}
