package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/this-is-tobi/rta/internal/near"
)

// Finding is one key in the config file that rta does not read.
//
// The decoder drops a key it has no field for without a word, so `oputput:
// json` is a valid file that sets nothing and `rta doctor` used to call it ok.
// Naming it is the whole job: the operator's file looks right to them, and a
// setting that silently does nothing is the failure a typo is.
type Finding struct {
	// Key is the dotted path as `rta config get` spells it: dashboard.colums.
	Key string
	// Line is where the key is written, counted from 1.
	Line   int
	Reason string
	Hint   string
}

func (f Finding) String() string {
	where := f.Key
	if f.Line > 0 {
		where = fmt.Sprintf("%s (line %d)", f.Key, f.Line)
	}
	if f.Hint == "" {
		return where + ": " + f.Reason
	}
	return fmt.Sprintf("%s: %s (%s)", where, f.Reason, f.Hint)
}

// Check reads the config file as it is now and reports the keys in it that
// rta ignores, in the order the file has them. A file that does not exist has
// none, and one that does not parse has none either: LoadFile names that fault
// with the parser's own line, and listing every key of a file nothing could
// read would be noise beside it.
func Check() ([]Finding, error) {
	data, err := ReadText()
	if err != nil || data == nil {
		return nil, err
	}
	return CheckText(data), nil
}

// CheckText is Check over text already in hand: what `rta config edit` holds
// before it has put anything on disk.
//
// Two blocks are left to the readers that already answer for them: profiles,
// whose loader names an unknown key and tells a pre-environment shape from a
// typo (Profile.UnknownKeys), and theme, whose slots theme.Apply reports one by
// one. Naming them here as well would say each of those twice in `rta doctor`.
// What is checked is everything else — the top level, the dashboard, its tiles,
// each role — and the shape of a plugins: section, whose own keys are what each
// plugin declares and so are internal/pluginconf's to judge.
func CheckText(data []byte) []Finding {
	file, err := parser.ParseBytes(trimBOM(data), 0)
	if err != nil || len(file.Docs) == 0 || file.Docs[0].Body == nil {
		return nil
	}
	var found []Finding
	walkShape(file.Docs[0].Body, fileShape(), "", &found)
	sort.SliceStable(found, func(i, j int) bool { return found[i].Line < found[j].Line })
	return found
}

// shape is what the file may say at one point, read off the typed structs
// rather than listed beside them: a field added to Config is a known key from
// the moment it exists, and a key removed stops being one, with nothing here to
// remember.
type shape struct {
	// names are a struct's keys in the order it declares them, which is the
	// order a hint lists them in.
	names  []string
	fields map[string]*shape
	// each is the shape of every value of a map whose keys are the operator's
	// own (a role's name, a plugin's namespace); elem is a list's element.
	each, elem *shape
}

var checkedElsewhere = map[string]bool{"profiles": true, "theme": true}

var fileShape = sync.OnceValue(func() *shape {
	return shapeOf(reflect.TypeOf(Config{}), checkedElsewhere)
})

func shapeOf(t reflect.Type, opaque map[string]bool) *shape {
	switch t.Kind() {
	case reflect.Struct:
		s := &shape{fields: map[string]*shape{}}
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if !f.IsExported() || name == "" || name == "-" {
				continue
			}
			s.names = append(s.names, name)
			if opaque[name] {
				s.fields[name] = &shape{}
				continue
			}
			s.fields[name] = shapeOf(f.Type, nil)
		}
		return s
	case reflect.Slice:
		return &shape{elem: shapeOf(t.Elem(), nil)}
	case reflect.Map:
		if t.Elem().Kind() == reflect.Struct {
			return &shape{each: shapeOf(t.Elem(), nil)}
		}
		if t.Elem().Kind() == reflect.Map {
			return &shape{each: &shape{}}
		}
	}
	return &shape{}
}

