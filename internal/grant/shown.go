package grant

import (
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/internal/textclean"
)

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

// The roster's own words, beside the records and connections it lists: none
// of either, a folder's width, and a connection repointed since issue.
const (
	rosterAnyRecord = "any"
	rosterNoProfile = "—"
	rosterFolder    = " (all)"
	rosterChanged   = " (changed)"
)

// ShownRecord is a record as a listing shows it: as the gate compares it
// (textclean.Record), none for a grant naming no record — the listing's own
// word for that, "any" in the roster and a dash in a plan — and quoted when
// the record is spelled like that word, or a kv key named any reads as a
// grant over the whole store.
func ShownRecord(scope, none string) string {
	switch scope {
	case "":
		return none
	case none:
		return strconv.Quote(scope)
	}
	return textclean.Record(scope)
}

// RosterRecord is a grant's record in the roster's Record column: as
// ShownRecord shows it, "any" for none, and a folder with its width beside
// it — a bare "prod/" in a column headed Record reads as one record with a
// trailing slash, which is the opposite of what it authorizes.
func RosterRecord(scope string) string {
	shown := ShownRecord(scope, rosterAnyRecord)
	if IsFolderScope(scope) {
		shown += rosterFolder
	}
	return shown
}

// RecordOfRoster reads a Record cell back into the record it shows, and ok is
// false for a cell RosterRecord draws for no record at all.
//
// For what acts on a row rather than reading it. The TUI's x and n on a
// grant.list row seed revoke and renew from the row, and seeded the cell
// itself: "any" as a record named any, "prod/ (all)" as a record nobody
// holds, and a quoted padded record as its quotation marks — so the seeded
// command named another grant or none, on the one screen that exists to take
// a grant back quickly.
//
// An inverse is enough because the drawing is one to one: textclean.Record
// never shows two records alike, the roster's word for none is quoted when
// it is a record, and only a folder gets its width. Proved rather than
// argued: a reading is accepted only when drawing it again gives the cell.
func RecordOfRoster(cell string) (string, bool) {
	body, folder := strings.CutSuffix(cell, rosterFolder)
	scope := body
	switch {
	case body == rosterAnyRecord && !folder:
		scope = ""
	case strings.HasPrefix(body, `"`):
		unquoted, err := strconv.Unquote(body)
		if err != nil {
			return "", false
		}
		scope = unquoted
	}
	if RosterRecord(scope) != cell {
		return "", false
	}
	return scope, true
}

// RosterProfile is a grant's connection in the roster's Profile column: the
// profile it names, a dash for the base connection — not the word "any",
// which the Record column beside it uses for the opposite meaning: an empty
// profile is not a wildcard — and marked when the connection has been
// repointed since the grant was issued.
func RosterProfile(profile string, changed bool) string {
	shown := profile
	if shown == "" {
		shown = rosterNoProfile
	}
	if changed {
		shown += rosterChanged
	}
	return shown
}

// ProfileOfRoster reads a Profile cell back into the profile it shows, for
// RecordOfRoster's reason: a row whose connection had changed seeded
// "staging (changed)", which names no connection, so the grant it came from
// was never the one taken back. ok is false for a cell RosterProfile draws
// for no profile.
func ProfileOfRoster(cell string) (string, bool) {
	body, changed := strings.CutSuffix(cell, rosterChanged)
	profile := body
	if body == rosterNoProfile {
		profile = ""
	}
	if RosterProfile(profile, changed) != cell {
		return "", false
	}
	return profile, true
}
