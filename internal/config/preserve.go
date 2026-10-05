package config

import (
	"bytes"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/internal/yamlguard"
	"github.com/this-is-tobi/rta/pkg/view"
)

// What follows is how a write keeps what a person typed into the file.
//
// **The file is the operator's, and rta only ever has a typed view of it.**
// Every writer hands back a Config, and a Config has no comments, no blank
// lines, no ordering, no flow style and no keys it does not know — so marshalling
// it again, which is all write used to do, kept none of them. A role's reasons,
// a comment above a tile, a typo'd key somebody had not noticed yet: all of it
// went on the next `rta dashboard add`, an unrelated command, and the header
// said so in as many words. That made the file safe to rewrite and unsafe to
// annotate, and `rta config edit` would have been a trap.
//
// So the text is spliced, not regenerated. The old file is cut into its
// top-level blocks (splitTop); each is kept byte for byte unless the typed
// meaning of its key changed, and only a key that changed is written again.
// `rta dashboard add` rewrites `dashboard:` and nothing else in the file moves.
// A block that is written again takes its comments with it (the CommentMap
// below), and a key rta does not manage is never touched at all.
//
// Three things are not kept, because keeping them would mean guessing:
//   - a key rta does not know *inside* a block that has to be written again (the
//     key at the top level of the file is kept; `rta config check` names both);
//   - a comment inside a written-again block whose line rta removes, or whose
//     list element changed beyond recognition;
//   - the blank lines and the flow style inside a written-again block.
//
// The comments that survive a rewrite are located by their path in the
// document ($.dashboard.add[1].id), and a path that is an index into a list is
// only as good as the list: remove the first tile and the second's comment, by
// index, would sit on a different tile. remapComments follows each element to
// where its value went instead.

// configHeader is what a new file starts with; legacyHeader is the one earlier
// builds wrote, which promised the opposite of what a write does now and is
// replaced when the file is next written.
const (
	configHeader = "# rta configuration — written by rta.\n" +
		"# Everything here is optional: rta works with no config at all.\n" +
		"# Comments you add by hand stay when rta changes this file (`rta profile set`,\n" +
		"# `rta dashboard add`, the TUI). `rta config edit` opens it and keeps the schema\n" +
		"# below beside it, so an editor completes and checks what you write.\n" +
		"# yaml-language-server: $schema=" + SchemaFile + "\n"
	legacyHeader = "# rta configuration — written by rta.\n" +
		"# Everything here is optional: rta works with no config at all.\n" +
		"# rta writes this whole file again when it changes something (`rta profile set`,\n" +
		"# `rta dashboard add`, the TUI), and a comment added by hand does not survive that.\n"
)

// managedKeys are the top-level keys Config states, in the order it declares
// them: the order a new block is placed in, and the set of keys a write is
// allowed to touch.
var managedKeys = func() []string {
	t := reflect.TypeOf(Config{})
	var keys []string
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if name, _, _ := strings.Cut(f.Tag.Get("yaml"), ","); name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}()

