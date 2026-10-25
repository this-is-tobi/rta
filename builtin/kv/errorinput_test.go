package kv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// lockedToAKey makes the store, locked to an SSH key of its own, and returns
// the key's path.
func lockedToAKey(t *testing.T) string {
	t.Helper()
	private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
	text(t, runSet, map[string]any{"key": "k", "value": "v", "identity": private}, false)
	return private
}

// fileHolding writes body to a file nothing else uses and returns its path.
func fileHolding(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func capabilityOf(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("%s is not a kv capability", id)
	return plugin.Capability{}
}

// A refusal of one input names it (view.Error.Input), by the name the
// capability that refused it declares: the TUI opens the form again on that
// box, and an agent is told which argument to send again. A name the
// capability does not declare is ignored by every surface, so each one here is
// also held to the declaration (ErrorInput).
//
// Beside them, the refusals that name none on purpose: none of several inputs
// given, two inputs at odds, a key against what the store holds of it. No one
// box holds the fix to those, and a form reopened on one of them would point at
// the wrong half.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cap     string
		surface plugin.Surface
		given   func(t *testing.T) map[string]any
		code    string
		input   string
	}{
		{name: "a key the store does not hold", cap: "kv.get", code: "kv.notfound", input: "key",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				return map[string]any{"key": "absent"}
			}},
		{name: "one of the keys an export names", cap: "kv.env", code: "kv.notfound", input: "key",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				return map[string]any{"key": []string{"k", "absent"}}
			}},
		{name: "a file the value cannot be written to", cap: "kv.get", code: "kv.out.unwritable", input: "out",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				return map[string]any{"key": "k", "out": filepath.Join(fileHolding(t, "blocker", "x"), "secret")}
			}},
		{name: "a format with no syntax", cap: "kv.env", code: "kv.env.badformat", input: "format",
			given: func(*testing.T) map[string]any { return map[string]any{"format": "yaml"} }},
		{name: "an empty key", cap: "kv.set", code: "kv.set.nokey", input: "key",
			given: func(*testing.T) map[string]any { return map[string]any{"key": "", "value": "v"} }},
		{name: "a key with white space around it", cap: "kv.set", code: "kv.set.padded", input: "key",
			given: func(*testing.T) map[string]any { return map[string]any{"key": " k", "value": "v"} }},
		{name: "a key that names a folder", cap: "kv.set", code: "kv.set.foldername", input: "key",
			given: func(*testing.T) map[string]any { return map[string]any{"key": "db/", "value": "v"} }},
		{name: "a kind there is none of", cap: "kv.set", code: "kv.set.badkind", input: "kind",
			given: func(*testing.T) map[string]any {
				return map[string]any{"key": "k", "value": "v", "kind": "nonsense"}
			}},
		{name: "a file the value cannot be read from", cap: "kv.set", code: "kv.file.unreadable", input: "file",
			given: func(t *testing.T) map[string]any {
				return map[string]any{"key": "k", "file": filepath.Join(t.TempDir(), "absent")}
			}},
		{name: "a new name that is the key's own", cap: "kv.rename", code: "kv.rename.samename", input: "new-name",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				return map[string]any{"key": "k", "new-name": "k"}
			}},
		{name: "a new name with white space around it", cap: "kv.rename", code: "kv.set.padded", input: "new-name",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				return map[string]any{"key": "k", "new-name": "k2 "}
			}},
		{name: "a new name that names a folder", cap: "kv.rename", code: "kv.set.foldername", input: "new-name",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				return map[string]any{"key": "k", "new-name": "db/"}
			}},
		{name: "a new name another entry has", cap: "kv.rename", code: "kv.rename.taken", input: "new-name",
			given: func(t *testing.T) map[string]any {
				set(t, "a", "1")
				set(t, "b", "2")
				return map[string]any{"key": "a", "new-name": "b"}
			}},
		{name: "a revision the key does not have", cap: "kv.restore", code: "kv.restore.norevision", input: "revision",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				return map[string]any{"key": "k", "revision": 3}
			}},
		{name: "a recipient a re-key cannot read", cap: "kv.rekey", code: "kv.recipient.invalid", input: "recipient",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				return map[string]any{"recipient": []string{"definitely not a key"}}
			}},
		{name: "a recipient the first lock cannot read", cap: "kv.init", code: "kv.recipient.invalid", input: "recipient",
			given: func(t *testing.T) map[string]any {
				private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
				return map[string]any{"identity": private, "recipient": []string{"definitely not a key"}}
			}},
		{name: "a passphrase too short to make a store with", cap: "kv.set", code: "kv.passphrase.short", input: "passphrase",
			given: func(*testing.T) map[string]any {
				return map[string]any{"key": "k", "value": "v", "passphrase": "short"}
			}},
		// Over MCP the refusal is worded again for the operator, and a copy made
		// to change its hint keeps what it is about.
		{name: "that passphrase, refused to an agent", cap: "kv.set", surface: plugin.SurfaceMCP,
			code: "kv.passphrase.short", input: "passphrase",
			given: func(*testing.T) map[string]any {
				return map[string]any{"key": "k", "value": "v", "passphrase": "short"}
			}},
		{name: "a passphrase that does not open the store", cap: "kv.list", code: "kv.wrongpass", input: "passphrase",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				return map[string]any{"passphrase": "not the passphrase"}
			}},
		{name: "no passphrase at all", cap: "kv.list", code: "kv.passphrase.missing", input: "passphrase",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				prompted = ""
				t.Cleanup(func() { prompted = "" })
				return map[string]any{"passphrase": ""}
			}},
		{name: "an identity that cannot be read", cap: "kv.list", code: "kv.identity.unreadable", input: "identity",
			given: func(t *testing.T) map[string]any {
				lockedToAKey(t)
				return map[string]any{"identity": filepath.Join(t.TempDir(), "absent")}
			}},
		{name: "an identity that is no key", cap: "kv.list", code: "kv.identity.invalid", input: "identity",
			given: func(t *testing.T) map[string]any {
				lockedToAKey(t)
				return map[string]any{"identity": fileHolding(t, "not-a-key", "hello\n")}
			}},
		{name: "an age identity line that does not parse", cap: "kv.list", code: "kv.identity.invalid", input: "identity",
			given: func(t *testing.T) map[string]any {
				lockedToAKey(t)
				return map[string]any{"identity": fileHolding(t, "bad-age-key", "AGE-SECRET-KEY-1NOTAKEY\n")}
			}},
		{name: "a key that cannot decrypt the store", cap: "kv.list", code: "kv.wrongkey", input: "identity",
			given: func(t *testing.T) map[string]any {
				lockedToAKey(t)
				other, _ := writeSSHKeypair(t, t.TempDir(), "id_other")
				return map[string]any{"identity": other}
			}},
		{name: "no identity for a store locked to keys", cap: "kv.get", code: "kv.identity.required", input: "identity",
			given: func(t *testing.T) map[string]any {
				lockedToAKey(t)
				return map[string]any{"key": "k"}
			}},
		{name: "an identity for a store locked with a passphrase", cap: "kv.set", code: "kv.identity.wrongmode", input: "identity",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
				return map[string]any{"key": "k2", "value": "v", "identity": private}
			}},
		{name: "the wrong passphrase for a locked key", cap: "kv.list", code: "kv.identity.locked", input: "passphrase",
			given: func(t *testing.T) map[string]any {
				locked := writeLockedSSHKey(t, t.TempDir(), "id_locked", "correct horse")
				text(t, runSet, map[string]any{
					"key": "k", "value": "v", "identity": locked, "passphrase": "correct horse",
				}, false)
				return map[string]any{"identity": locked, "passphrase": "not the key's"}
			}},

		{name: "neither a value nor a label", cap: "kv.set", code: "kv.set.novalue",
			given: func(*testing.T) map[string]any { return map[string]any{"key": "k"} }},
		{name: "a label for a key the store does not hold", cap: "kv.set", code: "kv.set.unknown",
			given: func(t *testing.T) map[string]any {
				set(t, "other", "v")
				return map[string]any{"key": "absent", "description": "for the deploy"}
			}},
		{name: "a rename missing one of its names", cap: "kv.rename", code: "kv.rename.noname",
			given: func(*testing.T) map[string]any { return map[string]any{"key": "k", "new-name": ""} }},
		{name: "a key to make and a key to use", cap: "kv.init", code: "kv.init.bothkeys",
			given: func(t *testing.T) map[string]any {
				private, _ := writeSSHKeypair(t, t.TempDir(), "id_ed25519")
				return map[string]any{"generate": true, "identity": private}
			}},
		{name: "a restore of a key that is live", cap: "kv.restore", code: "kv.restore.live",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "v")
				return map[string]any{"key": "k"}
			}},
		{name: "a revision of a key that is removed", cap: "kv.restore", code: "kv.restore.removed",
			given: func(t *testing.T) map[string]any {
				set(t, "k", "one")
				set(t, "k", "two")
				text(t, runRemove, map[string]any{"key": "k"}, false)
				return map[string]any{"key": "k", "revision": 1}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			c := capabilityOf(t, tc.cap)
			r := req(tc.given(t), false)
			if tc.surface != "" {
				r = r.WithSurface(tc.surface)
			}
			_, err := c.Run(context.Background(), r)
			verr := view.AsError(err, "kv.test.none")
			if verr == nil || verr.Code != tc.code {
				t.Fatalf("%s answered %+v, want %s", tc.cap, verr, tc.code)
			}
			if verr.Input != tc.input {
				t.Errorf("%s: %s names the input %q, want %q", tc.cap, tc.code, verr.Input, tc.input)
			}
			if _, declared := c.ErrorInput(verr); tc.input != "" && !declared {
				t.Errorf("%s: %s names %q, which it does not declare", tc.cap, tc.code, verr.Input)
			}
		})
	}
}

