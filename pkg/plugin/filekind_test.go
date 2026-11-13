package plugin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

// The closed set of what a setting's file holds, held to its list the way the
// endpoint roles are: a kind declared and missing from fileKinds is refused by
// validate, so every plugin declaring it would fail to load.
func TestFileKindsCoversEveryDeclaredConstant(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "plugin.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]FileKind{"FileNone": FileNone, "FileCertificates": FileCertificates}
	declared := 0
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if id, typed := vs.Type.(*ast.Ident); !ok || !typed || id.Name != "FileKind" {
				continue
			}
			declared++
			kind, known := byName[vs.Names[0].Name]
			if !known {
				t.Fatalf("FileKind %s was added to plugin.go but not to this test's map", vs.Names[0].Name)
			}
			if !slices.Contains(fileKinds, kind) {
				t.Errorf("FileKind %s is declared but missing from fileKinds", vs.Names[0].Name)
			}
		}
	}
	if declared == 0 || declared != len(fileKinds) {
		t.Errorf("%d FileKind constants but %d in fileKinds", declared, len(fileKinds))
	}
}

// A setting declares the file it names holds certificates only where the
// declaration can mean something: a Local String with a Config key, which a
// profile fills. Anywhere else the plugin fails to load, saying why.
func TestAFileSettingIsALocalStringWithAConfigKey(t *testing.T) {
	declare := func(f Field) error {
		p := validPlugin()
		p.Capabilities[0].Inputs = []Field{f}
		return p.Validate()
	}
	ca := Field{Name: "ca-file", Type: String, Help: "CA", Local: true, Config: "ca-file", File: FileCertificates}
	if err := declare(ca); err != nil {
		t.Fatalf("a CA setting: %v", err)
	}
	path, caller, unkeyed, unknown := ca, ca, ca, ca
	path.Type = Path
	caller.Local = false
	unkeyed.Config = ""
	unknown.File = "keys"
	for name, f := range map[string]Field{"a Path": path, "a caller's String": caller, "no Config key": unkeyed} {
		if err := declare(f); err == nil || !strings.Contains(err.Error(), "a Local String with a Config key") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := declare(unknown); err == nil || !strings.Contains(err.Error(), `"keys"`) {
		t.Errorf("an unknown kind: %v", err)
	}
}

// The SDK's own CA setting says what it holds, so every plugin built on
// Service can take its CA from where the CA is kept.
func TestTheServicesCAFileHoldsCertificates(t *testing.T) {
	for _, f := range redisService.Fields() {
		want := FileNone
		if f.Name == "ca-file" {
			want = FileCertificates
		}
		if f.File != want {
			t.Errorf("%s declares File %q, want %q", f.Name, f.File, want)
		}
	}
}
