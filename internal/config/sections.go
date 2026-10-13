package config

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Dashboard sections: tiles drawn together under one header that can be folded.
//
// **Why a label on the entry and a map beside the entries, and not tiles nested
// under a section.** Every writer of the dashboard — `rta dashboard add`, the
// TUI's `+`, `H` and `[` `]`, the hand in the file — addresses a tile as an entry
// of `tiles:` or `add:` with a key (Tile.Key), and `order:`, `hidden:` and the
// replace-in-place of a second `add` are all stated in that key space. Nesting
// the entries under a section would have made each of them a two-level lookup,
// and moved every existing entry in every existing file. A `section:` on the
// entry is one more field that is absent from every config there is today, which
// is also what keeps a dashboard that states no section drawn exactly as it was.
// What a section *is* — whether it starts folded, how many columns its tiles
// share — is not a property of any one tile, so it is stated once, by name, in
// `sections:`; naming a section there that no tile is in is a dangling name that
// `rta config check` reports rather than a refusal, as a view a profile names
// that nothing states is.
//
// **A section has no identity beyond its name.** Two entries that say
// `section: databases` are in the same section, wherever in the file they are,
// and an expanded entry's panels are in the section the entry names or, when it
// names none, the one `AutoSectionName` spells. So the section a person sees
// folded by default and the one `sections:` addresses are one string, and a
// section that no longer has a tile in it is simply not drawn.

// DashboardSection is how one section starts out and how it is drawn.
type DashboardSection struct {
	// Collapsed is the state the section opens in: one header row, however many
	// tiles it holds. It is only the default. z and Z in the TUI fold and unfold
	// for the session and write nothing, so what a person left folded does not
	// follow them into the next terminal — see tui.Model.folds for why that is
	// not persisted, and what persisting it would take.
	Collapsed bool `yaml:"collapsed,omitempty" json:"collapsed,omitempty"`
	// Columns is the grid width of this section's tile rows, in place of the
	// dashboard's. 0 inherits it, which is every section's default: a section
	// that wants three narrow tiles in a row beside a dashboard of two wide ones
	// says so here, and the rest is not touched.
	Columns int `yaml:"columns,omitempty" json:"columns,omitempty"`
}

// MaxSectionName bounds a section's name, in characters: it is a header the
// terminal draws on one line, and an unbounded one is a line that cuts the
// count and the status beside it.
const MaxSectionName = 64

// ValidSectionName reports whether name can be a section's: not empty, no
// space at either end, at most MaxSectionName characters, and nothing the
// terminal would act on — a control character, or one that reorders the text
// around it, is not a name. Anything else is a name: a section is a heading a
// person reads, so `databases`, `pg · staging` and `prod: orders` all are.
func ValidSectionName(name string) bool {
	if name == "" || name != strings.TrimSpace(name) || utf8.RuneCountInString(name) > MaxSectionName {
		return false
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// AutoSectionName is the section an entry's panels are in when the entry became
// several of them and names none: the plugin and the profile whose connections
// they are, `pg · staging`. It is a rule and not an invention of the drawing
// code because it is also a key — `sections: {"pg · staging": {collapsed:
// true}}` folds the group by default — so the writer of the file and the
// drawing of the screen have to agree on the spelling.
func AutoSectionName(namespace, profile string) string { return namespace + " · " + profile }

// SectionOf is the stated state of a section, and the zero one for a name
// `sections:` does not mention.
func (d Dashboard) SectionOf(name string) DashboardSection { return d.Sections[name] }
