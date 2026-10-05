package plugindist

import (
	"slices"
	"sort"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// declChange is one line of the declaration diff together with whether that
// line hands the plugin more than it had.
//
// The two travel together because they are one judgement made in one place.
// A bulk upgrade decides whether to land bytes on the strength of this, and
// the alternative — rendering the lines and matching on their text later —
// would re-derive the judgement from prose, wrongly the first time a line's
// wording changes and silently, in the direction of landing.
type declChange struct {
	line string
	// widens is "the operator would be agreeing to something they have not
	// agreed to", not "this changed". Most of the diff is news.
	widens bool
}

// declarationDiff lists what changed between two declarations, in the
// dimensions an authorization hangs off: each capability's safety class and
// grant, whether it reveals a value a masked view withholds, and the
// credential locations the plugin asks to read. A capability changing safety class,
// or a destructive one appearing, is the supply-chain event that matters —
// and precisely what a signature does not tell you.
func declarationDiff(old, next plugin.Plugin) []string {
	changes := declarationChanges(old, next)
	lines := make([]string, len(changes))
	for i, c := range changes {
		lines[i] = c.line
	}
	return lines
}

// widenings is the half of the diff a gate can act on: the changes that would
// hand the plugin authority the operator has not weighed.
func widenings(old, next plugin.Plugin) []string {
	var lines []string
	for _, c := range declarationChanges(old, next) {
		if c.widens {
			lines = append(lines, c.line)
		}
	}
	return lines
}

// revealsText is how the diff says a capability's answer is the value a
// masked view elsewhere withholds (plugin.Capability.Reveals).
const revealsText = "reveals a masked value"

func declarationChanges(old, next plugin.Plugin) []declChange {
	was := map[string]plugin.Capability{}
	for _, c := range old.Capabilities {
		was[c.ID] = c
	}
	var caps []declChange
	for _, c := range next.Capabilities {
		prev, existed := was[c.ID]
		if !existed {
			line := "+ " + c.ID + "  " + string(c.Safety)
			if c.NeedsGrant {
				line += ", needs a grant"
			}
			if c.Reveals {
				line += ", " + revealsText
			}
			// A new read capability behind no grant is the ordinary way a
			// plugin grows, and a gate that stopped for it would be a gate
			// operators route around — which costs more than it buys, because
			// the flag they reach for turns off the destructive case too.
			// Everything else new is authority that did not exist when they
			// last looked.
			caps = append(caps, declChange{line, c.Safety != plugin.Read || c.NeedsGrant || c.Reveals})
			continue
		}
		if prev.Safety != c.Safety {
			caps = append(caps, declChange{
				line:   "! " + c.ID + "  " + string(prev.Safety) + " → " + string(c.Safety),
				widens: harmRank(c.Safety) > harmRank(prev.Safety)})
		}
		if !prev.NeedsGrant && c.NeedsGrant {
			caps = append(caps, declChange{line: "! " + c.ID + "  now needs a grant"})
		}
		if prev.NeedsGrant && !c.NeedsGrant {
			// The same event as a capability appearing, reached from the other
			// side: what stood between the plugin and the operation is gone,
			// and nothing will ask the operator again.
			caps = append(caps, declChange{"! " + c.ID + "  no longer needs a grant", true})
		}
		// What a capability says its answer is is what the operator reads in a
		// consent and what the host tells a caller about the call, so a plugin
		// that starts saying the answer is a masked value has put the value
		// into somebody's context under an id they approved as something
		// else, and one that stops saying it has taken the warning off a call
		// that still returns it. Both are a decision, in the direction of
		// the gate, not news.
		if !prev.Reveals && c.Reveals {
			caps = append(caps, declChange{"! " + c.ID + "  now " + revealsText, true})
		}
		if prev.Reveals && !c.Reveals {
			caps = append(caps, declChange{"! " + c.ID + "  no longer declares that it " + revealsText, true})
		}
		delete(was, c.ID)
	}
	// Whatever is left in was is a capability the new declaration dropped.
	removed := make([]declChange, 0, len(was))
	for id := range was {
		removed = append(removed, declChange{line: "- " + id})
	}

	had := map[plugin.Need]bool{}
	for _, n := range old.Needs {
		had[n] = true
	}
	var needs []declChange
	for _, n := range next.Needs {
		if had[n] {
			delete(had, n)
			continue
		}
		// Never silently granted — allow binds to the digest, so an upgrade
		// drops every allow the old artifact had. It is still growth: the
		// plugin now asks for a credential location nobody weighed, and the
		// operator meets that as a prompt they did not expect or as a failure
		// in the middle of an operation.
		needs = append(needs, declChange{"+ asks to read " + string(n), true})
	}
	for n := range had {
		removed = append(removed, declChange{line: "- asks to read " + string(n)})
	}

	// Capabilities, then needs, then everything that went away — grouped
	// rather than sorted as one list so the axis an operator is scanning for
	// stays together, and removals last because they are the only lines that
	// never need a decision.
	sortChanges(caps)
	sortChanges(needs)
	sortChanges(removed)
	return slices.Concat(caps, needs, removed)
}

func sortChanges(cs []declChange) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].line < cs[j].line })
}

// harmRank orders a safety class by how much harm it admits, reusing
// plugin.Safeties' own ordering rather than restating it. An unrecognised
// class ranks below every real one, so a change into a known class reads as
// growth — the safe direction for a value that should not exist.
func harmRank(s plugin.Safety) int { return slices.Index(plugin.Safeties(), s) }
