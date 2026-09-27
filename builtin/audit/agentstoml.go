package audit

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/this-is-tobi/rta/pkg/findings"
)

// Codex keeps its MCP servers in TOML, the one client that does, and the
// file used to be listed here and never opened: `rta mcp install codex`
// writes ~/.codex/config.toml, the audit graded its permissions, and a
// [mcp_servers.github] table holding a plaintext token beside an unpinned npx
// read "no issues found". The same server written as JSON failed for the
// token and warned for the launch.
//
// So the TOML is read into the tree encoding/json produces — a table as a
// map[string]any, an array as []any, a string as a string — and handed to the
// same collectServers and the same grading. One walk over one shape rather
// than a second grader for Codex's table names, for the reason the package
// comment gives for walking shapes at all: a schema that moves keeps being
// found.
//
// A reader rather than a dependency: the language subset an agent's config
// uses is tables, dotted keys, strings, arrays and inline tables, and the
// binary does not grow by a TOML library to read four of its constructs.
// Numbers, booleans and dates are kept as the text they were written as,
// since nothing graded here reads one. What this does not read is refused as
// a whole — the file is then graded for its permissions alone and says so —
// rather than read in part, because a server list read in part is a clean
// bill for the servers that were not.

// auditAgentTOML walks one TOML file for the shapes worth grading.
func auditAgentTOML(r *agentReport, f agentFile) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		r.Add(grpAgentFiles, shortPath(f.path), findings.Warn,
			f.label+" config could not be read: "+findings.Clip(err.Error()), findings.Reference{})
		return
	}
	doc, err := tomlTree(string(data))
	if err != nil {
		// The point first: the compact row clips, and what was not graded is
		// the part a reader must not lose to the parser's complaint.
		r.Add(grpAgentFiles, shortPath(f.path), findings.Info,
			f.label+" config's servers and credentials were not graded, only its permissions — "+
				"it is TOML this audit does not read: "+findings.Clip(err.Error()), findings.Reference{})
		return
	}
	var servers []serverDecl
	collectServers(doc, &servers)
	gradeServers(r, f, servers)
}

// tomlTree reads text as TOML into the tree encoding/json would build.
func tomlTree(text string) (map[string]any, error) {
	p := &tomlParser{s: text}
	root := map[string]any{}
	current := root
	for {
		p.skipBlank(true)
		if p.done() {
			return root, nil
		}
		var err error
		if p.peek() == '[' {
			current, err = p.header(root)
		} else {
			err = p.keyValue(current)
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", p.line(), err)
		}
		p.skipBlank(false)
		if !p.done() && p.peek() != '\n' && p.peek() != '\r' {
			return nil, fmt.Errorf("line %d: unexpected %q after a value", p.line(), p.peek())
		}
	}
}

type tomlParser struct {
	s string
	i int
	// depth is how many arrays and inline tables the value being read sits
	// inside; see maxTOMLNesting.
	depth int
}

// maxTOMLNesting bounds how deep arrays and inline tables may nest, and how
// many parts one dotted key may have. Each level of the first is a frame of
// recursion, and a file of ten million `[` — ten megabytes in a config an
// agent with a shell can write — overflowed the stack, which Go does not
// recover from: the whole of rta exited mid-audit. Each part of the second is
// a table the walk for servers then descends, one frame a level just the
// same: `[a.a.a…]` two million parts deep is four megabytes the reader took
// in half a second and the walk never finished. encoding/json draws the same
// line for the JSON configs, deeper; an agent's config nests three levels.
const maxTOMLNesting = 128

// nest enters one level of array or inline table, refusing past the bound.
// The caller undoes it with p.depth--.
func (p *tomlParser) nest() error {
	if p.depth++; p.depth > maxTOMLNesting {
		return fmt.Errorf("arrays and inline tables nest deeper than %d", maxTOMLNesting)
	}
	return nil
}

func (p *tomlParser) done() bool { return p.i >= len(p.s) }
func (p *tomlParser) peek() byte { return p.s[p.i] }
func (p *tomlParser) line() int  { return strings.Count(p.s[:min(p.i, len(p.s))], "\n") + 1 }

// skipBlank skips spaces, tabs and a comment, and newlines too when lines is
// set — inside an array, or between statements.
func (p *tomlParser) skipBlank(lines bool) {
	for !p.done() {
		switch c := p.peek(); {
		case c == ' ' || c == '\t':
			p.i++
		case c == '#':
			for !p.done() && p.peek() != '\n' {
				p.i++
			}
		case lines && (c == '\n' || c == '\r'):
			p.i++
		default:
			return
		}
	}
}

