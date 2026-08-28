package kv

import (
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Reveal returns one entry's value, unlocking the store from the host's own
// environment and nothing else.
//
// It exists for one caller: the host resolving a profile's credential
// (internal/profile). An operator who writes
//
//	profiles:
//	  prod:
//	    secrets:
//	      password: kv:prod-db-password
//
// is saying "this connection's password is that entry" — the same statement
// ADR 0018 §6 already accepts for a Kubernetes Secret beside a service, and
// accepted for the same reason: **the operator writes the mapping and the
// plugin never does.** A plugin that could name the entry it wanted could name
// any entry in the store; here it declares only that it has a Secret input,
// and what fills it is written in the operator's own file. The mapping is an
// allowlist, and an entry nobody mapped is an entry nobody gets.
//
// Three properties, each deliberate.
//
// **It never prompts.** The request is built with no surface, so canPrompt is
// false by construction — a passphrase prompt in the middle of an MCP call is
// impossible and in the middle of a CLI capability run is a surprise. If the
// environment cannot open the store, this fails saying so and the connection
// fails with it. `kv.Unlockable()` answers the same question in advance, and
// `rta doctor` already asks it.
//
// **It is not `kv.get`, and does not go through its gate.** kv.get is
// NeedsGrant+Scope because it *returns a secret to a caller*; this returns it
// to the host, which puts it in a Secret-typed input — redacted on every
// surface, never rendered, never logged, and used only to authenticate. The
// widening is real and is worth naming plainly: an agent holding a grant for a
// profile can cause the entries that profile maps to be read. It cannot see
// them, and it cannot reach an entry the operator did not map into a profile.
//
// **The value is a string.** An entry holds []byte because kv stores
// certificates and keystores; a credential filling a declared input is text by
// the time a handler reads it, so the conversion happens here rather than
// leaving every caller to guess.
func Reveal(key string) (string, *view.Error) {
	if key == "" {
		return "", view.Errorf("kv.key.empty", "no entry named")
	}
	// No surface: this is the host resolving a value, not a person or an agent
	// asking for one. canPrompt requires SurfaceCLI, so nothing here can stop
	// to ask a question nobody is present to answer.
	req := plugin.NewRequest(map[string]any{
		"passphrase": os.Getenv(passphraseEnv),
		"identity":   os.Getenv(identityEnv),
	}, false, true)

	s, verr := load(req)
	if verr != nil {
		return "", verr
	}
	e, ok := s.Entries[key]
	if !ok {
		// Deliberately does not list what is there. This runs on behalf of a
		// call that may have come from an agent, and the entry names in an
		// operator's store are exactly what an agent has no business
		// enumerating. `rta kv list` answers it for the person who can.
		return "", view.Errorf("kv.notfound", "no entry %q in the store", key).
			WithHint("`rta kv list` shows what is there")
	}
	return string(e.Value), nil
}

// Names lists the entries in the store, sorted, unlocking from the host's own
// environment the way Reveal does.
//
// For one caller: the TUI's profile editor, offering the entries an operator
// could map a credential onto. Names and never values — the list is what makes
// "reference the one I already stored" a choice somebody can make rather than
// something they have to remember, and a screen is no place for the values
// behind them.
//
// A store that cannot be opened yields nothing rather than an error. The
// caller is drawing a picker, and a picker with no options is a correct answer
// to "which of your entries" when the answer is "none I can see from here".
func Names() []string {
	req := plugin.NewRequest(map[string]any{
		"passphrase": os.Getenv(passphraseEnv),
		"identity":   os.Getenv(identityEnv),
	}, false, true)
	s, verr := load(req)
	if verr != nil {
		return nil
	}
	out := make([]string, 0, len(s.Entries))
	for name := range s.Entries {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Store writes a value under a name, unlocking and re-sealing from the host's
// own environment.
//
// The other half of the TUI's credential action: an operator who has a
// password in their hand and no entry for it should not have to leave the
// screen, open a shell and run `rta kv set` to make a profile work. It refuses
// to overwrite, because "store this" and "replace what is there" are different
// intentions and only one of them was expressed.
func Store(name, value, description string) *view.Error {
	if name == "" {
		return view.Errorf("kv.key.empty", "no entry named")
	}
	req := plugin.NewRequest(map[string]any{
		"passphrase": os.Getenv(passphraseEnv),
		"identity":   os.Getenv(identityEnv),
	}, false, true)
	s, verr := load(req)
	if verr != nil {
		return verr
	}
	if _, exists := s.Entries[name]; exists {
		return view.Errorf("kv.exists", "%q is already in the store", name).
			WithHint("reference it instead, or pick another name — replacing a stored " +
				"secret is `rta kv set`, where the intention is explicit")
	}
	now := time.Now()
	s.Entries[name] = entry{
		Value: []byte(value), Description: description, Kind: detectKind(value, ""),
		Created: now, Updated: now,
	}
	return save(req, s)
}

// StoreStamp identifies the store's current contents without opening it.
//
// The other half of what a resolved credential depends on. A profile's
// `secrets:` block names an entry; Reveal turns that name into a value, and
// the value changes when somebody rewrites the entry — which is an ordinary
// thing to do in the middle of a session, and exactly what an operator does
// after rebuilding a plugin. A host caching a resolved connection has to be
// able to notice, or it holds a credential the store has since replaced and
// fails to authenticate with no visible reason.
//
// Modification time and size, not a digest of the bytes: this is asked on a
// timer, the store is a file rta wrote atomically so its mtime moves on every
// write, and hashing an encrypted blob on a five-second tick to detect a
// change rta could see from a stat is work for nothing. It cannot be defeated
// by rta's own writes, which is what it is for; it can be defeated by
// restoring an identical-length file with a preserved timestamp, which is not
// a case a cache-invalidation stamp needs to defend against.
//
// A store that is not there yields the empty string, which is stable — no
// store and no entry are the same input to a resolution.
func StoreStamp() string {
	info, err := os.Stat(storePath())
	if err != nil {
		return ""
	}
	return strconv.FormatInt(info.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(info.Size(), 10)
}
