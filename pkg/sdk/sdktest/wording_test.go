package sdktest

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

func wordingOf(c plugin.Capability, opts ...Option) *recorder {
	cfg := noConfig()
	for _, o := range opts {
		o(&cfg)
	}
	rec := &recorder{}
	checkWording(rec, plugin.Plugin{Name: "demo", Summary: "Demo", Capabilities: []plugin.Capability{c}}, cfg)
	return rec
}

func tidy() plugin.Capability {
	c := ok()
	c.Summary = "List items"
	c.Description = "Returns the stored items, newest first."
	c.Inputs[0].Help = "how many items to return"
	return c
}

// Each rule below is shown to catch the plugin that breaks it: a conformance
// check nobody has watched fail passes everything.
func TestAPluginThatWordsItselfAsTheCatalogueDoesIsNotReported(t *testing.T) {
	rec := wordingOf(tidy())
	if len(rec.errs) > 0 || len(rec.logs) > 0 {
		t.Errorf("a well-worded capability was reported:\nerrors %q\nnotes %q", rec.errText(), rec.logText())
	}
}

func TestAnAgentTextThatSpeaksToAPersonAtATerminalIsRejected(t *testing.T) {
	for _, say := range []string{
		"Run it from a terminal and read the table.",
		"Prints the items; piping the result keeps only the names.",
		"Shown as a tile on the dashboard tiles page.",
		"Open the TUI to see more.",
	} {
		c := tidy()
		c.Description = say
		if rec := wordingOf(c); !strings.Contains(rec.errText(), "speaks to a person at a terminal") {
			t.Errorf("%q was accepted as what an agent reads: %q", say, rec.errText())
		}
	}

	// Description is read at a terminal too: once the agent has text of its
	// own, the Description is free to speak to the person, and the agent's is
	// what is held.
	c := tidy()
	c.Description = "Run it from a terminal and read the table."
	c.Agent = "Returns the stored items, newest first."
	if rec := wordingOf(c); len(rec.errs) > 0 {
		t.Errorf("a Description the agent never reads was held to what an agent reads:\n%s", rec.errText())
	}
	c.Agent = "Run it from a terminal and read the table."
	c.Description = "Returns the stored items, newest first."
	if rec := wordingOf(c); !strings.Contains(rec.errText(), "agent text says") {
		t.Errorf("an agent text speaking to a terminal was accepted: %q", rec.errText())
	}
}

func TestAnInputHelpThatSpeaksToATerminalIsRejectedOnlyWhereAnAgentReadsIt(t *testing.T) {
	c := tidy()
	c.Inputs[0].Help = "taken from a pipe when left out, piping is how scripts give it"
	if rec := wordingOf(c); !strings.Contains(rec.errText(), "help of limit says") {
		t.Errorf("terminal wording in an input's help was accepted: %q", rec.errText())
	}
	c.Inputs[0].Local = true
	if rec := wordingOf(c); len(rec.errs) > 0 {
		t.Errorf("a Local input's help is read at a terminal alone and was held to an agent's rule:\n%s", rec.errText())
	}
}

func TestAnAgentTextOverItsBudgetIsRejectedAndNamesTheWayOut(t *testing.T) {
	c := tidy()
	c.Description = strings.Repeat("Returns the stored items, newest first. ", 25)
	rec := wordingOf(c)
	for _, want := range []string{"over the 800", "Write an Agent text"} {
		if !strings.Contains(rec.errText(), want) {
			t.Errorf("an overlong description was accepted, or did not say %q:\n%s", want, rec.errText())
		}
	}

	// The same words are within budget once the long form is for the person
	// and the agent has a short one.
	c.Agent = "Returns the stored items, newest first."
	if rec := wordingOf(c); len(rec.errs) > 0 {
		t.Errorf("an Agent text standing in for a long Description was held to the Description:\n%s", rec.errText())
	}

	c = tidy()
	c.Inputs[0].Help = strings.Repeat("how many ", 20)
	if rec := wordingOf(c); !strings.Contains(rec.errText(), "over the 160") {
		t.Errorf("an overlong input help was accepted: %q", rec.errText())
	}
}

func TestADescriptionThatSaysItsSummaryAgainIsRejected(t *testing.T) {
	c := tidy()
	c.Description = "Lists items. Newest first."
	if rec := wordingOf(c); !strings.Contains(rec.errText(), "opens by saying its summary again") {
		t.Errorf("a description restating its summary was accepted: %q", rec.errText())
	}
	c.Agent = "Lists items."
	c.Description = "Returns the stored items, newest first."
	if rec := wordingOf(c); !strings.Contains(rec.errText(), "agent text opens by saying") {
		t.Errorf("an agent text restating its summary was accepted: %q", rec.errText())
	}
}

