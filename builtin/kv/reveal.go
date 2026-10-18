package kv

import (
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
// is already accepted for a Kubernetes Secret beside a service, and
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
//
// What it refuses with is worded for a reader who has no terminal — the
// command is handed on as the operator's to run — because this entry point
// cannot tell who is asking. The caller that can, a person at a terminal or in
// the TUI, says so with RevealAs.
func Reveal(key string) (string, *view.Error) { return reveal(plugin.SurfaceMCP, key) }

// RevealAs is Reveal for a caller that knows who reads its refusal: the CLI
// and the TUI, where it is the person at the terminal, who is the operator and
// has nobody to ask. "Ask the operator to run `rta kv set staging-db-password`"
// read to them as an instruction for somebody else, in a word `rta operator`
// also uses, for whoever manages a remote server.
//
// Nothing else differs, and an agent's call never reaches it: the MCP bridge
// answers a refused credential with a refusal that names neither the entry nor
// the store, and keeps the real one on the record for the person. The default
// Reveal is the more cautious wording all the same, so a caller that forgets
// to say who it is gets the one safe to read from either side.
func RevealAs(sf plugin.Surface) func(key string) (string, *view.Error) {
	return func(key string) (string, *view.Error) { return reveal(sf, key) }
}

// RevealForServer is Reveal for `rta mcp serve`, whose refusals the operator
// reads and an agent never does: the bridge answers the agent with a sentence
// that names neither the entry nor the store, and keeps this one for the
// record's note (internal/mcp). Reveal's wording hands a command on as "ask the
// operator to run" because it cannot tell who is asking, and on that row it
// told the operator to ask themselves.
//
// What the operator needs differs from a person at a terminal in one thing: the
// passphrase or the key has to reach the environment the server was started in,
// which is not the shell they are typing in, and is the commonest reason the
// refusal is on the record at all. The commands are spelled as they type them,
// the CLI's own, and the two refusals that name an environment name that one.
func RevealForServer(key string) (string, *view.Error) {
	value, verr := reveal(plugin.SurfaceCLI, key)
	if verr == nil {
		return value, nil
	}
	const where = " in the environment rta mcp serve runs in"
	out := *verr
	switch verr.Code {
	case "kv.passphrase.missing":
		out.Hint = "set " + passphraseEnv + where
	case "kv.identity.required":
		out.Hint = "set " + identityEnv + " to the private key of one of the store's recipients," + where +
			" — " + plugin.SurfaceCLI.CapabilityName("kv.recipients") + " lists who can read it"
	}
	return "", &out
}

func reveal(sf plugin.Surface, key string) (string, *view.Error) {
	if key == "" {
		return "", view.Errorf("kv.key.empty", "no entry named")
	}
	// No surface: this is the host resolving a value, not a person or an agent
	// asking for one. canPrompt requires SurfaceCLI, so nothing here can stop
	// to ask a question nobody is present to answer.
	req := plugin.NewRequest(map[string]any{
		"passphrase": hostPassphrase(),
		"identity":   os.Getenv(identityEnv),
	}, false, true)

	s, verr := load(req)
	if verr != nil {
		return "", hostOpenHint(sf, verr)
	}
	e, ok := s.Entries[key]
	if !ok {
		// Deliberately does not list what is there. This runs on behalf of a
		// call that may have come from an agent, and the entry names in an
		// operator's store are exactly what an agent has no business
		// enumerating. `rta kv list` answers it for the person who can, and the
		// hint hands it on as theirs to run, since whoever made the call may be
		// an agent with no terminal.
		if !fileExists(storePath()) {
			// A profile maps a credential onto an entry on a machine that has no
			// store at all — a config copied from another machine is the commonest
			// way here. "In the store" says one exists, and the `kv list` the hint
			// would then send a person to prints an empty table; what is missing
			// is the store, and one `kv set` makes it and the entry together. Says
			// nothing an agent could not already read off the profile it was
			// refused on: whether a store exists is not what the comment above
			// keeps from it, which is what the entries in it are called.
			return "", view.Errorf("kv.notfound", "no entry %s: there is no store on this machine yet", textclean.Record(key)).
				WithHint(makeStoreHint(sf, key))
		}
		return "", view.Errorf("kv.notfound", "no entry %s in the store", textclean.Record(key)).
			WithHint(listEntriesHint(sf))
	}
	return string(e.Value), nil
}

// hostOpenHint is the refusal load gave to open the store, with the passphrase
// hint worded for what this open can actually be given.
//
// load words "no passphrase provided" for a call that has a flag — "set
// RTA_KV_PASSPHRASE or give --passphrase" — and the request the host reads
// with carries none: it is built from the environment alone (hostPassphrase)
// and never prompts. A person who ran `rta pg query --profile staging` was told
// to give a flag that command does not have. What opens the store here is the
// variable, in the environment of whatever resolves the credential: this
// process at a terminal, and over MCP the server's own, which is the one an
// operator is not looking at.
//
// The same goes for a store locked to keys: load's hint says to give a flag, and
// the host's read has none. It is rebuilt here for the reader on sf, which also
// keeps the path of a generated key out of the default reader's refusal, the one
// an agent could be handed.
func hostOpenHint(sf plugin.Surface, verr *view.Error) *view.Error {
	out := *verr
	switch verr.Code {
	case "kv.passphrase.missing":
		switch sf {
		case plugin.SurfaceMCP:
			out.Hint = passphraseHint(plugin.SurfaceMCP)
		case plugin.SurfaceTUI:
			out.Hint = "press U to unlock the store for this session — or set " + passphraseEnv + " before starting rta"
		default:
			out.Hint = "set " + passphraseEnv + " — a profile's credential is read without asking for the passphrase"
		}
	case "kv.identity.locked":
		if sf != plugin.SurfaceTUI {
			return verr
		}
		out.Hint = "press U to unlock the key for this session — or set " + passphraseEnv +
			" to its passphrase before starting rta, or add a key that needs none: " + sf.CapabilityWith("kv.rekey", "generate")
	case "kv.identity.required":
		specs, _ := loadRecipients()
		out.Hint = hostIdentity(sf, specs) + " — " + sf.CapabilityName("kv.recipients") + " lists who can read it"
	default:
		return verr
	}
	return &out
}

// makeStoreHint is the command that makes the store and the entry a profile
// maps a credential onto, as the reader on sf runs it: handed on as the
// operator's over MCP, and the reader's own anywhere else.
//
// The key is the command's argument, so it is spelled as a command spells one
// (Surface.Call), and over MCP as the record is drawn but for the name Record
// puts beside a host: that is a note to read, and typed it would be part of
// the key.
func makeStoreHint(sf plugin.Surface, key string) string {
	const result = "creates the store, locked with a passphrase you choose, and the entry"
	if sf == plugin.SurfaceMCP {
		return plugin.AskOperator("kv set "+textclean.WithoutNote(textclean.Record(key))) + " — it " + result
	}
	return "`" + sf.Call("kv.set", keyArg(key)) + "` " + result
}

// listEntriesHint is where to see what the store holds, as the reader on sf
// reaches it.
func listEntriesHint(sf plugin.Surface) string {
	if sf == plugin.SurfaceMCP {
		return plugin.AskOperator("kv list") + " to see what is there"
	}
	return sf.CapabilityName("kv.list") + " lists every key"
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
		"passphrase": hostPassphrase(),
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

// Lacking is the entries among keys that the store does not hold, opened the
// way a profile's credential is: from the host's own environment, never
// prompting, and answering with names the caller already has.
//
// For one caller, `rta doctor`, which asks it of the entries the profiles map
// so that a profile whose first call would fail on an entry nobody made says
// so before the call does. It is asked only when this environment can open the
// store (Unlockable), because opening one costs a third of a second of key
// derivation and a quarter of a gigabyte, and nothing that runs on a timer
// asks it.
//
// The error is the store's own refusal to open, which is not the same news as
// the entries being absent: a RTA_KV_PASSPHRASE that does not fit the store
// leaves every entry unread, and "none of them are there" would be a false
// answer to it.
//
// It changes nothing, the TUI's store session included. Doctor runs in the
// TUI's own process, and a failed open ends the session for every use of the
// store (wrongKey); this one is a question about the environment, not a use,
// and what it tried was the environment's passphrase, which a session never
// stood behind.
func Lacking(keys []string) ([]string, *view.Error) {
	req := plugin.NewRequest(map[string]any{
		"passphrase": hostPassphrase(),
		"identity":   os.Getenv(identityEnv),
	}, false, true)
	s, verr := open(req, false)
	if verr != nil {
		return nil, verr
	}
	var lacking []string
	for _, key := range keys {
		if _, held := s.Entries[key]; !held {
			lacking = append(lacking, key)
		}
	}
	return lacking, nil
}

// Store writes a value under a name, unlocking and re-sealing from the host's
// own environment.
//
// The other half of the TUI's credential action: an operator who has a
// password in their hand and no entry for it should not have to leave the
// screen, open a shell and run `rta kv set` to make a profile work. It refuses
// to overwrite, because "store this" and "replace what is there" are different
// intentions and only one of them was expressed.
func Store(name, value, description, origin string) *view.Error {
	if name == "" {
		return view.Errorf("kv.key.empty", "no entry named")
	}
	req := plugin.NewRequest(map[string]any{
		"passphrase": hostPassphrase(),
		"identity":   os.Getenv(identityEnv),
	}, false, true)
	// **Under the store lock, like every other writer.** This is a
	// load-modify-save over the whole store: it decrypts everything, adds one
	// entry, and re-encrypts everything. Two of those interleaving means the
	// later save writes a snapshot taken before the earlier one landed, and
	// the earlier entry is gone — with both callers told they succeeded.
	//
	// It is not a theoretical interleave. This is the TUI's credential
	// action, and the process that runs it is also serving MCP: an agent's
	// `kv.set` takes the lock, this took nothing, and a probe lost a write on
	// 20 runs out of 20. Every other write path in this package — set, rm,
	// rename, edit, rekey — already holds it; this one was written later and
	// separately, which is exactly how a discipline that lives in each caller
	// rather than in the store gets missed. This shape has been fixed once already.
	unlock, verr := lockStore()
	if verr != nil {
		return verr
	}
	defer unlock()
	s, verr := load(req)
	if verr != nil {
		return verr
	}
	if _, exists := s.Entries[name]; exists {
		// Worded for the TUI's credential action, Store's one caller.
		return view.Errorf("kv.exists", "%s is already in the store", textclean.Record(name)).
			WithHint("reference it instead, or pick another name — replacing a stored " +
				"secret is " + plugin.SurfaceTUI.CapabilityName("kv.set") + ", where the intention is explicit")
	}
	now := time.Now()
	s.Entries[name] = entry{
		Value: []byte(value), Description: description, Kind: detectKind(value, ""),
		Origin: origin, Created: now, Updated: now,
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
	stamp := strconv.FormatInt(info.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(info.Size(), 10)
	// Whether a session holds the store open is part of what a resolution
	// depends on: a bind made while it was shut cached the refusal, and the
	// store file did not change when the person unlocked it, so without this the
	// tiles kept their error until something else moved. Looking does not
	// extend the session, and its lapse changes the stamp too, so the tiles
	// relock on the tick after it ends.
	if _, open := peekSession(); open {
		stamp += ":unlocked"
	}
	return stamp
}
