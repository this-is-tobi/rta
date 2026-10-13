package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/this-is-tobi/rta/pkg/view"
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

// sectionFault is a place in the text where a section's key has a shape the
// decoder refuses, and what it takes there.
type sectionFault struct {
	key, says, takes, hint string
	line, col              int
}

// refuseBadSections is what the loader says of a file the decoder refused when
// the reason is a section's key of the wrong shape: a `columns:` that is not a
// whole number, or a `sections:` that is not a mapping of names. It names the file,
// the spot, the key and what the key takes, as a profile's `dashboard:` is
// refused (refuseBadDashboards), where the decoder's sentence is about Go types,
// names no key and quotes a line of the file.
//
// **Asked only once the decoder has refused the file**, and answered from the text
// by the one test the decoder would apply: the value is decoded as the number it
// is meant to be, so a count in quotes, a decimal and an empty one, which load,
// are never named, and a file with another mistake in it is still refused for that
// one, in the decoder's words. Not asked of a file that loads, so reading the
// config costs nothing for it. What it does not cover — a section's entry that is
// not a mapping — is the decoder's sentence still.
func refuseBadSections(path string, data []byte) error {
	f := sectionFaultIn(data)
	if f == nil {
		return nil
	}
	spot := ""
	if f.line > 0 {
		spot = fmt.Sprintf("[%d:%d] ", f.line, f.col)
	}
	return view.Errorf("config.invalid", "parsing %s: %s%s %s, and %s", path, spot, f.key, f.says, f.takes).
		WithHint(f.hint)
}

// sectionFaultIn is the first section key of the wrong shape in the text, none for
// text that is not YAML at all, which is the decoder's own to refuse.
func sectionFaultIn(data []byte) *sectionFault {
	file, err := parser.ParseBytes(data, 0)
	if err != nil || len(file.Docs) == 0 || file.Docs[0].Body == nil {
		return nil
	}
	return firstSectionFault(file.Docs[0].Body)
}

// firstSectionFault is the first section key of the wrong shape in the file's
// order, wherever sections are stated: the dashboard block, a view, and a profile's
// own screen.
func firstSectionFault(root ast.Node) *sectionFault {
	for _, top := range mappingPairs(root) {
		switch keyText(top) {
		case "dashboard":
			if f := blockSectionFault("dashboard", top.Value, true); f != nil {
				return f
			}
		case "profiles":
			for _, profile := range mappingPairs(top.Value) {
				for _, field := range mappingPairs(profile.Value) {
					if keyText(field) != "dashboard" {
						continue
					}
					if f := blockSectionFault("profiles."+pathPart(keyText(profile))+".dashboard", field.Value, false); f != nil {
						return f
					}
				}
			}
		}
	}
	return nil
}

// blockSectionFault is the first fault in one dashboard block and, for the block
// that has them, in its views, which are blocks of the same keys.
func blockSectionFault(prefix string, block ast.Node, hasViews bool) *sectionFault {
	for _, pair := range mappingPairs(block) {
		switch keyText(pair) {
		case "sections":
			if f := sectionsFault(prefix+".sections", pair.Value); f != nil {
				return f
			}
		case "views":
			if !hasViews {
				continue
			}
			for _, v := range mappingPairs(pair.Value) {
				if f := blockSectionFault(prefix+".views."+pathPart(keyText(v)), v.Value, false); f != nil {
					return f
				}
			}
		}
	}
	return nil
}

func sectionsFault(key string, node ast.Node) *sectionFault {
	if _, empty := node.(*ast.NullNode); empty {
		return nil
	}
	if !isMapping(node) {
		return &sectionFault{
			key: key, says: "is " + describeValue(node),
			takes: "takes a mapping of section names to how each starts",
			hint: "state each section under its name — `sections: {databases: {collapsed: true, columns: 2}}` — " +
				"or take the key out; `rta config schema` lists everything a section takes",
			line: node.GetToken().Position.Line, col: node.GetToken().Position.Column,
		}
	}
	for _, section := range mappingPairs(node) {
		for _, field := range mappingPairs(section.Value) {
			if keyText(field) != "columns" {
				continue
			}
			var n int
			if yaml.NodeToValue(field.Value, &n) == nil {
				continue
			}
			// A number too large for an int is not read as one by the parser either,
			// so it is told from a word by what it spells.
			says := describeValue(field.Value)
			if _, err := strconv.Atoi(field.Value.GetToken().Value); errors.Is(err, strconv.ErrRange) {
				says = "a number too large to be a count of columns"
			}
			return &sectionFault{
				key: key + "." + pathPart(keyText(section)) + ".columns", says: "is " + says,
				takes: "takes a whole number from 0",
				hint: "`columns: 3` draws three tiles to a row of the section, and 0 or leaving it out is the " +
					"dashboard's own grid — `rta config schema` lists everything a section takes",
				line: field.Value.GetToken().Position.Line, col: field.Value.GetToken().Position.Column,
			}
		}
	}
	return nil
}

// mappingPairs are the pairs of a YAML mapping in the order it states them, and
// none for anything that is not one.
func mappingPairs(n ast.Node) []*ast.MappingValueNode {
	switch v := n.(type) {
	case *ast.MappingNode:
		return v.Values
	case *ast.MappingValueNode:
		return []*ast.MappingValueNode{v}
	}
	return nil
}

func isMapping(n ast.Node) bool {
	switch n.(type) {
	case *ast.MappingNode, *ast.MappingValueNode:
		return true
	}
	return false
}

func keyText(pair *ast.MappingValueNode) string { return pair.Key.GetToken().Value }

// pathPart is a name as it stands in a dotted path a person would look a key up
// by: as it is when it can be a heading, and quoted when it holds what a path
// could not show, a line break or an escape sequence.
func pathPart(name string) string {
	if ValidSectionName(name) {
		return name
	}
	return strconv.Quote(name)
}

// describeValue is what a YAML value is, in the words a person who wrote it would
// use.
func describeValue(n ast.Node) string {
	switch n.(type) {
	case *ast.SequenceNode:
		return "a list"
	case *ast.MappingNode, *ast.MappingValueNode:
		return "a mapping"
	case *ast.IntegerNode, *ast.FloatNode, *ast.InfinityNode, *ast.NanNode:
		return "a number"
	case *ast.BoolNode:
		return "true or false"
	case *ast.StringNode:
		return "text"
	case *ast.LiteralNode:
		return "a block of text"
	}
	return "not what it takes"
}
