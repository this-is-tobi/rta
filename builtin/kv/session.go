package kv

import (
	"os"
	"sync"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A store session is the passphrase the TUI's masked unlock form was last
// answered with, kept in this process's memory for a while so the next kv
// action in the same TUI does not ask for it again, and so a profile's
// `secrets:` reference can be filled on the update loop, where nothing can
// stop to ask.
//
// It is the one place a kv unlock outlives the call that made it, and the
// reasons it is allowed to are exactly the reasons it is allowed nowhere
// else:
//
//   - **Only the TUI starts one.** A CLI command is one process per command,
//     so `prompted` (store.go) already covers "one command, one prompt" and a
//     session would die with the process anyway. An MCP server has nobody
//     at the other end to type anything, and its unlock is the environment
//     it inherited — which `rta doctor` already warns about. The TUI is a
//     person, at a terminal, in one long-lived process that is theirs.
//   - **Process memory only.** Nothing is written and no environment
//     variable is set, so no child the TUI ever starts inherits it — plugin
//     processes get the pluginhost allowlist and nothing else — and no
//     other process on the machine can read it: the same argument
//     internal/guard makes for its Pin, that a running process's memory is
//     the one place a same-uid attacker cannot reach.
//   - **Idle, not absolute, and only a person's use counts.** A kv action the
//     person ran pushes the deadline out, so a TUI in steady use never asks
//     again, and one left open on a desk relocks itself. A tile reading a
//     profile's credential does not: it runs every few seconds with nobody
//     there, and if it counted the session could never end. Fifteen minutes is
//     sudo's number for the same trade; an unlock the person asked for names
//     its own length (StartSession), up to MaxSessionTTL.
//   - **Started by an open, never by a write.** It is recorded after a
//     decrypt succeeded, which is what makes what it holds known to be the
//     store's passphrase. The write that creates a store decrypts nothing,
//     so a store born in a TUI form asks once more on its first open — the
//     price of that being the single rule rather than one of two.
//   - **A wrong passphrase ends it, and so does a rekey.** Whatever is
//     remembered is not the store's any more; re-asking beats retrying a
//     stale value until the deadline.
//   - **The environment still wins.** RTA_KV_PASSPHRASE is a channel the
//     operator set on purpose; a session is a convenience, and it must
//     never paper over a misconfigured export, so an environment that names
//     a wrong passphrase fails the way it always did.
//
// A keys-mode store whose identity file is itself passphrase-protected is
// opened the same way: what the session holds is then the key's passphrase,
// which is what the host's read feeds the key (unlockSSHKey takes the one it
// is supplied). Only the person's own unlock starts that kind (StartSession);
// a kv form's passphrase box does not. And the TUI keeps the unlock form in front
// of every capability that hands the value out — see `discloses` in
// internal/render/tui — because the unlock is what makes a reveal a
// deliberate act, and a warm session must not turn one keystroke on a list
// into a secret on screen.
var (
	sessionMu    sync.Mutex
	sessionPass  string
	sessionUntil time.Time
	// sessionTTL is how far a person's use pushes the deadline out: the length
	// this session was started with.
	sessionTTL time.Duration
)

const (
	// sessionIdle is how long a session a kv form started stays warm after the
	// person's last use.
	sessionIdle = 15 * time.Minute
	// DefaultSessionTTL is the length an unlock the person asked for offers.
	DefaultSessionTTL = time.Hour
	// MinSessionTTL and MaxSessionTTL bound what a person may ask for. The
	// ceiling is a working day: a passphrase held in memory for longer than
	// that is a standing credential, which is what the encrypted store is
	// there to avoid.
	MinSessionTTL = time.Minute
	MaxSessionTTL = 12 * time.Hour
)

// now is overridable in tests, which move it past the idle limit rather
// than wait for it.
var now = time.Now

// rememberSession starts or refreshes the session. Callers have already
// established that this passphrase opened the store — it is recorded after
// a successful decrypt, never before one.
func rememberSession(passphrase string) { startSession(passphrase, sessionIdle) }

func startSession(passphrase string, ttl time.Duration) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	sessionPass, sessionTTL, sessionUntil = passphrase, ttl, now().Add(ttl)
}

// forgetSession ends it. Safe to call when there is none.
func forgetSession() {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	sessionPass, sessionTTL, sessionUntil = "", 0, time.Time{}
}

