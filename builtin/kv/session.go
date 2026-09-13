package kv

import (
	"os"
	"sync"
	"time"
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
//   - **Idle, not absolute.** Every use pushes the deadline out, so a TUI in
//     steady use never asks again, and one left open on a desk relocks
//     itself. Fifteen minutes is sudo's number for the same trade.
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
// What it deliberately does not cover: a keys-mode store whose identity
// file is itself passphrase-protected. That passphrase belongs to the key,
// not the store, and keyPassphrases (crypt.go) already caches it per path
// for the life of the process. And the TUI keeps the unlock form in front
// of every capability that hands the value out — see `discloses` in
// internal/render/tui — because the unlock is what makes a reveal a
// deliberate act, and a warm session must not turn one keystroke on a list
// into a secret on screen.
var (
	sessionMu    sync.Mutex
	sessionPass  string
	sessionUntil time.Time
)

// sessionIdle is how long a session stays warm after its last use.
const sessionIdle = 15 * time.Minute

// now is overridable in tests, which move it past the idle limit rather
// than wait for it.
var now = time.Now

// rememberSession starts or refreshes the session. Callers have already
// established that this passphrase opened the store — it is recorded after
// a successful decrypt, never before one.
func rememberSession(passphrase string) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	sessionPass, sessionUntil = passphrase, now().Add(sessionIdle)
}

// forgetSession ends it. Safe to call when there is none.
func forgetSession() {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	sessionPass, sessionUntil = "", time.Time{}
}

// sessionPassphrase returns the session's passphrase and slides its
// deadline, or reports that there is no live session. Expiry is checked
// here rather than by a timer, so a session that lapsed while nothing
// looked is simply not there when something does.
func sessionPassphrase() (string, bool) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if sessionPass == "" || !now().Before(sessionUntil) {
		sessionPass, sessionUntil = "", time.Time{}
		return "", false
	}
	sessionUntil = now().Add(sessionIdle)
	return sessionPass, true
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
	p, _ := sessionPassphrase()
	return p
}
