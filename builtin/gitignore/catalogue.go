package gitignore

import (
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/near"
)

// kind is where in the repository a template lives, which is the only
// classification upstream makes: the main ones are for languages, frameworks
// and tools, the global ones for operating systems and editors (what a person
// keeps in their own ignore file rather than in a project's), and the community
// ones are other people's, kept in their own folder.
type kind string

const (
	kindMain      kind = "main"
	kindGlobal    kind = "global"
	kindCommunity kind = "community"
)

// kindOrder is the order templates are listed in, and the order a bare name is
// settled in when two files have it: the main one wins over a global one,
// which wins over a community one.
var kindOrder = map[kind]int{kindMain: 0, kindGlobal: 1, kindCommunity: 2}

// entry is one template as the listing describes it.
type entry struct {
	Path string // as the repository has it: Global/macOS.gitignore
	Name string // the file's name without its extension: macOS
	Kind kind
	SHA  string // git's id for the file, which is what a download is held to
	// Ref is what gitignore.show takes for this template and gets this
	// template back for: the bare name when that is not also somebody else's,
	// and the path without its extension when it is.
	Ref string
}

// within is the template's name the way a person searches for it: the path
// below the folder that says its kind, without the extension, so a community
// template keeps the folder it sits in (Python/JupyterNotebooks) and loses
// the one every community template shares. A search that read the kind's own
// folder as part of a name answered "unity" with the whole of community.
func (e entry) within() string {
	p := strings.TrimSuffix(e.Path, suffix)
	switch e.Kind {
	case kindGlobal:
		return strings.TrimPrefix(p, "Global/")
	case kindCommunity:
		return strings.TrimPrefix(p, "community/")
	}
	return p
}

// matches is whether a lower-cased search term picks the template: it is part
// of the template's name, or it is the word for the template's kind. A kind
// word also stays a name search, which is what keeps a template whose name
// holds one listed beside the kind it names instead of becoming unreachable
// by a term.
func (e entry) matches(term string) bool {
	return term == string(e.Kind) || strings.Contains(strings.ToLower(e.within()), term)
}

// catalogue is the repository's listing, in the order it is shown.
type catalogue struct {
	Tree    string
	Entries []entry
}

const suffix = ".gitignore"

func newCatalogue(tree treeResponse) *catalogue {
	c := &catalogue{Tree: tree.SHA}
	for _, item := range tree.Tree {
		if item.Type != "blob" || !strings.HasSuffix(item.Path, suffix) || !plainPath(item.Path) || !blobHash(item.SHA) {
			continue
		}
		e := entry{Path: item.Path, Name: strings.TrimSuffix(path.Base(item.Path), suffix), SHA: item.SHA}
		switch {
		case !strings.Contains(item.Path, "/"):
			e.Kind = kindMain
		case strings.HasPrefix(item.Path, "Global/"):
			e.Kind = kindGlobal
		case strings.HasPrefix(item.Path, "community/"):
			e.Kind = kindCommunity
		default:
			// Folders upstream keeps for itself (workflows, documentation) hold
			// no templates, and a new kind of folder is not one to guess about.
			continue
		}
		if e.Name == "" {
			continue
		}
		c.Entries = append(c.Entries, e)
	}
	sort.SliceStable(c.Entries, func(i, j int) bool {
		a, b := c.Entries[i], c.Entries[j]
		if a.Kind != b.Kind {
			return kindOrder[a.Kind] < kindOrder[b.Kind]
		}
		if x, y := strings.ToLower(a.Path), strings.ToLower(b.Path); x != y {
			return x < y
		}
		return a.Path < b.Path
	})
	for i := range c.Entries {
		e := &c.Entries[i]
		if won := c.byName(e.Name); len(won) == 1 && won[0] == i {
			e.Ref = e.Name
		} else {
			e.Ref = strings.TrimSuffix(e.Path, suffix)
		}
	}
	return c
}

