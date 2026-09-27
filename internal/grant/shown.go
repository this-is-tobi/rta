package grant

import "github.com/this-is-tobi/rta/internal/textclean"

// Named is a grant as a line of prose names it: its target, then its record
// as the gate compares it (textclean.Record) when it names one — the way a
// person would type it to take the grant back.
//
// One spelling for every sentence that lists grants, because the gate judges
// a record byte for byte and a sentence that joined target and record as
// they are could not: a grant on "db-password" followed by a no-break space
// read as the grant on db-password beside it, and trimming the pair to tidy
// it made a padded record and the bare one the same words. rta doctor listed
// grants that way after every other listing had stopped.
func (g Grant) Named() string {
	if g.Scope == "" {
		return g.Target
	}
	return g.Target + " " + textclean.Record(g.Scope)
}
