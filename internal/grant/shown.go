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

// ArtifactState is how the artifact a grant was issued against (Digest)
// compares with the one answering for its namespace now.
type ArtifactState int

const (
	// ArtifactCurrent is the artifact the grant names, answering now: the
	// gate compares the two equal.
	ArtifactCurrent ArtifactState = iota
	// ArtifactReplaced is another artifact answering for the namespace now —
	// an upgrade, a rebuild, a binary swapped in under the name — so the
	// grant covers no call, whatever else it says.
	ArtifactReplaced
	// ArtifactGone is nothing answering for the namespace now: the plugin
	// was removed, or its bytes are not ones this machine trusts to run.
	ArtifactGone
	// ArtifactUnknown is a grant nobody judged: one a remote server listed
	// without its verdict, as a server older than the verdict does. Never a
	// state ArtifactNow gives; a listing shows it rather than guess, since
	// the plugins that answer there are the server's.
	ArtifactUnknown
)

// artifactWords are the states as the operator channel carries them
// (operator.GrantList): words rather than numbers, so that a state a newer
// server sends and this build has no word for reads as unknown instead of
// as whichever state happens to share its number.
var artifactWords = map[ArtifactState]string{
	ArtifactCurrent:  "current",
	ArtifactReplaced: "replaced",
	ArtifactGone:     "gone",
	ArtifactUnknown:  "unknown",
}

// MarshalText is the state's word on the wire.
func (s ArtifactState) MarshalText() ([]byte, error) {
	word, ok := artifactWords[s]
	if !ok {
		word = artifactWords[ArtifactUnknown]
	}
	return []byte(word), nil
}

// UnmarshalText reads a state's word, and any word it does not know as
// ArtifactUnknown rather than as an error: one verdict this build cannot
// read must not cost the roster every other one.
func (s *ArtifactState) UnmarshalText(text []byte) error {
	*s = ArtifactUnknown
	for state, word := range artifactWords {
		if word == string(text) {
			*s = state
		}
	}
	return nil
}

// ArtifactFrom is ArtifactNow asked of lookup, the registry's answer for a
// namespace (registry.Artifact) — the one lookup the gate compares a
// grant's Digest against, so every listing that judges a grant judges it
// by the gate's rule: grant list, the metrics a dashboard reads, and a
// server answering an operator's roster.
func (g Grant) ArtifactFrom(lookup func(string) (string, bool)) ArtifactState {
	current, known := lookup(Namespace(g.Target))
	return g.ArtifactNow(current, known)
}

// ArtifactNow judges the grant against the artifact behind its namespace
// now, as registry.Artifact answers: current is that artifact's digest,
// empty for a built-in, and known is false when nothing answers for the
// namespace.
//
// The gate's own comparison (covers), asked by what lists grants rather
// than by a call. It had to be: upgrading a plugin invalidates every grant
// standing on it — the Digest field says why that is the rule — and the
// roster went on showing each one as live, inside its window and budget,
// while every call it was issued for was refused under the sentence an
// ungranted call gets. Nothing on any screen said why a grant had stopped
// covering, nor that issuing it again is the fix.
func (g Grant) ArtifactNow(current string, known bool) ArtifactState {
	switch {
	case !known:
		return ArtifactGone
	case current != g.Digest:
		return ArtifactReplaced
	}
	return ArtifactCurrent
}

// ShortDigest is a digest as a listing shows it: its first twelve
// characters, the prefix rta doctor prints for a plugin and a
// `plugins.<ns>@<digest>` pin is compared against.
func ShortDigest(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

// RosterArtifact is a grant's Artifact cell: the short digest of the plugin
// it was issued against, "built in" for one issued on a namespace the rta
// binary answers for itself — it has no artifact apart from the rta the
// operator chose to run, so its grants carry no digest — and a mark when
// that is no longer what answers (ArtifactNow), or when nobody said
// whether it is (ArtifactUnknown).
func RosterArtifact(digest string, state ArtifactState) string {
	shown := "built in"
	if digest != "" {
		shown = ShortDigest(digest)
	}
	switch state {
	case ArtifactReplaced:
		shown += " (replaced)"
	case ArtifactGone:
		shown += " (not loaded)"
	case ArtifactUnknown:
		shown += " (unknown)"
	}
	return shown
}
