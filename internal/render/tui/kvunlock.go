package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// unlockID names the unlock form's capability. It is not in any registry: it
// exists here, built when U is pressed, so it is on no CLI, no MCP tool list
// and no catalogue. The one thing that can start a store session for a profile's
// credentials is a person typing into this screen.
const unlockID = "kv.unlock"

// unlockStore is U on the dashboard: one key to unlock the store for this
// session, and the same key to lock it again, after asking.
//
// A dashboard tile that reads a profile's credential from the store runs every
// few seconds with nobody there, so a store that needs a passphrase failed the
// tile at each tick, and the only ways out were to start rta with the
// passphrase in its environment or to run a kv action first and hope it started
// a session. This asks once, for the length the person chooses, and every tile
// that was failing is run again the moment it is answered.
//
// What is asked is whatever opens the store from this process: the store's
// passphrase, or for a store locked to a passphrase-protected key, that key's.
// Nothing is written and nothing leaves this process (builtin/kv's session.go
// carries the argument); an agent has no way to press this key, and an MCP
// server is another process.
func (m Model) unlockStore() (tea.Model, tea.Cmd) {
	if until, held := kv.SessionUntil(); held {
		// Locking is one key away from the keys beside it and costs the
		// passphrase again, so it asks (dialog.go).
		return m.openDialog(lockStoreDialog(time.Until(until))), nil
	}
	prompt, ok := kv.UnlockPrompt()
	if !ok {
		m.refuse("nothing to unlock — there is no store yet, or the environment already opens it")
		return m, nil
	}
	return m.openUnlock(prompt)
}

// openUnlock opens the unlock form asking for prompt, from the dashboard U is a
// key of. Said, as every other way into a form says where it was opened from:
// leaving the form goes back to the origin, and the one left behind by the last
// screen to open a form was the catalogue, the profiles pane or the plugins
// pane, where esc landed with the dashboard's refresh never started again.
func (m Model) openUnlock(prompt string) (tea.Model, tea.Cmd) {
	m.origin = modeDashboard
	return m.open(unlockCapability(prompt))
}

// unlockCapability is the form and the action behind it: a masked box for the
// passphrase and one for how long to keep it.
func unlockCapability(prompt string) plugin.Capability {
	return plugin.Capability{
		ID:      unlockID,
		Summary: "Unlock the store for this session",
		Description: "Opens the encrypted store from this rta process only, so the dashboard's tiles can read the " +
			"credentials a profile maps onto it. The passphrase is held in this process's memory, never written and never " +
			"passed to a plugin, and the store locks again when the time is up or when you press U.",
		Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "passphrase", Type: plugin.Secret, Required: true, Help: prompt},
			{
				Name: "for", Type: plugin.Duration, Default: ttlText(kv.DefaultSessionTTL),
				Min: ttlText(kv.MinSessionTTL), Max: ttlText(kv.MaxSessionTTL),
				Help: "how long the store stays unlocked, from " + ttlText(kv.MinSessionTTL) + " to " + ttlText(kv.MaxSessionTTL) +
					" — shorter is safer, and U locks it sooner",
			},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			ttl := req.Duration("for")
			if verr := kv.StartSession(req.String("passphrase"), ttl); verr != nil {
				// The store names the passphrase it refuses; the length is this
				// form's own box, whose name the store does not know.
				if verr.Code == "kv.session.ttl" {
					return nil, verr.WithInput("for")
				}
				return nil, verr
			}
			return view.Text{Body: fmt.Sprintf("store unlocked for %s — U locks it again", ttlText(ttl))}, nil
		},
	}
}

// unlockItem is the footer's word on the store: "lock store" while a session
// holds it open and "unlock store" otherwise, with the object named because
// `lock` is also the plugin that freezes an agent and `L lock` on the consent
// queue. Always offered, because a screen advertises every
// key it answers to; where there is nothing to unlock, the key says so.
func (m Model) unlockItem() hintItem {
	if _, held := kv.SessionUntil(); held {
		return labelled(bindUnlock, "lock store")
	}
	return item(bindUnlock)
}

// ttlText writes a length the way a person types one: 1h, not 1h0m0s.
func ttlText(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
