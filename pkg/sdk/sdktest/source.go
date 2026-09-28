package sdktest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/spelling"
)

// --- (h) source -------------------------------------------------------------

// WithSource has Check read the plugin's Go source in dir as well as what it
// declares — "." from a test file beside the plugin's own — and hold it to
// RuleSpelling: no sentence the source spells out names a flag or an `rta …`
// command line, and every call it names through a naming helper is one its
// reader can make (see checkSource).
//
// An option rather than the default because Check is not always called from
// the plugin's own directory: rta runs it over every built-in from the
// package that builds its command line, whose source spells flags because it
// is the CLI.
//
// Every official plugin carried this scan as a copy of one test file before
// it was exported, held to the others by a drift gate, since a plugin module
// reaches nothing of rta but its released SDK.
func WithSource(dir string) Option {
	return func(c *config) { c.source = dir }
}

// checkSource holds the non-test Go files in dir to what checkSpelling holds
// the declaration to, for the text a declaration does not hold: the
// sentences a handler words at run time, and the calls it names through
// Surface's naming helpers. Read from source rather than from the answers,
// because most of what a handler says is worded about a connection that
// failed or a server's answer, which no test can provoke without the server —
// and "raise --limit" is wrong whichever surface reads it.
//
// What this cannot see is a built-in's command line in running prose — "run
// rta net dns" — since only rta's registry says which words after "rta" are a
// namespace, and a plugin has none to ask. In a code span it is held whoever
// declared it, a span opening on "rta " being a command line whatever follows.
//
// No Skip waives it: a sentence the scan misreads is one to word through the
// naming helpers, which is what the scan asks for anyway, and none of the
// official plugins' copies ever needed a waiver.
func checkSource(t reporter, p plugin.Plugin, dir string) {
	t.Helper()

	fset, files, err := parseSource(dir)
	if err != nil {
		t.Errorf("sdktest: %s: reading the source in %s: %v", RuleSpelling, dir, err)
		return
	}
	// A directory with nothing to read is a guard that passes everything: a
	// WithSource naming the wrong place would go green for good.
	if len(files) == 0 {
		t.Errorf("sdktest: %s: WithSource: %v", RuleSpelling, errNoSource(dir))
		return
	}
	sp := spelling.ForPlugin(p)
	for _, f := range files {
		for _, h := range spelledForATerminal(sp, f) {
			for _, hit := range h.hits {
				t.Errorf("sdktest: %s: %s spells what only a terminal can act on: …%s…; name a capability "+
					"and an input through req.Surface() (CapabilityName, InputName, Call), which spell them "+
					"for whichever surface reads the message", RuleSpelling, fset.Position(h.pos), hit)
			}
		}
		for _, c := range namedCalls(f) {
			for _, problem := range callProblems(p, c) {
				t.Errorf("sdktest: %s: %s: %s", RuleSpelling, fset.Position(c.pos), problem)
			}
		}
	}
}

// Sentence is one string a plugin's Go source spells out (Sentences).
type Sentence struct {
	// Pos is where it starts: the file, the line and the column.
	Pos token.Position
	// Text is the literal, or a sum of literals read as one sentence with
	// spelling.Operand in each other operand's place.
	Text string
}

// Sentences returns every sentence the non-test Go files in dir spell out,
// read the way WithSource reads them for RuleSpelling, for a rule of the
// plugin's own that the suite has none for: a product it must never call by
// another's name, a word its domain spells one way. What plugin.AskOperator
// is given is left out, being a command rather than a sentence.
//
// The reader rather than a copy of it, for the reason WithSource is here:
// a copy reads a sum of literals, or what AskOperator is given, the way it
// did the day it was copied, and a plugin's gate over its own words should
// read the sentences the suite holds. A directory with no source in it, or a
// file that does not parse, is an error, since a test reading no sentences
// passes whatever the source says.
func Sentences(dir string) ([]Sentence, error) {
	fset, files, err := parseSource(dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errNoSource(dir)
	}
	var out []Sentence
	for _, f := range files {
		for _, s := range sentences(f) {
			out = append(out, Sentence{Pos: fset.Position(s.pos), Text: s.text})
		}
	}
	return out, nil
}