// The unlock the person asks for in the TUI hands StartSession its passphrase
// box, and an empty one is refused naming it, as every other open of the store
// names the passphrase it was not given. The length is the unlock form's own
// box, named by the form where it calls this: nothing here knows what the form
// calls it.
func TestStartSessionNamesThePassphraseAndLeavesTheLengthToItsCaller(t *testing.T) {
	setup(t)
	if verr := StartSession("", time.Hour); verr == nil || verr.Code != "kv.passphrase.missing" || verr.Input != "passphrase" {
		t.Errorf("an empty passphrase: %+v, want kv.passphrase.missing naming the passphrase", verr)
	}
	if verr := StartSession(aPassphrase, time.Second); verr == nil || verr.Code != "kv.session.ttl" || verr.Input != "" {
		t.Errorf("a length out of bounds: %+v, want kv.session.ttl naming no input", verr)
	}
}

// Over MCP an agent is told the argument a refusal is about when its schema has
// it, and never when it is Local: the key it asked for is its own to send again,
// while the identity the server unlocks with is the operator's, and naming it
// would tell the agent it exists.
func TestAnAgentIsToldTheKeyItAskedForAndNeverTheIdentity(t *testing.T) {
	setup(t)
	private := lockedToAKey(t)
	t.Setenv(identityEnv, private)
	session := mcpSession(t, mcp.Options{})
	told := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		res := callTool(t, session, tool, args)
		if !res.IsError {
			t.Fatalf("%s %v was answered, not refused", tool, args)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].(*sdk.TextContent).Text), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	if m := told("kv_show", map[string]any{"key": "absent"}); m["code"] != "kv.notfound" || m["input"] != "key" {
		t.Errorf("an absent key, to an agent: %v, want kv.notfound naming key", m)
	}
	t.Setenv(identityEnv, filepath.Join(t.TempDir(), "absent"))
	m := told("kv_list", map[string]any{})
	if _, named := m["input"]; m["code"] != "kv.identity.unreadable" || named {
		t.Errorf("an identity the server cannot read, to an agent: %v, want kv.identity.unreadable naming nothing", m)
	}
}

// A file a value or an identity is read from that is there and that this
// process may not read names no input: the path is right, and the fix is a
// chmod, not the box.
func TestAFileThatCannotBeReadNamesNoInput(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mode bits do not stop this reader")
	}
	shut := fileHolding(t, "shut", "value\n")
	if err := os.Chmod(shut, 0o000); err != nil {
		t.Fatal(err)
	}
	req := plugin.NewRequest(map[string]any{"file": shut}, false, false)
	if _, _, _, _, err := valueToStore(req); err == nil {
		t.Fatal("a value was read from a file this process may not read")
	} else if verr := view.AsError(err, "x"); verr.Code != "kv.file.unreadable" || verr.Input != "" {
		t.Errorf("the value's file: %s naming %q, want kv.file.unreadable naming none", verr.Code, verr.Input)
	}
	if _, verr := parseIdentities(req, shut); verr == nil || verr.Code != "kv.identity.unreadable" || verr.Input != "" {
		t.Errorf("the identity: %+v, want kv.identity.unreadable naming none", verr)
	}
}
