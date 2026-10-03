package app

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/mcp"
)

// What a tool's words may say, and how many of them there are.
//
// A description is paid for by every session that lists the tools, whether or
// not it ever calls this one, and it is read by something that has no terminal
// and no pipe. Both limits are measured from the catalogue rather than chosen
// for roundness: of ninety-three tools the median plugin-written text is about
// four hundred bytes and the longest was nearly two thousand, all of it detail
// a person at a command line reads in `--help` and a model never acts on.

const (
	// authoredBudget bounds the plugin-written text of one tool: its summary
	// and its description, without rta's lines. About two hundred tokens.
	authoredBudget = 800
	// inputBudget bounds one input's description.
	inputBudget = 160
)

// terminalWording is how a description speaks to a person at a command line,
// which over MCP is nobody: a flag, an `rta ...` command, what a pipe does,
// what happens from a terminal. Where a capability behaves differently over
// MCP the text says that, and says it once.
var terminalWording = regexp.MustCompile("(?i)\\bfrom a terminal\\b|\\b(?:at|on) (?:a|the) terminal\\b|" +
	"\\bon the CLI\\b|\\bread from a pipe\\b|\\bpiping\\b|\\bshell history\\b|`rta |" +
	"\\bfull-page surface\\b|\\bdashboard\\b|\\btile\\b")

func TestNoToolSpeaksToAPersonAtACommandLine(t *testing.T) {
	for _, tl := range surface(t, mcp.Options{}) {
		texts := map[string]string{"description": authored(t, tl.Description)}
		props, _ := tl.Schema["properties"].(map[string]any)
		for name, raw := range props {
			p, _ := raw.(map[string]any)
			texts["input "+name], _ = p["description"].(string)
		}
		var found []string
		for where, text := range texts {
			if m := terminalWording.FindString(text); m != "" {
				found = append(found, fmt.Sprintf("%s says %q", where, m))
			}
		}
		if len(found) > 0 {
			t.Errorf("%s: %s", tl.Name, strings.Join(found, "; "))
		}
	}
}

// An argument a description names has to be one the tool takes. An input
// declared Local is the operator's alone and is in no schema, so a text telling
// a model to give `file` or `secret-file` sends it to an argument that is
// refused as a typo is.
func TestNoDescriptionNamesAnInputTheToolDoesNotTake(t *testing.T) {
	byName := capabilities(t)
	for _, tl := range surface(t, mcp.Options{}) {
		text := authored(t, tl.Description)
		var found []string
		for _, f := range byName[tl.Name].Inputs {
			if f.Local && strings.Contains(text, "`"+f.Name+"`") {
				found = append(found, f.Name)
			}
		}
		if len(found) > 0 {
			t.Errorf("%s: the description names %v, which an agent cannot give", tl.Name, found)
		}
	}
}

func TestToolTextStaysInBudget(t *testing.T) {
	for _, tl := range surface(t, mcp.Options{}) {
		var over []string
		if n := len(authored(t, tl.Description)); n > authoredBudget {
			over = append(over, fmt.Sprintf("the description is %d bytes, over the %d it is held to", n, authoredBudget))
		}
		props, _ := tl.Schema["properties"].(map[string]any)
		for name, raw := range props {
			p, _ := raw.(map[string]any)
			if desc, _ := p["description"].(string); len(desc) > inputBudget {
				over = append(over, fmt.Sprintf("input %s is %d bytes, over the %d it is held to", name, len(desc), inputBudget))
			}
		}
		if len(over) > 0 {
			t.Errorf("%s: %s", tl.Name, strings.Join(over, "; "))
		}
	}
}
