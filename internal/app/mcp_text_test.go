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
	"\\bon the CLI\\b|\\bread from a pipe\\b|\\bpiping\\b|\\bshell history\\b|`rta ")

// pending is the tools still breaking a rule below, by rule. It exists so the
// rules could land before the text they hold was fixed, one namespace at a
// time; a tool leaves it in the commit that fixes it, and a tool listed that no
// longer breaks its rule is an error, so the list cannot outlive the debt.
var pending = map[string]map[string]bool{
	"wording": {
		"audit_deps":   true,
		"audit_why":    true,
		"cert_chain":   true,
		"cert_inspect": true,
		"cert_pem":     true,
		"cert_tls":     true,
		"codec_b64":    true,
		"codec_jwk":    true,
		"codec_jwt":    true,
		"debug_ansi":   true,
		"kv_get":       true,
		"kv_rename":    true,
		"kv_set":       true,
	},
	"local": {
		"cert_pem":  true,
		"codec_jwt": true,
		"kv_get":    true,
		"kv_init":   true,
		"kv_rekey":  true,
		"kv_set":    true,
		"net_dns":   true,
	},
	"budget": {
		"audit_deps":             true,
		"audit_kube_eol":         true,
		"audit_kube_podsecurity": true,
		"audit_mail":             true,
		"audit_web":              true,
		"audit_why":              true,
		"cert_pem":               true,
		"codec_jwt":              true,
		"debug_ansi":             true,
		"keys_list":              true,
		"kv_get":                 true,
		"kv_rekey":               true,
		"kv_rename":              true,
		"kv_set":                 true,
		"net_listen":             true,
	},
}

func hold(t *testing.T, rule, tool string, broken bool, format string, args ...any) {
	t.Helper()
	switch {
	case broken && !pending[rule][tool]:
		t.Errorf("%s: %s", tool, fmt.Sprintf(format, args...))
	case !broken && pending[rule][tool]:
		t.Errorf("%s no longer breaks the %q rule: take it off the pending list", tool, rule)
	}
}

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
		hold(t, "wording", tl.Name, len(found) > 0, "%s", strings.Join(found, "; "))
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
		hold(t, "local", tl.Name, len(found) > 0, "the description names %v, which an agent cannot give", found)
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
		hold(t, "budget", tl.Name, len(over) > 0, "%s", strings.Join(over, "; "))
	}
}
