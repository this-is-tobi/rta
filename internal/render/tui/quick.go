package tui

import (
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// What enter does on a capability that has nothing worth asking.
//
// A form exists to collect what a run cannot do without, and to let a person
// change what they would otherwise get. For a read with no required input the
// second half is the only job it has, and a person who wants the defaults —
// which is nearly everyone, nearly always — paid for it one field at a time:
// `sys.cpu` took two enters, `time.at` three, `gen.password` eight, each of
// them accepting a box already holding what the run would have used. The
// result screen already says `e edit inputs`, so the form is one key away from
// anyone who wants it, and the common case is no longer the one that waits.
//
// Read and nothing else, on purpose. A write opens its form because the
// values are the consent: the person typing them is the one deciding what is
// about to change. A destructive capability goes through its dry run
// (confirm.go). Neither is touched here, and the MCP surface never reaches this
// path at all: a run from a human surface needs no gate to be skipped, because
// a Read has none.
//
// A credential is the exception inside the reads. An optional input that holds
// a secret is optional because something else may supply it — the store
// session, the environment switched on — and when nothing has, the run does not
// fail softly: it comes back as a refusal, on a screen the person then has to
// leave to find the form that would have asked. So the form stays for a read
// with an unanswered secret box, which is the kv unlock as it has always been.

// quickRun reports whether enter runs c at once, and with which values: the
// ones the store session answers, and nothing else — every other input takes
// the default its declaration, the configuration or the switched-on
// environment gives it, exactly as it would from the form's untouched boxes.
func (m Model) quickRun(c plugin.Capability) (map[string]any, bool) {
	return m.quickRunFrom(c, nil)
}

// quickRunFrom is quickRun for a capability reached from a view that already
// knows some of its inputs — a row's identity, a key on a tile: what is left
// for the form to ask is what is left after those, and a read that has nothing
// left to ask runs.
func (m Model) quickRunFrom(c plugin.Capability, given map[string]any) (map[string]any, bool) {
	if c.Safety != plugin.Read || c.Run == nil {
		return nil, false
	}
	for _, f := range fieldsAfter(c, given) {
		if requiredHere(f) {
			return nil, false
		}
	}
	base := withStoreSession(c, given)
	for _, f := range fieldsAfter(c, base) {
		if f.Type.Sensitive() && !readsOnlyAcrossAServer(c, f, base) && !m.answered(c, f, base) {
			return nil, false
		}
	}
	return base, true
}

// readsOnlyAcrossAServer is whether a credential is read for a call to another
// machine and for no other: the box that signs a request to a server, and any
// box its capability declares as read only beside another input that is empty.
// A run on this machine has no use for it, and a form that asked for it
// anyway is the form a person walked through to run `grant.list`.
func readsOnlyAcrossAServer(c plugin.Capability, f plugin.Field, given map[string]any) bool {
	driver := f.With
	if driver == "" && f.Name == operatorid.PassphraseField.Name && f.Help == operatorid.PassphraseField.Help {
		for _, g := range c.Inputs {
			if g.Remote {
				driver = g.Name
				break
			}
		}
	}
	if driver == "" {
		return false
	}
	v, ok := given[driver]
	return !ok || v == nil || v == ""
}

// answered reports whether a secret input already has its value without
// anyone typing it: given in base (the store session), or filled by the
// environment that is switched on and still in force.
func (m Model) answered(c plugin.Capability, f plugin.Field, base map[string]any) bool {
	if _, given := base[f.Name]; given {
		return true
	}
	if bound := m.currentBind(); bound != nil {
		if b, covered := bound[c.ID]; covered && b.err == nil {
			_, filled := b.values[f.Name]
			return filled
		}
	}
	return false
}