func walkShape(n ast.Node, s *shape, path string, out *[]Finding) {
	switch v := n.(type) {
	case *ast.MappingNode:
		for _, pair := range v.Values {
			walkPair(pair, s, path, out)
		}
	case *ast.MappingValueNode:
		walkPair(v, s, path, out)
	case *ast.SequenceNode:
		if s.elem == nil {
			return
		}
		for i, e := range v.Values {
			walkShape(e, s.elem, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

func walkPair(pair *ast.MappingValueNode, s *shape, path string, out *[]Finding) {
	key := pair.Key.GetToken().Value
	full := key
	if path != "" {
		full = path + "." + key
	}
	switch {
	case s.each != nil:
		walkShape(pair.Value, s.each, full, out)
	case s.fields != nil:
		child, known := s.fields[key]
		if !known {
			*out = append(*out, unknownKey(pair, s, path, full))
			return
		}
		walkShape(pair.Value, child, full, out)
	}
}

// unknownKey is the finding for a key its block does not have, with the most
// useful thing to say about it: that it is a dotted path written flat, which is
// the shape `rta explain` prints keys in and the file does not take; the
// neighbour it is one slip from; or the block it belongs in.
func unknownKey(pair *ast.MappingValueNode, s *shape, path, full string) Finding {
	key := pair.Key.GetToken().Value
	f := Finding{Key: full, Line: pair.Key.GetToken().Position.Line, Reason: "is not a key rta reads"}
	if head, _, dotted := strings.Cut(key, "."); dotted && s.fields[head] != nil {
		f.Reason = "is a dotted path written as one key, and the file nests it"
		f.Hint = nestedForm(pair, path)
		return f
	}
	if guess := near.Word(key, s.names); guess != "" {
		f.Hint = fmt.Sprintf("did you mean %q?", guess)
		return f
	}
	if home, ok := keyHomes()[key]; ok && home != path {
		f.Hint = key + " belongs at the top level of the file"
		if home != "" {
			f.Hint = fmt.Sprintf("%s belongs under `%s:`", key, home)
		}
		return f
	}
	f.Hint = "keys here: " + strings.Join(s.names, ", ")
	return f
}

// nestedForm spells a dotted key as the block that states it, in the one-line
// form a person can paste, and the command that writes it. The value is a
// placeholder and never the one in the file: this runs over a file somebody
// else may have written, and a misplaced `plugins.pg.password: …` is exactly
// the line whose value must not be printed into a report.
func nestedForm(pair *ast.MappingValueNode, path string) string {
	dotted := pair.Key.GetToken().Value
	parts := strings.Split(dotted, ".")
	nest := "<value>"
	for i := len(parts) - 1; i >= 0; i-- {
		nest = parts[i] + ": " + nest
		if i > 0 {
			nest = "{" + nest + "}"
		}
	}
	line := "write it nested, `" + nest + "`"
	if path == "" {
		line += ", or let `rta config set " + dotted + " <value>` do it"
	}
	return line
}

// keyHomes maps a key to the one block that has it, for a key written in the
// wrong one: `columns:` at the top of the file belongs under `dashboard:`.
// Only the blocks that are mappings of fixed keys count; the name a key has in
// two of them is no answer.
var keyHomes = sync.OnceValue(func() map[string]string {
	homes := map[string]string{}
	clash := map[string]bool{}
	add := func(block string, s *shape) {
		for _, name := range s.names {
			if _, taken := homes[name]; taken {
				clash[name] = true
			}
			homes[name] = block
		}
	}
	root := fileShape()
	add("", root)
	for _, name := range root.names {
		if child := root.fields[name]; child.fields != nil {
			add(name, child)
		}
	}
	for name := range clash {
		delete(homes, name)
	}
	return homes
})

// KeyLines is the line each key of the text is written on, by its dotted path:
// output, dashboard.columns, plugins.http.timeout, and dashboard.add[1].id for
// a key inside a list. A key written flat, with a dot in its name, has the
// path it spells. What a reader that keeps no position of its own — the
// palette, a plugin's declaration — names a problem by, to be given a line.
func KeyLines(data []byte) map[string]int {
	lines := map[string]int{}
	file, err := parser.ParseBytes(trimBOM(data), 0)
	if err != nil || len(file.Docs) == 0 || file.Docs[0].Body == nil {
		return lines
	}
	var walk func(n ast.Node, path string)
	walk = func(n ast.Node, path string) {
		switch v := n.(type) {
		case *ast.MappingNode:
			for _, pair := range v.Values {
				walk(pair, path)
			}
		case *ast.MappingValueNode:
			key := v.Key.GetToken().Value
			if path != "" {
				key = path + "." + key
			}
			if _, seen := lines[key]; !seen {
				lines[key] = v.Key.GetToken().Position.Line
			}
			walk(v.Value, key)
		case *ast.SequenceNode:
			for i, e := range v.Values {
				walk(e, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(file.Docs[0].Body, "")
	return lines
}
