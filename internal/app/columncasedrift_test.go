package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// columnCaseExempt is the one place a built-in names a column in anything but
// Title Case: `rta agent log`, whose columns are the record's own field names
// (seq, outcome, authorized). `--after` exists so that a log shipper can read
// the table from a cursor, and it reads those names; renaming one to match the
// rest of the catalogue would break whatever matches on it, for a header the
// pretty renderer upper-cases for a person anyway. Keyed by file and by the
// function that declares the columns, so a lower-case column added to any other
// function of that file is still caught.
var columnCaseExempt = map[string]string{
	"builtin/agent/agent.go": "logColumns",
}

// titleCaseViolation is the first word of a column name that does not begin with
// a capital letter, or "" when the name is Title Case.
//
// A word is what lies between spaces, so a hyphenated compound ("Sub-note") is
// one word and its first letter is the one that counts; a word that opens with
// anything but a lower-case letter ("(bits)", "°C", "%") is not held to it. The
// rule is the one the catalogue keeps — `Expires In`, `Would Do`, `No Grant` —
// and the one the docs state for every built-in table.
func titleCaseViolation(name string) string {
	for _, word := range strings.Fields(name) {
		first := []rune(word)[0]
		if unicode.IsLetter(first) && unicode.IsLower(first) {
			return word
		}
	}
	return ""
}

// columnName is the name a view.Column composite literal gives, and whether it
// is spelled as a string literal at all (a name that is computed is no name this
// scan can read).
func columnName(lit *ast.CompositeLit) (string, bool) {
	value := ast.Expr(nil)
	for i, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Name" {
				value = kv.Value
			}
		} else if i == 0 {
			value = elt
		}
	}
	basic, ok := value.(*ast.BasicLit)
	if !ok || basic.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(basic.Value)
	return name, err == nil
}

func isViewColumn(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "view" && sel.Sel.Name == "Column"
}

// columnLiterals are the view.Column literals under node: the ones written out
// as `view.Column{...}` and the elements of a `[]view.Column{ {...}, {...} }`,
// which leave the type off.
func columnLiterals(node ast.Node) []*ast.CompositeLit {
	var out []*ast.CompositeLit
	ast.Inspect(node, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if isViewColumn(lit.Type) {
			out = append(out, lit)
		} else if list, ok := lit.Type.(*ast.ArrayType); ok && isViewColumn(list.Elt) {
			for _, elt := range lit.Elts {
				if col, ok := elt.(*ast.CompositeLit); ok && col.Type == nil {
					out = append(out, col)
				}
			}
		}
		return true
	})
	return out
}

// A column's name is the same word for the same field wherever it appears, so a
// script reading `-o csv` or `columns[].name` meets one spelling. The 45 that
// were lower case were found by reading, and the next one is added by someone
// who copied the table beside it.
//
// The tables of the built-in plugins and the ones this package draws itself
// (plugin list, the install report, the dashboard's) are one catalogue to the
// person reading them, so both are scanned.
func TestEveryBuiltinColumnIsNamedInTitleCase(t *testing.T) {
	fset := token.NewFileSet()
	var seen, exempted int
	for _, root := range []string{"../../builtin", "."} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(filepath.ToSlash(path), "../../")
			for _, decl := range file.Decls {
				enclosing := ""
				if fn, ok := decl.(*ast.FuncDecl); ok {
					enclosing = fn.Name.Name
				}
				for _, lit := range columnLiterals(decl) {
					name, literal := columnName(lit)
					if !literal {
						continue
					}
					seen++
					word := titleCaseViolation(name)
					switch {
					case word == "":
					case columnCaseExempt[rel] == enclosing:
						exempted++
					default:
						t.Errorf("%s:%d: column %q is not Title Case (%q): name it `%s`, "+
							"as every other built-in table does", rel, fset.Position(lit.Pos()).Line, name, word,
							strings.Join(strings.Fields(capitalised(name)), " "))
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// A scan that finds nothing passes whatever the catalogue says, and one that
	// stops seeing the log's columns has lost the place the exemption is for.
	if seen < 150 || exempted < 10 {
		t.Errorf("the scan read %d column names, %d of them the log's own: it no longer reaches the tables", seen, exempted)
	}
}

// capitalised is name with the first letter of each space-separated word upper-cased.
func capitalised(name string) string {
	words := strings.Fields(name)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// The rule above is held to what it exists to catch and what it must let through.
func TestTheColumnCaseRuleCatchesWhatItExistsFor(t *testing.T) {
	for name, want := range map[string]string{
		"owner": "owner", "Used by": "by", "Certificate expires": "expires", "would Do": "would",
		"Expires In": "", "Use%": "", "CPU%": "", "ID": "", "Entropy (bits)": "", "°C": "", "Sub-note": "",
	} {
		if got := titleCaseViolation(name); got != want {
			t.Errorf("titleCaseViolation(%q) = %q, want %q", name, got, want)
		}
	}
}
