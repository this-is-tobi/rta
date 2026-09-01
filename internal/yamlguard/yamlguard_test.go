package yamlguard

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// aliasBomb builds a "billion laughs"-style bomb: 6 levels of 10x fan-out,
// well under 1 KB on disk, decoding (unguarded) into 1.1 million values.
// Large enough to prove decoding really would explode; small enough that
// proving it — by letting a decoder actually run — does not hang the machine
// running this test. A real attack goes further: two more levels took the
// measured policy-file case to 37.8 seconds and 34.7 GB.
func aliasBomb() string {
	var b strings.Builder
	b.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 6; i++ {
		fmt.Fprintf(&b, "a%d: &a%d [", i, i)
		for j := range 10 {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*a%d", i-1)
		}
		b.WriteString("]\n")
	}
	return b.String()
}

func TestRefuseAnchors(t *testing.T) {
	for _, c := range []struct {
		name string
		yaml string
		want bool
	}{
		{"an ordinary document passes", "output: pretty\ntheme: dark\n", false},
		{"an anchor is refused", "a: &x 1\nb: 2\n", true},
		{"an alias is refused", "a: &x 1\nb: *x\n", true},
		{"a merge key is refused", "base: &b {k: v}\nuse:\n  <<: *b\n", true},
		{"an expansion bomb is refused", aliasBomb(), true},
		{"unparseable YAML is left to the decoder", "\t\tnot: [yaml\n", false},
		{"empty is not an error", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := RefuseAnchors([]byte(c.yaml))
			if got := err != nil; got != c.want {
				t.Fatalf("RefuseAnchors returned %v, want refusal=%v", err, c.want)
			}
			if c.want && !errors.Is(err, ErrAnchors) {
				t.Fatalf("refusal did not wrap ErrAnchors: %v", err)
			}
		})
	}
}

// The property that makes this a fix rather than a slower way to hit the same
// wall: the refusal costs what parsing the bytes costs, not what expanding
// them would have.
func TestTheRefusalIsProportionalToTheBytesNotTheExpansion(t *testing.T) {
	done := make(chan error, 1)
	go func() { done <- RefuseAnchors([]byte(aliasBomb())) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the bomb was not refused")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RefuseAnchors did not return within 2s — it expanded the bomb instead of refusing it")
	}
}

// decoders are the packages that turn somebody else's YAML into rta's own
// structs. Named rather than derived, because the list is the point: the three
// files here are all written by somebody other than the person running rta —
// a config shared between machines, a policy file a cloned repository can
// ship, and a manifest from an index somebody else merges to — and every one
// of them is decoded before anything about it is trusted.
//
// A fourth package joining this list is a real decision: it means rta decodes
// YAML from a new source. Make it here, in one line, rather than discovering
// it later as a finding — which is exactly how the policy and plugindist
// entries arrived, two rounds after config was fixed on its own.
var decoders = []string{
	"internal/config",
	"internal/policy",
	"internal/plugindist",
}

// The guard only works if it runs, and it only runs if somebody remembers to
// call it. config had it for a full round while policy and plugindist — the
// two files reached from a *repository* rather than from the operator's own
// config directory — did not, because nothing checked.
//
// Parsed rather than grepped, for the same reason the sibling drift tests are:
// the comments around these calls name yaml.Unmarshal, and a comment naming
// the rule must not trip it.
func TestEveryYAMLDecodeIsGuarded(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	for _, pkg := range decoders {
		dir := filepath.Join(root, filepath.FromSlash(pkg))
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Errorf("%s: no Go files — did the package move? A renamed package "+
				"silently stops being checked, which is the one way this test lies.", pkg)
			continue
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatal(perr)
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				t.Fatal(rerr)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				decodes, guards := 0, 0
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					id, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					switch {
					case id.Name == "yaml" && strings.HasPrefix(sel.Sel.Name, "Unmarshal"),
						id.Name == "yaml" && sel.Sel.Name == "NewDecoder":
						decodes++
					case id.Name == "yamlguard" && sel.Sel.Name == "RefuseAnchors":
						guards++
					}
					return true
				})
				if decodes > 0 && guards == 0 {
					t.Errorf("%s:%d: %s decodes YAML from a file rta did not write and never calls "+
						"yamlguard.RefuseAnchors — a few hundred bytes of nested anchors expand into "+
						"tens of gigabytes during the decode, and the decode is where the expansion "+
						"happens, so the guard has to run before it.",
						filepath.ToSlash(rel), fset.Position(fn.Pos()).Line, fn.Name.Name)
				}
			}
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test's working directory")
		}
		dir = parent
	}
}