// sessionPassphrase returns the session's passphrase and slides its
// deadline, or reports that there is no live session. Expiry is checked
// here rather than by a timer, so a session that lapsed while nothing
// looked is simply not there when something does.
func sessionPassphrase() (string, bool) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if sessionPass == "" || !now().Before(sessionUntil) {
		sessionPass, sessionTTL, sessionUntil = "", 0, time.Time{}
		return "", false
	}
	sessionUntil = now().Add(sessionTTL)
	return sessionPass, true
}

// peekSession is sessionPassphrase for a caller nobody is watching: it returns
// the passphrase and leaves the deadline where it is.
func peekSession() (string, bool) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if sessionPass == "" || !now().Before(sessionUntil) {
		sessionPass, sessionTTL, sessionUntil = "", 0, time.Time{}
		return "", false
	}
	return sessionPass, true
}

// StartSession is the person's own unlock: the passphrase typed into the TUI's
// unlock form, held in memory for ttl so a profile's `secrets:` references can
// be filled without asking again. What it takes is whatever opens the store
// from this process — the store's passphrase, or for a store locked to a
// passphrase-protected key, that key's — and it is tried against the store
// before anything is remembered, so a wrong one is refused here and not held.
//
// Refused when there is nothing to unlock: no store, or one this process
// already opens (a passphrase in the environment, a key that needs none), where
// a session would only be a second answer to a question already answered.
func StartSession(passphrase string, ttl time.Duration) *view.Error {
	if ttl < MinSessionTTL || ttl > MaxSessionTTL {
		return view.Errorf("kv.session.ttl", "a session of %s is outside what is allowed", ttl).
			WithHint("from " + MinSessionTTL.String() + " to " + MaxSessionTTL.String())
	}
	if passphrase == "" {
		return view.Errorf("kv.passphrase.missing", "no passphrase given")
	}
	if _, ok := UnlockPrompt(); !ok {
		return view.Errorf("kv.session.nothing", "there is nothing to unlock").
			WithHint("the store is absent, or this process already opens it")
	}
	req := plugin.NewRequest(map[string]any{
		"passphrase": passphrase,
		"identity":   os.Getenv(identityEnv),
	}, false, true)
	if _, verr := load(req); verr != nil {
		return verr
	}
	startSession(passphrase, ttl)
	return nil
}

// UnlockPrompt says what the person would be asked for to open the store from
// this process, and false when nothing could be asked: there is no store, it
// opens without a question (the environment's passphrase, a key that needs
// none), or a session already holds it. The answer is a phrase for the form's
// own help line: "the store's passphrase", or "the passphrase of the key
// ~/.ssh/rta_kv".
func UnlockPrompt() (string, bool) {
	if !fileExists(storePath()) || os.Getenv(passphraseEnv) != "" {
		return "", false
	}
	if _, held := peekSession(); held {
		return "", false
	}
	mode, _, verr := currentMode()
	if verr != nil {
		return "", false
	}
	if mode == modeKeys {
		if p := LockedIdentity(); p != "" {
			return "the passphrase of the key " + p, true
		}
		return "", false
	}
	return "the store's passphrase", true
}

// ForgetSession ends the session on demand. Exported for whoever owns a
// TUI's lifetime — its tests today, a "relock" keystroke if one is ever
// wanted — and never called by anything a request can reach.
func ForgetSession() { forgetSession() }

// SessionPassphrase is the TUI's way to fill the unlock pair of a kv form
// from the session instead of asking. Using it counts as use: the deadline
// moves.
func SessionPassphrase() (string, bool) { return sessionPassphrase() }

// SessionUntil reports when the session lapses if nothing uses it, without
// itself counting as a use — it is drawn in the header on every render, and
// looking at a badge must not be what keeps a store open.
func SessionUntil() (time.Time, bool) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if sessionPass == "" || !now().Before(sessionUntil) {
		return time.Time{}, false
	}
	return sessionUntil, true
}

// hostPassphrase is what a host-side open (Reveal, Names, Store) unlocks
// with: the environment first, because the operator set that on purpose,
// then the session.
func hostPassphrase() string {
	if p := os.Getenv(passphraseEnv); p != "" {
		return p
	}
	p, _ := peekSession()
	return p
}
