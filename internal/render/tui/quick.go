package tui

import "github.com/this-is-tobi/rta/pkg/plugin"

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
	if c.Safety != plugin.Read || c.Run == nil || formNeeded(c) {
		return nil, false
	}
	base := withStoreSession(c, nil)
	for _, f := range c.Inputs {
		if f.Type.Sensitive() && !m.answered(c, f, base) {
			return nil, false
		}
	}
	return base, true
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