// plainPath refuses a path that is not a plain relative one. The listing is a
// third party's text, and a path from it ends up in a URL and in a terminal.
func plainPath(p string) bool {
	if !utf8.ValidString(p) || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// blobHash holds an id to the shape git gives one, so a listing entry carrying
// anything else is dropped instead of being compared against.
func blobHash(h string) bool {
	if len(h) != 40 {
		return false
	}
	for _, r := range h {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// byName is the entries a bare name could mean, narrowed to the first kind that
// has any: a name that a main template and a community one share means the main
// one. More than one left is a name upstream itself keeps twice in one place.
func (c *catalogue) byName(name string) []int {
	var won []int
	for i, e := range c.Entries {
		if !strings.EqualFold(e.Name, name) {
			continue
		}
		// The entries are in kind order, so the first match names the kind
		// that wins and the first match of another kind ends the search.
		if len(won) > 0 && e.Kind != c.Entries[won[0]].Kind {
			break
		}
		won = append(won, i)
	}
	return won
}

// byPath is the entry a path names, with or without its extension, with or
// without the community folder in front, in any case.
func (c *catalogue) byPath(p string) (int, bool) {
	p = strings.TrimSuffix(p, suffix)
	for i, e := range c.Entries {
		full := strings.TrimSuffix(e.Path, suffix)
		if strings.EqualFold(full, p) {
			return i, true
		}
		if e.Kind == kindCommunity && strings.EqualFold(strings.TrimPrefix(full, "community/"), p) {
			return i, true
		}
	}
	return 0, false
}

// nicknames are the few names people type for a template that upstream spells
// another way. Each is tried only when what was typed matches nothing, and only
// when its target exists, so adding one can never change what an exact name
// means.
var nicknames = map[string]string{
	"vscode":   "VisualStudioCode",
	"golang":   "Go",
	"osx":      "macOS",
	"mac":      "macOS",
	"intellij": "JetBrains",
	"idea":     "JetBrains",
	"nodejs":   "Node",
	"py":       "Python",
	"tf":       "Terraform",
}

type outcome int

const (
	found outcome = iota
	ambiguous
	unknown
)

// resolve is the template a typed name means. When it means none, or more than
// one, refs say what it might have meant: the suggestions for a name that
// matched nothing, and every candidate for one that matched several.
func (c *catalogue) resolve(typed string) (entry, outcome, []string) {
	q := strings.TrimSpace(typed)
	if len(q) > len(suffix) && strings.EqualFold(q[len(q)-len(suffix):], suffix) {
		q = q[:len(q)-len(suffix)]
	}
	if i, ok := c.byPath(q); ok && strings.Contains(q, "/") {
		return c.Entries[i], found, nil
	}
	for _, name := range []string{q, nicknames[strings.ToLower(q)]} {
		if name == "" {
			continue
		}
		switch won := c.byName(name); len(won) {
		case 0:
		case 1:
			return c.Entries[won[0]], found, nil
		default:
			refs := make([]string, len(won))
			for k, i := range won {
				refs[k] = strings.TrimSuffix(c.Entries[i].Path, suffix)
			}
			return entry{}, ambiguous, refs
		}
	}
	return entry{}, unknown, c.suggest(q)
}

// suggest is up to five templates a name that matched nothing might have been
// meant for: those that contain it, or else the nearest spelling.
func (c *catalogue) suggest(q string) []string {
	low := strings.ToLower(q)
	var out []string
	if low != "" {
		for _, e := range c.Entries {
			if strings.Contains(strings.ToLower(e.Ref), low) {
				out = append(out, e.Ref)
				if len(out) == 5 {
					return out
				}
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	// The nearest spelling is looked for among the nicknames too, so that
	// "golng" finds "golang" and says what it means.
	candidates := make([]string, 0, len(c.Entries)+len(nicknames))
	for _, e := range c.Entries {
		candidates = append(candidates, e.Ref)
	}
	for nick := range nicknames {
		candidates = append(candidates, nick)
	}
	sort.Strings(candidates[len(c.Entries):])
	w := near.Word(q, candidates)
	if target, isNickname := nicknames[w]; isNickname {
		w = ""
		if i := c.byName(target); len(i) == 1 {
			w = c.Entries[i[0]].Ref
		}
	}
	if w != "" {
		return []string{w}
	}
	return nil
}
