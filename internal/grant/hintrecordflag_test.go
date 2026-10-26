package grant

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The record in a refusal is chosen by the agent that was refused, and the
// hint puts it in a command a person pastes. A record that opens on a dash read
// as a flag there: `grant allow kv.get --agent=other --agent claude --ttl 15m`
// grants kv.get on every record, not the one that was refused, and a key of
// --note=text put the agent's words in the grant's note. The flags go first and
// `--` ends them, so whatever follows is the target and the record, and nothing
// else.
func TestTheHintKeepsARecordThatReadsAsAFlagOutOfTheFlags(t *testing.T) {
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	for _, record := range []string{"--agent=other", "--note=injected", "--help", "-x", "-5"} {
		_, verr := Reserve(c, map[string]any{"key": record}, Caller{Agent: "claude"})
		if verr == nil || verr.Code != "core.grant.required" {
			t.Fatalf("%q: went through ungranted: %v", record, verr)
		}
		want := "grant allow --agent claude --ttl 15m -- kv.get " + record + "`"
		if !strings.Contains(verr.Hint, want) {
			t.Errorf("%q: hint = %q, want the flags first, then --, then the target and the record: %q", record, verr.Hint, want)
		}
	}
	// A record that does not open on a dash keeps the order it always had.
	_, verr := Reserve(c, map[string]any{"key": "db-password"}, Caller{Agent: "claude"})
	if verr == nil || !strings.Contains(verr.Hint, "grant allow kv.get db-password --agent claude --ttl 15m`") {
		t.Errorf("an ordinary record's hint changed: %v", verr)
	}
}

// The profile is as much the agent's as the record is: it is an argument of
// its call, and the hint printed it raw. A name with a semicolon in it made
// the command a person pastes two commands, the second the agent's.
func TestTheHintQuotesAProfileNameTheAgentChose(t *testing.T) {
	setup(t)
	hostile := "x; touch /tmp/pwned #"
	verr := gate(t, readCap(), map[string]any{"sql": "select 1"}, hostile, "")
	if verr == nil || verr.Code != "core.grant.required" {
		t.Fatalf("a read against a named connection went through ungranted: %v", verr)
	}
	if want := "--profile 'x; touch /tmp/pwned #'"; !strings.Contains(verr.Hint, want) {
		t.Errorf("hint = %q, want the profile as one quoted word: %s", verr.Hint, want)
	}
}

// A record in angle brackets read as a usage line's placeholder and was
// printed bare, and bare it is two redirections: pasted in a repository
// holding ./src, `grant allow kv.get <src> --agent claude --ttl 15m` read
// stdin from src, truncated a file named --agent, and granted kv.get on the
// record "claude" to every agent. A record is the agent's, and is quoted
// whatever it looks like.
func TestTheHintQuotesARecordThatLooksLikeAPlaceholder(t *testing.T) {
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	for _, record := range []string{"<src>", "<file>", "<a-b>"} {
		_, verr := Reserve(c, map[string]any{"key": record}, Caller{Agent: "claude"})
		if verr == nil || verr.Code != "core.grant.required" {
			t.Fatalf("%q: went through ungranted: %v", record, verr)
		}
		want := "grant allow kv.get '" + record + "' --agent claude --ttl 15m`"
		if !strings.Contains(verr.Hint, want) {
			t.Errorf("%q: hint = %q, want the record as one quoted word: %q", record, verr.Hint, want)
		}
	}
}
