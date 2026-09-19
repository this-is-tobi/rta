package tui

import (
	"strings"
	"time"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// discloses is every kv capability whose result *is* the value — on screen,
// on the clipboard, as shell exports. The store session (builtin/kv,
// session.go) never fills their unlock form: the unlock is what makes a
// reveal a deliberate act, which is the whole argument kvreveal_test.go
// makes for keeping `v` a form rather than a value, and a warm session
// must not quietly retire it. Every other kv capability answers a question
// about the store without handing its contents to whoever is at the
// keyboard, and for those the session is exactly the convenience it is
// meant to be.
//
// Audited by reading each handler, the same way each built-in's Flash was
// decided, and pinned by a test so a new disclosing capability
// is a conscious addition here rather than one the session silently covers.
var discloses = map[string]bool{
	"kv.get":  true,
	"kv.copy": true,
	"kv.env":  true,
}

// withStoreSession fills the unlock pair of a kv capability from the store
// session, so fieldsAfter no longer has them to ask about.
//
// The passphrase is the real value rather than a marker, and that is safe by
// the discipline every Secret input already lives under: it reaches the run
// as a caller value the way a typed one would, recent.Record refuses Secret
// fields so it never lands in the completion file, and withoutSecrets strips
// it before a prior run's values seed the next form. identity is filled
// empty on purpose — a session exists only for a store a passphrase opened
// (store.go's remember guard), so there is no key file to name, and the box
// asking for one would be the unlock form reappearing under another label.
//
// A copy, never a write into the caller's map: base is sometimes a row's
// identity that the view keeps.
func withStoreSession(c plugin.Capability, base map[string]any) map[string]any {
	if !strings.HasPrefix(c.ID, "kv.") || discloses[c.ID] {
		return base
	}
	if _, given := base["passphrase"]; given {
		return base
	}
	if !hasInput(c, "passphrase") {
		return base
	}
	pass, ok := kv.SessionPassphrase()
	if !ok {
		return base
	}
	out := make(map[string]any, len(base)+2)
	for k, v := range base {
		out[k] = v
	}
	out["passphrase"] = pass
	if hasInput(c, "identity") {
		out["identity"] = ""
	}
	return out
}

// withoutSessionUnlock drops the unlock pair from the fields a form shows
// when withStoreSession has already answered them in base. startForm shows
// every input of a capability typed into the search bar, base included, so
// the pair would otherwise be asked with the session's answer sitting
// unseen behind the boxes.
func withoutSessionUnlock(c plugin.Capability, fields []plugin.Field, base map[string]any) []plugin.Field {
	if _, ok := kv.SessionPassphrase(); !ok || !strings.HasPrefix(c.ID, "kv.") || discloses[c.ID] {
		return fields
	}
	if _, filled := base["passphrase"]; !filled {
		return fields
	}
	out := make([]plugin.Field, 0, len(fields))
	for _, f := range fields {
		if f.Name == "passphrase" || f.Name == "identity" {
			continue
		}
		out = append(out, f)
	}
	return out
}

func hasInput(c plugin.Capability, name string) bool {
	for _, f := range c.Inputs {
		if f.Name == name {
			return true
		}
	}
	return false
}

// storeBadge is the header's word on the store session, or "" when there is
// none. An open store is a fact worth a glance, so it sits beside the
// environment badge, in the warning colour rather than the environment's
// green: not a problem, but a state somebody chose and might want to know
// is still in force. Peeking at the deadline is not a use, so drawing this
// on every render does not keep the store open.
func (m Model) storeBadge() string {
	until, ok := kv.SessionUntil()
	if !ok {
		return ""
	}
	left := time.Until(until)
	if left <= 0 {
		return ""
	}
	return theme.WarnText.Render(" ◐ store unlocked · " + profile.ShortDuration(left))
}