// render is the file cfg should be, given the one there is: old, which may be
// empty. The result is old with the keys that changed written again.
func render(old []byte, cfg Config) ([]byte, error) {
	// The text that is there is somebody else's as much as the loader's: a write
	// that reached it without the loader having read it (Write, from a test or a
	// caller stating the whole file) decodes it below, and the decode is where an
	// anchor expands.
	if err := yamlguard.RefuseAnchors(old); err != nil {
		return nil, view.Errorf("config.invalid", "parsing %s: %v", Path(), err).WithHint(parseHint(err))
	}
	fresh, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(old)) == 0 {
		return append([]byte(configHeader), fresh...), nil
	}
	doc := splitTop(string(old))
	if !doc.ok {
		// Not a block mapping at the top (a `{…}` document, say): there are no
		// blocks to keep, so everything is written again and the comments are
		// carried by their paths alone.
		return withComments(old, cfg, fresh, true)
	}

	var oldCfg Config
	if err := yaml.Unmarshal(old, &oldCfg); err != nil {
		return withComments(old, cfg, fresh, true)
	}
	oldTree, err := typedTree(oldCfg)
	if err != nil {
		return nil, err
	}
	newTree := map[string]any{}
	if err := yaml.Unmarshal(fresh, &newTree); err != nil {
		return nil, err
	}

	var replace, add, remove []string
	for _, key := range managedKeys {
		was, is := oldTree[key], newTree[key]
		if reflect.DeepEqual(was, is) {
			continue
		}
		switch {
		case is == nil && doc.block(key) != nil:
			remove = append(remove, key)
		case is == nil:
		case doc.block(key) == nil:
			add = append(add, key)
		default:
			replace = append(replace, key)
		}
	}
	if len(replace)+len(add)+len(remove) == 0 {
		return old, nil
	}

	var written topDoc
	if len(replace)+len(add) > 0 {
		commented, err := withComments(old, cfg, fresh, false)
		if err != nil {
			return nil, err
		}
		written = splitTop(string(commented))
	}
	bodyOf := func(key string) []string {
		b := written.block(key)
		if b == nil {
			return nil
		}
		return written.lines[b.line:b.end]
	}

	var splices []splice
	for _, key := range replace {
		b := doc.block(key)
		splices = append(splices, splice{at: b.line, to: b.end, with: bodyOf(key)})
	}
	for _, key := range remove {
		b := doc.block(key)
		at, to := b.head, b.end
		// Comments that open the file are as likely its heading as a note on its
		// first key, and a heading outliving a key is a smaller loss than a
		// note about nothing that is not the heading.
		if at == 0 && b == &doc.blocks[0] {
			at = b.line
		}
		for to < len(doc.lines) && strings.TrimSpace(doc.lines[to]) == "" {
			to++
		}
		splices = append(splices, splice{at: at, to: to})
	}
	added := map[int][]string{}
	for _, key := range add {
		at, beforeNote := doc.insertionPoint(key)
		body := bodyOf(key)
		// A block that goes in ahead of one with a note above it is set apart
		// from that note by a blank line, so the note still reads as being about
		// the block below it and not as the last line of the new one.
		if beforeNote {
			body = append(slices.Clone(body), "")
		}
		added[at] = append(added[at], body...)
	}
	for at, lines := range added {
		splices = append(splices, splice{at: at, to: at, with: lines})
	}
	// An insertion ahead of a block that is going away shares its first line,
	// and has to be made before the removal that starts there.
	sort.SliceStable(splices, func(i, j int) bool {
		if splices[i].at != splices[j].at {
			return splices[i].at < splices[j].at
		}
		return splices[i].to < splices[j].to
	})

	var out []string
	pos := 0
	for _, s := range splices {
		out = append(out, doc.lines[pos:s.at]...)
		out = append(out, s.with...)
		pos = s.to
	}
	out = append(out, doc.lines[pos:]...)
	text := strings.Join(out, "\n") + "\n"
	if rest, ok := strings.CutPrefix(text, legacyHeader); ok {
		text = configHeader + rest
	}
	return []byte(text), nil
}

// splice replaces lines[at:to] with a different run of lines.
type splice struct {
	at, to int
	with   []string
}

// typedTree is cfg as the generic tree its file form parses to: what the file
// says, as far as rta reads it, with no comments and no ordering to compare.
func typedTree(cfg Config) (map[string]any, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	// What was just marshalled from a typed value holds no anchor, and the guard
	// is asked anyway: every decode in this package goes through it, so one added
	// later over text from elsewhere is covered without having to remember to.
	if err := yamlguard.RefuseAnchors(data); err != nil {
		return nil, err
	}
	tree := map[string]any{}
	if err := yaml.Unmarshal(data, &tree); err != nil {
		return nil, err
	}
	return tree, nil
}

// withComments marshals cfg with the comments of old applied to it.
//
// A comment that cannot be placed is left out and the rest are kept: a comment
// that goes missing costs one line of somebody's notes, and an error here would
// refuse a write over it.
//
// edges says whether the comments above and below a top-level key travel too.
// They do when the whole document is written again; when it is spliced they
// are the text between the blocks, which stays where it is (splitTop), and
// carrying them as well would print them twice.
func withComments(old []byte, cfg Config, fresh []byte, edges bool) ([]byte, error) {
	if err := yamlguard.RefuseAnchors(old); err != nil {
		return nil, view.Errorf("config.invalid", "parsing %s: %v", Path(), err).WithHint(parseHint(err))
	}
	cm := yaml.CommentMap{}
	var rawOld any
	if err := yaml.UnmarshalWithOptions(old, &rawOld, yaml.CommentToMap(cm)); err != nil || len(cm) == 0 {
		return append([]byte(configHeader), fresh...), nil
	}
	var rawNew any
	if err := yaml.Unmarshal(fresh, &rawNew); err != nil {
		return nil, err
	}
	kept := remapComments(cm, rawOld, rawNew, edges)
	out, err := yaml.MarshalWithOptions(cfg, yaml.WithComment(kept))
	if err != nil {
		return append([]byte(configHeader), fresh...), nil
	}
	return out, nil
}