func TestAnAgentTextThatNamesAnInputAnAgentCannotGiveIsRejected(t *testing.T) {
	c := tidy()
	c.Inputs = append(c.Inputs, plugin.Field{Name: "out", Type: plugin.Path, Local: true, Help: "where to write it"})
	c.Description = "Returns the stored items; give `out` to write them to a file."
	if rec := wordingOf(c); !strings.Contains(rec.errText(), "names `out`, which is Local") {
		t.Errorf("an agent text sending a model to a Local input was accepted: %q", rec.errText())
	}
	c.Agent, c.Description = "Returns the stored items.", "Returns the stored items; give `out` to write them to a file."
	if rec := wordingOf(c); len(rec.errs) > 0 {
		t.Errorf("a Description the agent never reads was held to what an agent can give:\n%s", rec.errText())
	}
}

// A HumanOnly capability is never a tool: what an agent reads of it is
// nobody's, and so is its budget.
func TestHumanOnlyTextIsNotHeldToWhatAnAgentReads(t *testing.T) {
	c := tidy()
	c.HumanOnly = true
	c.Description = strings.Repeat("Run it from a terminal and read the table. ", 30)
	if rec := wordingOf(c); len(rec.errs) > 0 {
		t.Errorf("text no agent reads was held to an agent's rules:\n%s", rec.errText())
	}
}

func TestATimeThatIsANumberMustSayItsUnitAndIsAskedToBeADuration(t *testing.T) {
	c := tidy()
	c.Inputs = append(c.Inputs, plugin.Field{Name: "connect-timeout", Type: plugin.Int, Help: "give up after this"})
	rec := wordingOf(c)
	for _, want := range []string{`input "connect-timeout" is a length of time given as a bare int`, "does not say in what unit", "plugin.Duration"} {
		if !strings.Contains(rec.errText(), want) {
			t.Errorf("a unitless time was accepted, or the finding lacks %q:\n%s", want, rec.errText())
		}
	}

	c.Inputs[1].Help = "give up after this many seconds"
	rec = wordingOf(c)
	if len(rec.errs) > 0 {
		t.Errorf("a time that says its unit was rejected:\n%s", rec.errText())
	}
	if !strings.Contains(rec.logText(), "declare it plugin.Duration") {
		t.Errorf("a time that is a number was not asked to be a Duration: %q", rec.logText())
	}

	c.Inputs[1] = plugin.Field{Name: "connect-timeout", Type: plugin.Duration, Help: "give up after this", Default: "5s"}
	if rec := wordingOf(c); len(rec.errs) > 0 || len(rec.logs) > 0 {
		t.Errorf("a Duration was reported:\nerrors %q\nnotes %q", rec.errText(), rec.logText())
	}
}

func TestASummaryAndANameUnlikeTheCataloguesAreNotedNotRejected(t *testing.T) {
	c := tidy()
	c.Summary = "lists items."
	c.Inputs = append(c.Inputs, plugin.Field{Name: "max-results", Type: plugin.Int, Help: "most rows"})
	rec := wordingOf(c)
	if len(rec.errs) > 0 {
		t.Errorf("a style convention was made an error:\n%s", rec.errText())
	}
	for _, want := range []string{"starts with a lowercase letter", `input "max-results" caps how many rows`} {
		if !strings.Contains(rec.logText(), want) {
			t.Errorf("%q was not noted:\n%s", want, rec.logText())
		}
	}
	c.Summary = "Lists items."
	if rec := wordingOf(c); !strings.Contains(rec.logText(), "ends with a full stop") {
		t.Errorf("a summary ending in a full stop was not noted: %q", rec.logText())
	}
	c.Agent, c.Description = "Same words.", "Same words."
	if rec := wordingOf(c); !strings.Contains(rec.logText(), "identical to its Description") {
		t.Errorf("an Agent text that is the Description was not noted: %q", rec.logText())
	}
}

func TestAWordingSkipWaivesOnlyTheCapabilityItNames(t *testing.T) {
	c := tidy()
	c.Description = "Run it from a terminal and read the table."
	if rec := wordingOf(c); len(rec.errs) == 0 {
		t.Fatal("the fixture is not held")
	}
	rec := wordingOf(c, Skip(RuleWording, "demo.item.list", "quotes a terminal on purpose"))
	if len(rec.errs) > 0 {
		t.Errorf("a waived capability was held:\n%s", rec.errText())
	}
	if rec := wordingOf(c, Skip(RuleWording, "demo.other", "another capability")); len(rec.errs) == 0 {
		t.Error("a waiver for one capability reached another")
	}
}