// errNoSource is the error for a directory with no Go file outside its
// tests, worded for the author who named it.
func errNoSource(dir string) error {
	return fmt.Errorf("%s holds no Go source outside its tests; name the directory of the plugin's own "+
		".go files", dir)
}

// parseSource parses every file in dir that is not a test, whatever its
// build constraints: a sentence behind `//go:build windows` is read by
// somebody too.
func parseSource(dir string) (*token.FileSet, []*ast.File, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, nil, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, f)
	}
	return fset, files, nil
}

// sentence is the text of one string the source spells out, and where it
// starts.
type sentence struct {
	pos  token.Pos
	text string
}

// held is a sentence, and what in it only a terminal could act on.
type held struct {
	sentence
	hits []string
}

// spelledForATerminal returns the sentences in f that spell something only
// a terminal could act on, each with what was found in it.
//
// A sentence is text with a space in it: an argument handed to kubectl or
// pg_dump is a literal too, "--namespace=" and "--no-owner", and is that
// program's spelling.
//
// Each is read as text only a terminal reads (Find's terminalOnly), because
// a handler words one of rta's own commands — `rta doctor`, `rta explain` —
// in the branch that asked its surface for the CLI, and no scan of the
// source can see which branch a literal sits in. A command in a namespace,
// the plugin's or a built-in's, is held all the same: the naming helpers
// spell any capability for whichever surface reads it, `rta net dns` among
// them, so a handler has no reason to write one out.
func spelledForATerminal(sp spelling.Speller, f *ast.File) []held {
	var out []held
	for _, s := range sentences(f) {
		if !strings.Contains(s.text, " ") {
			continue
		}
		if hits := sp.Find(s.text, true); len(hits) > 0 {
			out = append(out, held{s, hits})
		}
	}
	return out
}

// sentences returns every string the source spells out: a literal on its
// own, and a sum of literals and other operands as one sentence, with
// spelling.Operand in each other operand's place. One sentence rather than
// its pieces, because a code span opened in one literal and closed in the
// next — "`createdb --host=" + host + "`" — is only a span read whole, and
// read piece by piece the flag inside it looked like prose.
//
// What plugin.AskOperator is given is left out: the command it hands the
// operator is the one command line an agent may read. Known by the name the
// file imports pkg/plugin under, so an author who imports it as sdk is read
// as one who imports it as plugin.
func sentences(f *ast.File) []sentence {
	pkg := importName(f, "github.com/this-is-tobi/rta/pkg/plugin", "plugin")
	var out []sentence
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "AskOperator" {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == pkg {
					return false
				}
			}
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "AskOperator" && pkg == "." {
				return false
			}
		case *ast.BinaryExpr:
			if n.Op != token.ADD {
				return true
			}
			var parts []ast.Expr
			var flatten func(e ast.Expr)
			flatten = func(e ast.Expr) {
				if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
					flatten(b.X)
					flatten(b.Y)
					return
				}
				parts = append(parts, e)
			}
			flatten(n)
			var text strings.Builder
			spelled := false
			for _, p := range parts {
				if s, ok := stringLit(p); ok {
					text.WriteString(s)
					spelled = true
					continue
				}
				text.WriteString(spelling.Operand)
				ast.Inspect(p, visit)
			}
			if spelled {
				out = append(out, sentence{n.Pos(), text.String()})
			}
			return false
		case *ast.BasicLit:
			if s, ok := stringLit(n); ok {
				out = append(out, sentence{n.Pos(), s})
			}
		}
		return true
	}
	ast.Inspect(f, visit)
	return out
}