// remapComments moves the comments of the old document onto the new one.
//
// A comment about a top-level key as a whole — the lines above it, the lines
// after it — is not here unless edges asks for it: splitTop keeps those as text,
// where they were. What is here is everything inside a block, located by path,
// with each index in a path followed to where the element it named went (see the
// note at the top of this file). A comment whose path does not exist in the new
// document is dropped, which is also what happens to the comment on a line that
// is gone.
func remapComments(cm yaml.CommentMap, rawOld, rawNew any, edges bool) yaml.CommentMap {
	paths := make([]string, 0, len(cm))
	for p := range cm {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	claimed := map[string]map[int]bool{}
	out := yaml.CommentMap{}
	for _, p := range paths {
		segs, ok := parseCommentPath(p)
		if !ok || len(segs) == 0 {
			continue
		}
		comments := cm[p]
		if len(segs) == 1 && !edges {
			comments = slices.DeleteFunc(slices.Clone(comments), func(c *yaml.Comment) bool {
				return c.Position != yaml.CommentLinePosition
			})
			if len(comments) == 0 {
				continue
			}
		}
		moved, ok := followPath(segs, rawOld, rawNew, claimed)
		if !ok {
			continue
		}
		if _, err := yaml.PathString(moved); err != nil {
			continue
		}
		out[moved] = append(out[moved], comments...)
	}
	return out
}

// pathSeg is one step of a comment path: a key, or an index into a list.
type pathSeg struct {
	key   string
	index int
	isIdx bool
}

// parseCommentPath reads goccy's path spelling — $.a.'b.c'.d[0].e — and says
// whether it understood it.
func parseCommentPath(p string) ([]pathSeg, bool) {
	rest, ok := strings.CutPrefix(p, "$")
	if !ok {
		return nil, false
	}
	var segs []pathSeg
	for rest != "" {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			if strings.HasPrefix(rest, "'") {
				end := strings.Index(rest[1:], "'")
				if end < 0 {
					return nil, false
				}
				segs = append(segs, pathSeg{key: rest[1 : 1+end]})
				rest = rest[end+2:]
				continue
			}
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				end = len(rest)
			}
			if end == 0 {
				return nil, false
			}
			segs = append(segs, pathSeg{key: rest[:end]})
			rest = rest[end:]
		case '[':
			end := strings.Index(rest, "]")
			if end < 0 {
				return nil, false
			}
			n, err := strconv.Atoi(rest[1:end])
			if err != nil || n < 0 {
				return nil, false
			}
			segs = append(segs, pathSeg{index: n, isIdx: true})
			rest = rest[end+1:]
		default:
			return nil, false
		}
	}
	return segs, true
}

// followPath walks segs down both documents at once and returns the path of the
// same place in the new one.
func followPath(segs []pathSeg, oldNode, newNode any, claimed map[string]map[int]bool) (string, bool) {
	var b strings.Builder
	b.WriteString("$")
	for _, s := range segs {
		if !s.isIdx {
			om, ok1 := oldNode.(map[string]any)
			nm, ok2 := newNode.(map[string]any)
			if !ok1 || !ok2 {
				return "", false
			}
			on, has1 := om[s.key]
			nn, has2 := nm[s.key]
			if !has1 || !has2 {
				return "", false
			}
			oldNode, newNode = on, nn
			switch {
			case strings.ContainsAny(s.key, "[]$'\\"):
				// A name the path grammar cannot carry.
				return "", false
			case strings.ContainsAny(s.key, ".*"):
				b.WriteString(".'" + s.key + "'")
			default:
				b.WriteString("." + s.key)
			}
			continue
		}
		ol, ok1 := oldNode.([]any)
		nl, ok2 := newNode.([]any)
		if !ok1 || !ok2 || s.index >= len(ol) {
			return "", false
		}
		at := b.String()
		if claimed[at] == nil {
			claimed[at] = map[int]bool{}
		}
		m, ok := matchElement(ol, nl, s.index, claimed[at])
		if !ok {
			return "", false
		}
		oldNode, newNode = ol[s.index], nl[m]
		b.WriteString("[" + strconv.Itoa(m) + "]")
	}
	return b.String(), true
}

// matchElement finds where ol[n] went in nl: the same index when it is still
// the same value, else wherever that value now is, else — for a tile, which
// `rta dashboard add` replaces in place to give it a span or an input — the same
// index while it is still the same tile. An element that was removed has no
// place and its comments go with it.
//
// Not "the same index when the list is as long as it was". That reads an
// element removed and another added as one edited, and moves the comment about
// the first onto the second — a note that says what a tile is for, sitting on a
// different tile, is worse than the note gone.
//
// claimed is the indexes of nl already taken by another element's comments, so
// two equal tiles each keep their own.
func matchElement(ol, nl []any, n int, claimed map[int]bool) (int, bool) {
	if n < len(nl) && !claimed[n] && reflect.DeepEqual(ol[n], nl[n]) {
		claimed[n] = true
		return n, true
	}
	for m := range nl {
		if !claimed[m] && reflect.DeepEqual(ol[n], nl[m]) {
			claimed[m] = true
			return m, true
		}
	}
	if n < len(nl) && !claimed[n] && sameTile(ol[n], nl[n]) {
		claimed[n] = true
		return n, true
	}
	return 0, false
}