// header reads a [table] or [[array.of.tables]] line and returns the table
// the lines below it fill.
func (p *tomlParser) header(root map[string]any) (map[string]any, error) {
	p.i++ // [
	array := !p.done() && p.peek() == '['
	if array {
		p.i++
	}
	p.skipBlank(false)
	path, err := p.key()
	if err != nil {
		return nil, err
	}
	p.skipBlank(false)
	closing := "]"
	if array {
		closing = "]]"
	}
	if !strings.HasPrefix(p.s[p.i:], closing) {
		return nil, fmt.Errorf("a table header is not closed")
	}
	p.i += len(closing)
	if !array {
		return descend(root, path)
	}
	parent, err := descend(root, path[:len(path)-1])
	if err != nil {
		return nil, err
	}
	last := path[len(path)-1]
	list, _ := parent[last].([]any)
	if _, exists := parent[last]; exists && list == nil {
		return nil, fmt.Errorf("%s is both a value and an array of tables", last)
	}
	table := map[string]any{}
	parent[last] = append(list, table)
	return table, nil
}

// descend walks path from t, making the tables it names, and returns the
// last. A path through an array of tables continues in its latest entry, as
// TOML defines.
func descend(t map[string]any, path []string) (map[string]any, error) {
	for _, k := range path {
		switch next := t[k].(type) {
		case nil:
			made := map[string]any{}
			t[k] = made
			t = made
		case map[string]any:
			t = next
		case []any:
			if len(next) == 0 {
				return nil, fmt.Errorf("%s is an empty array, not a table", k)
			}
			last, ok := next[len(next)-1].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not a table", k)
			}
			t = last
		default:
			return nil, fmt.Errorf("%s is both a value and a table", k)
		}
	}
	return t, nil
}

// keyValue reads one key = value into t.
func (p *tomlParser) keyValue(t map[string]any) error {
	path, err := p.key()
	if err != nil {
		return err
	}
	p.skipBlank(false)
	if p.done() || p.peek() != '=' {
		return fmt.Errorf("a key without a value")
	}
	p.i++
	p.skipBlank(false)
	val, err := p.value()
	if err != nil {
		return err
	}
	parent, err := descend(t, path[:len(path)-1])
	if err != nil {
		return err
	}
	parent[path[len(path)-1]] = val
	return nil
}

// key reads a dotted key — bare, "quoted" or 'literal' parts.
func (p *tomlParser) key() ([]string, error) {
	var path []string
	for {
		p.skipBlank(false)
		if p.done() {
			return nil, fmt.Errorf("a key is cut short")
		}
		var part string
		switch p.peek() {
		case '"':
			s, err := p.basic()
			if err != nil {
				return nil, err
			}
			part = s
		case '\'':
			s, err := p.literal()
			if err != nil {
				return nil, err
			}
			part = s
		default:
			start := p.i
			for !p.done() && bareKeyByte(p.peek()) {
				p.i++
			}
			if p.i == start {
				return nil, fmt.Errorf("unexpected %q where a key belongs", p.peek())
			}
			part = p.s[start:p.i]
		}
		path = append(path, part)
		if len(path) > maxTOMLNesting {
			return nil, fmt.Errorf("a dotted key nests deeper than %d", maxTOMLNesting)
		}
		p.skipBlank(false)
		if p.done() || p.peek() != '.' {
			return path, nil
		}
		p.i++
	}
}

func bareKeyByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// value reads a string, an array, an inline table, or any other scalar as the
// text it was written as.
func (p *tomlParser) value() (any, error) {
	if p.done() {
		return nil, fmt.Errorf("a value is missing")
	}
	switch p.peek() {
	case '"':
		return p.basic()
	case '\'':
		return p.literal()
	case '[':
		return p.array()
	case '{':
		return p.inlineTable()
	}
	start := p.i
	for !p.done() && !strings.ContainsRune(",]}#\r\n", rune(p.peek())) {
		p.i++
	}
	raw := strings.TrimSpace(p.s[start:p.i])
	if raw == "" {
		return nil, fmt.Errorf("a value is missing")
	}
	return raw, nil
}

