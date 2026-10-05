package sdktest

import (
	"strings"
	"unicode"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/spelling"
)

// checkAgentWords holds the words an agent is shown for one capability: its
// summary and agent text (Agent, or Description when there is none), and the
// help of each input it can give.
//
// What an agent can give is every input that is not Local. A Local one is the
// operator's alone: it is in no schema, and an agent text that names it sends
// the model to an argument the bridge refuses as a typo.
func checkAgentWords(t reporter, c plugin.Capability) {
	t.Helper()

	where := "description"
	if c.Agent != "" {
		where = "agent text"
	}
	text := c.AgentText()

	n := len(c.Summary)
	if text != "" {
		n += len("\n\n") + len(text)
	}
	if n > agentTextBudget {
		t.Errorf("sdktest: %s: %s tells an agent %d bytes in its summary and %s, over the %d it is held to; an "+
			"agent reads all of it on every connection before it has decided to call anything. Write an Agent "+
			"text for the model — what it returns and what its inputs mean, nothing its schema already says — "+
			"and keep the long form in Description for the person at a terminal",
			RuleWording, c.ID, n, where, agentTextBudget)
	}
	if m := spelling.TerminalWording(text); m != "" {
		t.Errorf("sdktest: %s: %s %s says %q, which speaks to a person at a terminal; an agent has a tool list "+
			"and no terminal. Say what holds on every surface, or word the agent's version apart from the "+
			"Description in Agent", RuleWording, c.ID, where, m)
	}
	for _, f := range c.Inputs {
		if f.Local {
			if strings.Contains(text, "`"+f.Name+"`") {
				t.Errorf("sdktest: %s: %s %s names `%s`, which is Local: an agent's schema does not have it and "+
					"the bridge drops it unread", RuleWording, c.ID, where, f.Name)
			}
			continue
		}
		if m := spelling.TerminalWording(f.Help); m != "" {
			t.Errorf("sdktest: %s: %s help of %s says %q, which speaks to a person at a terminal; an agent "+
				"reads this help in its schema", RuleWording, c.ID, f.Name, m)
		}
		if len(f.Help) > inputHelpBudget {
			t.Errorf("sdktest: %s: %s help of %s is %d bytes, over the %d an input's is held to; an agent reads "+
				"it with every other input on every connection", RuleWording, c.ID, f.Name, len(f.Help), inputHelpBudget)
		}
	}
	for _, d := range []struct{ where, text string }{{"description", c.Description}, {"agent text", c.Agent}} {
		if first, restates := restatesSummary(c.Summary, d.text); restates {
			t.Errorf("sdktest: %s: %s %s opens by saying its summary again — %q under %q; the summary is shown "+
				"on the line above it, so start with what it adds", RuleWording, c.ID, d.where, first, c.Summary)
		}
	}
	if c.Agent != "" && c.Agent == c.Description {
		t.Logf("sdktest: %s: %s declares an Agent text identical to its Description; leave Agent unset and "+
			"Description serves both readers", RuleWording, c.ID)
	}
}

// restatesSummary reports whether the first sentence of text says the summary
// again: nearly every word of it is one of the summary's, and it is not much
// longer. "Reveals a stored value" under "Reveal a stored value" is a model
// reading the same thing twice, once for every tool that does it.
func restatesSummary(summary, text string) (first string, restates bool) {
	if text == "" {
		return "", false
	}
	first, _, _ = strings.Cut(text, ". ")
	sentence, from := stems(first), stems(summary)
	shared := 0
	for w := range sentence {
		if from[w] {
			shared++
		}
	}
	restates = len(sentence) > 0 && len(sentence) <= len(from)+3 && shared*5 >= len(sentence)*4
	return first, restates
}

// stems is the set of a text's words with a trailing plural taken off, so
// "values" and "value" are one word.
func stems(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		out[strings.TrimSuffix(strings.TrimSuffix(w, "es"), "s")] = true
	}
	return out
}