// sameTile reports whether two list elements are the same dashboard tile, which
// is what Tile.Key says: the capability, and the connection it is pinned to.
func sameTile(a, b any) bool {
	am, ok1 := a.(map[string]any)
	bm, ok2 := b.(map[string]any)
	return ok1 && ok2 && am["id"] != nil &&
		reflect.DeepEqual(am["id"], bm["id"]) && reflect.DeepEqual(am["profile"], bm["profile"])
}

// topBlock is one top-level key of the file as text.
type topBlock struct {
	key string
	// head is the first of the comment lines directly above the key, with no
	// blank line between: the ones that are about it. line is the key's own, and
	// end one past the last line that belongs to it. Blank lines and detached
	// comments after a block are the file's and belong to no block.
	head, line, end int
}

// topDoc is a file cut into the blocks of its top-level keys.
type topDoc struct {
	lines  []string
	blocks []topBlock
	ok     bool
}

// topKey is a top-level key line: a bare or quoted key at column 0 and its
// colon. A key that holds a space bare (`my key:`) is not one rta's own file
// has, and a line that matches nothing makes the whole file unreadable as
// blocks (ok is false) rather than guessed at.
var topKey = regexp.MustCompile(`^("(?:[^"\\]|\\.)*"|'[^']*'|[^\s#'"\[\]{}&*!|>%@` + "`" + `:,-][^\s:#]*)\s*:(?:\s|$)`)

// splitTop cuts text into blocks by its column-0 keys.
//
// By text and not by parsing it, so that nothing between the blocks has to be
// understood to be kept. What it accepts is what a block mapping looks like at
// the top: keys at column 0, their content indented or a list written at the
// same column, comments and blank lines between. Anything else — a flow
// mapping, a second document, a directive — leaves ok false.
func splitTop(text string) topDoc {
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	doc := topDoc{lines: lines, ok: true}
	floor := headerLines(lines)
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "---") || strings.HasPrefix(line, "..."):
			if len(doc.blocks) > 0 {
				return doc
			}
			floor = max(floor, i+1)
		case line[0] == ' ' || line[0] == '\t':
			if len(doc.blocks) == 0 {
				doc.ok = false
				return doc
			}
			doc.blocks[len(doc.blocks)-1].end = i + 1
		default:
			if m := topKey.FindStringSubmatch(line); m != nil {
				if len(doc.blocks) > 0 {
					floor = max(floor, doc.blocks[len(doc.blocks)-1].end)
				}
				head := i
				for head > floor && strings.HasPrefix(strings.TrimRight(lines[head-1], "\r"), "#") {
					head--
				}
				doc.blocks = append(doc.blocks, topBlock{
					key: strings.Trim(m[1], `"'`), head: head, line: i, end: i + 1})
				continue
			}
			inBlock := len(doc.blocks) > 0
			if inBlock && (line[0] == '-' || line[0] == '}' || line[0] == ']') {
				doc.blocks[len(doc.blocks)-1].end = i + 1
				continue
			}
			doc.ok = false
			return doc
		}
	}
	return doc
}

// headerLines is how many lines at the top are one of rta's own headers, which
// are the file's and not a comment on its first key.
func headerLines(lines []string) int {
	for _, h := range []string{configHeader, legacyHeader} {
		want := strings.Split(strings.TrimSuffix(h, "\n"), "\n")
		if len(lines) >= len(want) && slices.Equal(lines[:len(want)], want) {
			return len(want)
		}
	}
	return 0
}

func (d topDoc) block(key string) *topBlock {
	for i := range d.blocks {
		if d.blocks[i].key == key {
			return &d.blocks[i]
		}
	}
	return nil
}

// insertionPoint is the line a block for key is put before: ahead of the first
// block the file has for a key that Config declares later, so a file rta
// writes keeps the order it always had; at the end of the last block when the
// file has none, and after everything when it has no blocks at all.
//
// beforeNote says the block it goes ahead of opens with comment lines, which
// stay with that block.
func (d topDoc) insertionPoint(key string) (at int, beforeNote bool) {
	rank := slices.Index(managedKeys, key)
	best, at := len(managedKeys), -1
	for _, b := range d.blocks {
		if r := slices.Index(managedKeys, b.key); r > rank && r < best {
			best, at, beforeNote = r, b.head, b.head < b.line
		}
	}
	if at >= 0 {
		return at, beforeNote
	}
	if len(d.blocks) > 0 {
		return d.blocks[len(d.blocks)-1].end, false
	}
	return len(d.lines), false
}
