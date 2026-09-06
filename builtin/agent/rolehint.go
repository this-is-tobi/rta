package agent

import (
	"fmt"
	"strings"

	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/role"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// roleHint says which available role has a line covering a parked call, so
// the first ungranted call of the day can be answered with the day's list.
// Computed at display time and never written into the request file: the
// operator's sealed decision binds to a digest over the call as displayed,
// and a stored hint would be a rewritable suggestion to approve more.
func roleHint(r consent.Request) string {
	all, verr := role.Available()
	if verr != nil {
		return ""
	}
	for _, s := range all {
		// A role naming another agent is not this call's; one naming none,
		// or this one, is.
		if s.Role.Agent != "" && s.Role.Agent != r.Agent {
			continue
		}
		lines, err := s.Lines()
		if err != nil {
			continue
		}
		for i, l := range lines {
			if covers(l, r) {
				return fmt.Sprintf("line %d of %s — `rta agent allow %s --role %s` issues the whole role, then this call",
					i+1, s.Name, r.ID, s.Name)
			}
		}
	}
	return ""
}

// covers is the role line's promise against the call: the capability or its
// plugin, the one record if the line names one, the same connection.
func covers(l role.Line, r consent.Request) bool {
	if l.Target != r.Cap && l.Target != plugin.Namespace(r.Cap) {
		return false
	}
	if strings.TrimSpace(l.Profile) != strings.TrimSpace(r.Profile) {
		return false
	}
	if l.Scope == "" {
		return true
	}
	return len(r.Scopes) == 1 && r.Scopes[0] == l.Scope
}