// importName is the name f refers to the package at path by: the name it
// was imported under — "." for one imported into the file's own scope — or
// fallback, the package's own, when it was not renamed.
func importName(f *ast.File, path, fallback string) string {
	for _, spec := range f.Imports {
		if p, err := strconv.Unquote(spec.Path.Value); err != nil || p != path {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return fallback
	}
	return fallback
}

// namingHelpers are the plugin.Surface methods that name a capability by the
// ID they are given first.
var namingHelpers = map[string]bool{"CapabilityName": true, "CapabilityWith": true, "Call": true}

// namedCall is a capability a naming helper was given as a literal, with the
// inputs named beside it: CapabilityWith's by name, and Call's as the
// plugin.Arg literals written in the call, each by its place or as a flag.
type namedCall struct {
	pos    token.Pos
	helper string
	id     string
	inputs []namedInput
}

type namedInput struct {
	name       string
	positional bool
}

// namedCalls returns every call to a naming helper in f whose capability is a
// literal. What is not one — an ID in a variable, the Args of a slice built
// beforehand — is left out rather than guessed at.
func namedCalls(f *ast.File) []namedCall {
	var out []namedCall
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !namingHelpers[sel.Sel.Name] {
			return true
		}
		id, ok := stringLit(call.Args[0])
		if !ok {
			return true
		}
		c := namedCall{pos: call.Pos(), helper: sel.Sel.Name, id: id}
		for _, a := range call.Args[1:] {
			if name, ok := stringLit(a); ok {
				c.inputs = append(c.inputs, namedInput{name: name})
			} else if in, ok := argLit(a); ok {
				c.inputs = append(c.inputs, in)
			}
		}
		out = append(out, c)
		return true
	})
	return out
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// argLit reads a plugin.Arg literal's Name, and whether it says Positional.
func argLit(e ast.Expr) (namedInput, bool) {
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return namedInput{}, false
	}
	var in namedInput
	named := false
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name":
			in.name, named = stringLit(kv.Value)
		case "Positional":
			v, ok := kv.Value.(*ast.Ident)
			in.positional = ok && v.Name == "true"
		}
	}
	return in, named
}

// callProblems returns what is wrong with c, when it names a capability in
// p's namespace: an ID p does not declare, an input the capability does not
// declare, or one given where the CLI does not take it. CapabilityWith
// spells every input it names as a flag, so a Positional one there is given
// where the command refuses it too. A capability of another namespace,
// rta's own net.dns among them, only the host's registry knows.
//
// A call naming what it cannot make is one its reader cannot make either:
// Call spells what it is told, so an input given in the wrong place reads as
// a command line the CLI refuses with core.usage, and an ID nothing declares
// as a command it does not have. Receipts and hints once named `rta cnpg
// backup list shop`, `rta qdrant restore docs <file>` and a Galera node's
// `cluster` command, each refused by the CLI it was written for, and the
// helpers would have spelled the same mistakes just as faithfully.
//
// An input the capability does not declare is still one its command takes
// when it is one of the host's switches (spelling.HostSwitches), given as a
// flag: `--output json` is every capability's.
func callProblems(p plugin.Plugin, c namedCall) []string {
	if ns, _, _ := strings.Cut(c.id, "."); ns != p.Name {
		return nil
	}
	at := slices.IndexFunc(p.Capabilities, func(cp plugin.Capability) bool { return cp.ID == c.id })
	if at < 0 {
		return []string{c.helper + " names " + c.id + ", which this plugin does not declare"}
	}
	cp := p.Capabilities[at]
	var out []string
	for _, in := range c.inputs {
		if slices.Contains(spelling.HostSwitches(), in.name) && !in.positional {
			continue
		}
		i := slices.IndexFunc(cp.Inputs, func(f plugin.Field) bool { return f.Name == in.name })
		switch {
		case i < 0:
			out = append(out, c.helper+" gives "+c.id+" "+strconv.Quote(in.name)+", an input it does not declare")
		case cp.Inputs[i].Positional && !in.positional:
			out = append(out, c.helper+" gives "+c.id+"'s "+strconv.Quote(in.name)+
				" as a flag, where the CLI takes it by its place")
		case !cp.Inputs[i].Positional && in.positional:
			out = append(out, c.helper+" gives "+c.id+"'s "+strconv.Quote(in.name)+
				" by its place, where the CLI takes it as a flag")
		}
	}
	return out
}
