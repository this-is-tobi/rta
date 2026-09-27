package agent

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/role"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// roleHint says which available role has a line covering a parked call, so
// the first ungranted call of the day can be answered with the day's list.
// Computed at display time and never written into the request file: the
// operator's sealed decision binds to a digest over the call as displayed,
// and a stored hint would be a rewritable suggestion to approve more. The
// call it offers is spelled as sf, the surface showing the request, makes it.
func roleHint(sf plugin.Surface, r consent.Request) string {
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
		if used := covering(lines, r); len(used) > 0 {
			which := "line " + used[0]
			if len(used) > 1 {
				which = "lines " + andList(used)
			}
			return fmt.Sprintf("%s of %s — `%s` issues the whole role, then this call", which, s.Name,
				sf.Call("agent.allow", plugin.Arg{Name: "id", Value: r.ID, Positional: true},
					plugin.Arg{Name: "role", Value: s.Name}))
		}
	}
	return ""
}

// covering is the numbers of the lines that cover the call between them, or
// none when they do not: for each record the call names, the first line that
// covers it. A call naming two — every kv.rename, whose destination needs a
// grant as much as its key — is covered by one line naming no record, or by
// one for each; a role was held to a line covering a call naming exactly
// one, so no rename was ever offered a role, even one with both of its lines.
//
// In the role's order, not the call's: the reader finds them by counting
// down the role, and "lines 2 and 1" sends them back up it.
func covering(lines []role.Line, r consent.Request) []string {
	var used []int
	for _, record := range records(r) {
		i := slices.IndexFunc(lines, func(l role.Line) bool { return covers(l, r, record) })
		if i < 0 {
			return nil
		}
		if !slices.Contains(used, i+1) {
			used = append(used, i+1)
		}
	}
	slices.Sort(used)
	out := make([]string, len(used))
	for k, n := range used {
		out[k] = strconv.Itoa(n)
	}
	return out
}

// covers is the role line's promise against one record of the call: the
// capability or its plugin, that record if the line names one, the same
// connection.
func covers(l role.Line, r consent.Request, record string) bool {
	if l.Target != r.Cap && l.Target != plugin.Namespace(r.Cap) {
		return false
	}
	if strings.TrimSpace(l.Profile) != strings.TrimSpace(r.Profile) {
		return false
	}
	return l.Scope == "" || l.Scope == record
}