func (p *tomlParser) array() ([]any, error) {
	defer func() { p.depth-- }()
	if err := p.nest(); err != nil {
		return nil, err
	}
	p.i++ // [
	out := []any{}
	for {
		p.skipBlank(true)
		if p.done() {
			return nil, fmt.Errorf("an array is not closed")
		}
		if p.peek() == ']' {
			p.i++
			return out, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.skipBlank(true)
		if !p.done() && p.peek() == ',' {
			p.i++
		}
	}
}

func (p *tomlParser) inlineTable() (map[string]any, error) {
	defer func() { p.depth-- }()
	if err := p.nest(); err != nil {
		return nil, err
	}
	p.i++ // {
	out := map[string]any{}
	for {
		p.skipBlank(false)
		if p.done() {
			return nil, fmt.Errorf("an inline table is not closed")
		}
		if p.peek() == '}' {
			p.i++
			return out, nil
		}
		if err := p.keyValue(out); err != nil {
			return nil, err
		}
		p.skipBlank(false)
		if !p.done() && p.peek() == ',' {
			p.i++
		}
	}
}

// basic reads a "basic string", or a """multi-line""" one, with its escapes.
func (p *tomlParser) basic() (string, error) {
	multi := strings.HasPrefix(p.s[p.i:], `"""`)
	if multi {
		p.i += 3
		p.skipNewline()
	} else {
		p.i++
	}
	var b strings.Builder
	for !p.done() {
		c := p.peek()
		switch {
		case multi && strings.HasPrefix(p.s[p.i:], `"""`):
			p.i += 3
			return b.String(), nil
		case !multi && c == '"':
			p.i++
			return b.String(), nil
		case !multi && c == '\n':
			return "", fmt.Errorf("a string is not closed")
		case c == '\\':
			if err := p.escape(&b, multi); err != nil {
				return "", err
			}
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	return "", fmt.Errorf("a string is not closed")
}

// escape reads one backslash escape into b.
func (p *tomlParser) escape(b *strings.Builder, multi bool) error {
	p.i++ // the backslash
	if p.done() {
		return fmt.Errorf("a string is not closed")
	}
	c := p.peek()
	p.i++
	switch c {
	case 'b':
		b.WriteByte('\b')
	case 't':
		b.WriteByte('\t')
	case 'n':
		b.WriteByte('\n')
	case 'f':
		b.WriteByte('\f')
	case 'r':
		b.WriteByte('\r')
	case 'e':
		b.WriteByte(0x1b)
	case '"', '\\':
		b.WriteByte(c)
	case 'u', 'U':
		width := 4
		if c == 'U' {
			width = 8
		}
		if p.i+width > len(p.s) {
			return fmt.Errorf("an escape is cut short")
		}
		n, err := strconv.ParseUint(p.s[p.i:p.i+width], 16, 32)
		if err != nil {
			return fmt.Errorf("an escape is not hexadecimal")
		}
		if n > unicode.MaxRune {
			return fmt.Errorf("an escape names no character")
		}
		p.i += width
		b.WriteRune(rune(n))
	case ' ', '\t', '\r', '\n':
		// A line-ending backslash in a multi-line string trims the newline
		// and the whitespace after it.
		if !multi {
			return fmt.Errorf("an escape %q is not TOML", c)
		}
		for !p.done() && strings.ContainsRune(" \t\r\n", rune(p.peek())) {
			p.i++
		}
	default:
		return fmt.Errorf("an escape %q is not TOML", c)
	}
	return nil
}

// literal reads a 'literal string', or a multi-line one between three single
// quotes: no escapes.
func (p *tomlParser) literal() (string, error) {
	quote := "'"
	if strings.HasPrefix(p.s[p.i:], "'''") {
		quote = "'''"
	}
	p.i += len(quote)
	if quote == "'''" {
		p.skipNewline()
	}
	end := strings.Index(p.s[p.i:], quote)
	if end < 0 || (quote == "'" && strings.Contains(p.s[p.i:p.i+end], "\n")) {
		return "", fmt.Errorf("a string is not closed")
	}
	s := p.s[p.i : p.i+end]
	p.i += end + len(quote)
	return s, nil
}

// skipNewline drops the newline straight after a multi-line string opens,
// which TOML does not count as part of the string.
func (p *tomlParser) skipNewline() {
	if strings.HasPrefix(p.s[p.i:], "\r\n") {
		p.i += 2
	} else if !p.done() && p.peek() == '\n' {
		p.i++
	}
}
