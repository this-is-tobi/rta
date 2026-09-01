package plugin

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func noop(context.Context, Request) (view.View, error) { return view.Text{Body: "ok"}, nil }

func validPlugin() Plugin {
	return Plugin{
		Name:    "demo",
		Summary: "demo plugin",
		Capabilities: []Capability{
			{ID: "demo.item.list", Summary: "list items", Safety: Read, Run: noop},
			{ID: "demo.item.rm", Summary: "remove item", Safety: Destructive, Run: noop},
		},
	}
}

func TestValidateOK(t *testing.T) {
	if err := validPlugin().Validate(); err != nil {
		t.Fatalf("valid plugin rejected: %v", err)
	}
}

func TestValidateFailures(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Plugin)
		wantSub string
	}{
		{"bad name", func(p *Plugin) { p.Name = "Demo!" }, "lowercase"},
		{"no capabilities", func(p *Plugin) { p.Capabilities = nil }, "no capabilities"},
		{"bad id", func(p *Plugin) { p.Capabilities[0].ID = "demo" }, "segments"},
		{"too many segments", func(p *Plugin) { p.Capabilities[0].ID = "demo.a.b.c" }, "segments"},
		{"wrong namespace", func(p *Plugin) { p.Capabilities[0].ID = "other.item.list" }, "namespace"},
		{"missing summary", func(p *Plugin) { p.Capabilities[0].Summary = "" }, "summary"},
		{"bad safety", func(p *Plugin) { p.Capabilities[0].Safety = "yolo" }, "safety"},
		{"nil handler", func(p *Plugin) { p.Capabilities[0].Run = nil }, "handler"},
		{"dup id", func(p *Plugin) { p.Capabilities[1].ID = p.Capabilities[0].ID; p.Capabilities[1].Safety = Read }, "duplicate"},
		{"bad field", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "BadName", Type: String}}
		}, "field name"},
		// A real bug an audit found: two inputs sharing a
		// name used to validate cleanly, and declareFlags then registered
		// one pflag.Flag per input in declaration order — the second
		// AddFlag for the same name panics the whole process at startup,
		// for every command, not just the capability that declared it.
		{"dup field name", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "target", Type: String}, {Name: "target", Type: Int}}
		}, "twice"},
		{"unknown field type", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "port", Type: "integer"}}
		}, "unknown type"},
		// Live marks a Suggest for the deliberate-press channel, so each rule
		// is about the mark pointing at something that can answer it.
		{"live without suggest", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "bucket", Type: String, Live: true}}
		}, "no Suggest"},
		{"live beside options", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "bucket", Type: String, Live: true,
				Options: []string{"a"}, Suggest: func(context.Context, Request) []string { return nil }}}
		}, "beside Options"},
		{"live on an int", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "port", Type: Int, Live: true,
				Suggest: func(context.Context, Request) []string { return nil }}}
		}, "String box"},
		{"scope names no input", func(p *Plugin) { p.Capabilities[0].Scope = "nope" }, "names no input"},
		// Endpoint roles. Every one of these is refused at registration rather
		// than at dial time, because what goes wrong is *where a call goes*
		// and the operator diagnosing it holds a connection error naming
		// nothing.
		{"unknown endpoint role", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "host", Type: String, Local: true, Config: "host", Endpoint: "hsot"}}
		}, "does not recognise"},
		{"endpoint on a secret", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "host", Type: Secret, Local: true, Config: "host", Endpoint: EndpointAddress}}
		}, "not a credential"},
		{"endpoint without local", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "host", Type: String, Config: "host", Endpoint: EndpointAddress}}
		}, "point the operator's credential at a machine it chose"},
		{"port role on a string", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{
				{Name: "host", Type: String, Local: true, Config: "host", Endpoint: EndpointHost},
				{Name: "port", Type: String, Local: true, Config: "port", Endpoint: EndpointPort},
			}
		}, "a port is an Int"},
		{"two inputs claim one role", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{
				{Name: "addr", Type: String, Local: true, Config: "addr", Endpoint: EndpointAddress},
				{Name: "server", Type: String, Local: true, Config: "server", Endpoint: EndpointAddress},
			}
		}, "at most one input may take each"},
		// Half an address is the dangerous one: filled, it points the call at
		// 127.0.0.1 on the plugin's own default port, which is a live port on
		// the operator's machine often enough to connect to the wrong thing
		// rather than fail.
		{"host without port", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "host", Type: String, Local: true, Config: "host", Endpoint: EndpointHost}}
		}, "no input takes port"},
		{"endpoint without config", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "addr", Type: String, Local: true, Endpoint: EndpointAddress}}
		}, "was never offered"},
		// The tls role is the one where the host produces a value the plugin
		// must accept, so what it can say is pinned at registration rather
		// than discovered when the plugin rejects the word.
		{"tls role with no off spelling", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "sslmode", Type: String, Local: true,
				Config: "sslmode", Endpoint: EndpointTLS, Options: []string{"prefer", "require"}}}
		}, "no way to turn TLS off"},
		{"tls role on a wrong type", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "sslmode", Type: Int, Local: true,
				Config: "sslmode", Endpoint: EndpointTLS}}
		}, "must be one of those two types"},
		{"port without host", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: "port", Type: Int, Local: true, Config: "port", Endpoint: EndpointPort}}
		}, "no input takes host"},
		// Every identifier regex (nameRe, idRe, fieldRe, configKeyRe)
		// constrains character set but not length, so nothing before this
		// stopped a declaration built to be pathological rather than typed
		// by hand — a plugin author never writes a name this long, but
		// nothing checked that until now.
		{"plugin name too long", func(p *Plugin) {
			p.Name = strings.Repeat("a", maxIdentifier+1)
		}, "characters, want at most"},
		{"capability id too long", func(p *Plugin) {
			p.Capabilities[0].ID = "demo." + strings.Repeat("a", maxIdentifier)
		}, "characters, want at most"},
		{"field name too long", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{{Name: strings.Repeat("a", maxIdentifier+1), Type: String}}
		}, "characters, want at most"},
		{"config key too long", func(p *Plugin) {
			p.Capabilities[0].Inputs = []Field{
				{Name: "target", Type: String, Config: strings.Repeat("a", maxIdentifier+1)},
			}
		}, "characters, want at most"},
		// Unlike capability count (see text.go's own comment on why that is
		// deliberately not bounded here), one capability's own input list
		// and one field's own enum have no downstream mechanism that
		// absorbs an unbounded count the way many capabilities does.
		{"too many inputs", func(p *Plugin) {
			inputs := make([]Field, maxInputs+1)
			for i := range inputs {
				inputs[i] = Field{Name: fmt.Sprintf("f%d", i), Type: String}
			}
			p.Capabilities[0].Inputs = inputs
		}, "declares 65 inputs"},
		{"too many options", func(p *Plugin) {
			opts := make([]string, maxOptions+1)
			for i := range opts {
				opts[i] = fmt.Sprintf("o%d", i)
			}
			p.Capabilities[0].Inputs = []Field{{Name: "choice", Type: String, Options: opts}}
		}, "declares 257 options"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPlugin()
			tt.mutate(&p)
			err := p.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("want error containing %q, got %v", tt.wantSub, err)
			}
		})
	}
}

// Scope is what lets a grant narrow to one record instead of the whole
// capability, and it narrows by naming an input the caller filled in. A Scope
// naming nothing — "keys" where the field is "key" — leaves the gate with no
// value to match against, so every grant issued on that capability silently
// covers every record it can reach: exactly the "read the staging token" that
// turns out to mean "read every secret I own". Nothing
// else in the system would notice, because the grant still looks scoped in
// `rta grant list`.
func TestScopeMustNameADeclaredInput(t *testing.T) {
	p := validPlugin()
	p.Capabilities[0].Scope = "key"
	p.Capabilities[0].Inputs = []Field{{Name: "keys", Type: StringSlice, Help: "keys to read"}}

	err := p.Validate()
	if err == nil {
		t.Fatal(`a scope naming no declared input was accepted`)
	}
	if !strings.Contains(err.Error(), "names no input") {
		t.Errorf("the error should say why: %v", err)
	}

	// Spelled to match a declared input, the same capability is fine — the
	// rule rejects the dangling reference, not the act of scoping.
	p.Capabilities[0].Inputs = []Field{{Name: "key", Type: String, Help: "key to read"}}
	if err := p.Validate(); err != nil {
		t.Errorf("a scope naming a declared input was rejected: %v", err)
	}
}

// Field.Type was validated nowhere, and every surface switches on it with a
// default branch meaning "string". So a capability declaring Type: "integer" —
// JSON Schema's spelling, and the obvious thing to reach for — got a string
// --port flag, {"type": "string"} in its published MCP schema, and a handler
// whose req.Int("port") returned 0, with nothing anywhere reporting a problem.
func TestAnUnrecognisedFieldTypeIsRejected(t *testing.T) {
	declared := []FieldType{String, Int, Bool, Float, StringSlice, Text, Path, Secret}

	p := validPlugin()
	p.Capabilities[0].Inputs = []Field{{Name: "port", Type: "integer", Help: "port to probe"}}

	err := p.Validate()
	if err == nil {
		t.Fatal(`an input declaring Type "integer" was accepted`)
	}
	// An author who reached for the wrong spelling needs the right one, not a
	// verdict, so the rejection carries the whole accepted set. Only the half
	// after the offer is searched, since "integer" itself contains "int".
	_, accepted, ok := strings.Cut(err.Error(), "want one of ")
	if !ok {
		t.Fatalf("the rejection should offer the accepted types: %v", err)
	}
	for _, ft := range declared {
		if !strings.Contains(accepted, string(ft)) {
			t.Errorf("the rejection should name %q as an option: %v", ft, err)
		}
	}

	// Every declared type is accepted, so the rule catches misspellings rather
	// than narrowing the contract. The built-in catalogue uses all of these but
	// Float; it stays accepted because removing a declared constant from the
	// set is the same breaking change one direction over.
	for _, ft := range declared {
		p := validPlugin()
		p.Capabilities[0].Inputs = []Field{{Name: "input", Type: ft}}
		if err := p.Validate(); err != nil {
			t.Errorf("declared type %q was rejected: %v", ft, err)
		}
	}
}

func TestRequestAccessors(t *testing.T) {
	r := NewRequest(map[string]any{
		"s": "str", "i": 42, "i64": int64(7), "f": 3.5, "fi": 2,
		"b": true, "ss": []string{"a", "b"}, "ssa": []any{"x", 1},
	}, true, false)
	if r.String("s") != "str" || r.String("missing") != "" {
		t.Error("String accessor")
	}
	if r.Int("i") != 42 || r.Int("i64") != 7 || r.Int("missing") != 0 {
		t.Error("Int accessor")
	}
	if r.Float("f") != 3.5 || r.Float("fi") != 2 {
		t.Error("Float accessor")
	}
	if !r.Bool("b") || r.Bool("missing") {
		t.Error("Bool accessor")
	}
	if got := r.StringSlice("ss"); len(got) != 2 || got[0] != "a" {
		t.Error("StringSlice accessor")
	}
	if got := r.StringSlice("ssa"); len(got) != 2 || got[1] != "1" {
		t.Error("StringSlice from []any")
	}
	if !r.DryRun || r.Yes {
		t.Error("flags not carried")
	}
}

// A bare string in a StringSlice slot means one value, not none. An MCP
// client is not schema-checked before its arguments reach here (the SDK's
// own contract makes that the caller's responsibility), so {"key": "x"}
// instead of {"key": ["x"]} is a call this has to interpret the same way
// internal/grant does when it reads the identical raw value to decide what a
// grant covers — two readers disagreeing here is how a per-key grant on
// kv.env ends up exporting the whole store (see internal/grant/grant_test.go
// TestScalarAndSliceFormsAgreeOnScope for the sharp case).
func TestStringSliceAcceptsABareString(t *testing.T) {
	r := NewRequest(map[string]any{"tag": "solo", "empty": ""}, false, false)
	if got := r.StringSlice("tag"); len(got) != 1 || got[0] != "solo" {
		t.Errorf("StringSlice(scalar) = %v, want one value", got)
	}
	// An empty string is "nothing said", the same as an absent key — not a
	// list holding one empty entry.
	if got := r.StringSlice("empty"); got != nil {
		t.Errorf("StringSlice(\"\") = %v, want nil", got)
	}
	if got := r.StringSlice("missing"); got != nil {
		t.Errorf("StringSlice(missing) = %v, want nil", got)
	}
}

func TestWords(t *testing.T) {
	c := Capability{ID: "pg.table.list"}
	w := c.Words()
	if len(w) != 3 || w[0] != "pg" || w[2] != "list" {
		t.Errorf("Words() = %v", w)
	}
}

// TestFieldTypesCoversEveryDeclaredConstant walks the FieldType const block in
// the source and asserts fieldTypes lists all of it.
//
// A hand-written list of the constants — which is what the other test does,
// and what any test of a closed set naturally reaches for — only catches drift
// in one direction. Deleting a type from fieldTypes fails loudly. Adding a
// ninth FieldType constant and forgetting to add it to fieldTypes does not:
// the new type becomes a value Validate rejects, every surface grows a case
// for it that can never run, and the suite stays green. That is the specific
// failure a closed set invites, so the source is the only honest oracle.
func TestFieldTypesCoversEveryDeclaredConstant(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "plugin.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "FieldType" {
				continue
			}
			for _, name := range vs.Names {
				declared = append(declared, name.Name)
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no FieldType constants — the parse is wrong, not the code")
	}

	listed := map[FieldType]bool{}
	for _, ft := range fieldTypes {
		listed[ft] = true
	}
	byName := map[string]FieldType{
		"String": String, "Int": Int, "Bool": Bool, "Float": Float,
		"StringSlice": StringSlice, "Text": Text, "Path": Path, "Secret": Secret,
		"SecretSlice": SecretSlice,
	}
	for _, name := range declared {
		ft, known := byName[name]
		if !known {
			t.Fatalf("FieldType %s was added to plugin.go but not to this test's map, "+
				"so nothing checks whether fieldTypes lists it", name)
		}
		if !listed[ft] {
			t.Errorf("FieldType %s (%q) is declared but missing from fieldTypes, so Validate "+
				"rejects every plugin that uses it", name, ft)
		}
	}
	if len(declared) != len(fieldTypes) {
		t.Errorf("%d FieldType constants but %d in fieldTypes", len(declared), len(fieldTypes))
	}
}

// TestEndpointRolesCoversEveryDeclaredConstant is the same oracle as above,
// for the same reason, one closed set along.
//
// The drift it catches is worse here than for FieldType. A role declared in
// the source and missing from endpointRoles is refused by validate() — so a
// plugin declaring it fails to load, which is at least loud. The direction
// that is quiet is the tunnel filler: a role nothing fills is an input the
// host was asked to point at a forward and silently did not, and what the
// operator gets is a call that reaches the plugin's *default* host instead of
// the cluster. That is the "connects to the wrong thing rather than fails"
// failure checkEndpoints exists to prevent, arriving through the back door.
func TestEndpointRolesCoversEveryDeclaredConstant(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "plugin.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok || id.Name != "EndpointRole" {
				continue
			}
			declared = append(declared, vs.Names[0].Name)
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no EndpointRole constants — the parse is wrong, not the code")
	}
	byName := map[string]EndpointRole{
		"EndpointNone": EndpointNone, "EndpointHost": EndpointHost,
		"EndpointPort": EndpointPort, "EndpointAddress": EndpointAddress,
		"EndpointURL": EndpointURL, "EndpointTLS": EndpointTLS,
	}
	for _, name := range declared {
		role, known := byName[name]
		if !known {
			t.Fatalf("EndpointRole %s was added to plugin.go but not to this test's map, "+
				"so nothing checks whether endpointRoles lists it", name)
		}
		if !slices.Contains(endpointRoles, role) {
			t.Errorf("EndpointRole %s (%q) is declared but missing from endpointRoles, so "+
				"validate rejects every plugin that declares it", name, role)
		}
	}
	if len(declared) != len(endpointRoles) {
		t.Errorf("%d EndpointRole constants but %d in endpointRoles", len(declared), len(endpointRoles))
	}
}

// An input with no Type at all validated before, and behaved as a string on
// every surface. Rejecting it is the freeze-relevant half of the closed-set
// decision and the one most likely to be quietly reverted by the first
// "be lenient, default empty to String" patch — so it is pinned separately,
// and the message is checked for saying which mistake it was.
func TestAnInputMustDeclareItsType(t *testing.T) {
	p := validPlugin()
	p.Capabilities[0].Inputs = []Field{{Name: "host", Help: "host to reach"}}

	err := p.Validate()
	if err == nil {
		t.Fatal("an input declaring no type was accepted; Secret and Path are what make a " +
			"value masked and completable, and neither can be inferred from a name")
	}
	if !strings.Contains(err.Error(), "declares no type") {
		t.Errorf("omission and misspelling are different mistakes and need different "+
			"messages: %v", err)
	}
	if !strings.Contains(err.Error(), string(Secret)) {
		t.Errorf("the rejection should name the accepted types: %v", err)
	}
}

// A declared bound the host will never apply is worse than no bound: the
// author believes the input is clamped and stops checking it, and nothing
// anywhere says otherwise. All three forms are quiet — Resolve reads Min
// through toFloat and simply gets not-ok, clamping applies Min then Max so an
// inverted pair pins every value rather than erroring, and Resolve clamps no
// type but Int and Float.
func TestABoundTheHostCannotApplyIsRejected(t *testing.T) {
	tests := []struct {
		name    string
		field   Field
		wantSub string
	}{
		{"string min", Field{Name: "n", Type: Int, Min: "1"}, "non-numeric Min"},
		{"string max", Field{Name: "n", Type: Int, Max: "10"}, "non-numeric Max"},
		{"bool min", Field{Name: "n", Type: Float, Min: true}, "non-numeric Min"},
		{"inverted", Field{Name: "n", Type: Int, Min: 100, Max: 10}, "clamps to Max"},
		{"bound on a string", Field{Name: "n", Type: String, Max: 10}, "apply only to"},
		{"bound on a bool", Field{Name: "n", Type: Bool, Min: 0}, "apply only to"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPlugin()
			p.Capabilities[0].Inputs = []Field{tt.field}
			err := p.Validate()
			if err == nil {
				t.Fatalf("%+v was accepted", tt.field)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("want an error mentioning %q, got %v", tt.wantSub, err)
			}
		})
	}
}

// The rule must not cost the bounds people actually write. Every numeric Go
// type Resolve reads is accepted, a single-sided bound is accepted, and equal
// bounds are a legal way to pin a value to one number.
func TestOrdinaryBoundsAreAccepted(t *testing.T) {
	fields := []Field{
		{Name: "n", Type: Int, Min: 1, Max: 65535},
		{Name: "n", Type: Int, Min: int64(1)},
		{Name: "n", Type: Float, Max: 1.0},
		{Name: "n", Type: Float, Min: 0.0, Max: 100.0},
		{Name: "n", Type: Int, Min: 5, Max: 5},
		{Name: "n", Type: String, Help: "no bounds at all"},
	}
	for _, f := range fields {
		p := validPlugin()
		p.Capabilities[0].Inputs = []Field{f}
		if err := p.Validate(); err != nil {
			t.Errorf("%+v was rejected: %v", f, err)
		}
	}
}

// Config on a Secret is refused, and the message names the alternative.
//
// Six problems close on this one rule. A config file is plaintext, read on
// every invocation with nobody watching; a Secret's default is published
// verbatim in the MCP tool schema; the TUI would draw an empty password box
// over a value that is already known; and for kv specifically the passphrase
// is resolved by machinery that needs a TTY and a person, which argument
// resolution has neither. Local is the path that already works.
func TestConfigIsRefusedOnASecretInput(t *testing.T) {
	err := Plugin{
		Name: "pg", Summary: "postgres", Capabilities: []Capability{{
			ID: "pg.query", Summary: "query", Safety: Read,
			Run:    func(context.Context, Request) (view.View, error) { return nil, nil },
			Inputs: []Field{{Name: "password", Type: Secret, Help: "p", Config: "password"}},
		}},
	}.Validate()
	if err == nil {
		t.Fatal("a Secret input was allowed to be filled from config")
	}
	if !strings.Contains(err.Error(), "Local") {
		t.Errorf("error = %v, want it to name the alternative", err)
	}
}

// CLI positional arity is computed from Required, so an input that config
// might or might not have satisfied changes what an argument count means.
func TestConfigIsRefusedOnAPositionalInput(t *testing.T) {
	err := Plugin{
		Name: "pg", Summary: "postgres", Capabilities: []Capability{{
			ID: "pg.query", Summary: "query", Safety: Read,
			Run:    func(context.Context, Request) (view.View, error) { return nil, nil },
			Inputs: []Field{{Name: "sql", Type: String, Help: "s", Positional: true, Config: "sql"}},
		}},
	}.Validate()
	if err == nil {
		t.Fatal("a positional input was allowed to be filled from config")
	}
}

// The key grammar is closed: no leading dot, no empty segment, nothing that
// could be read as a filesystem path by whatever looks at it next.
func TestTheConfigKeyGrammarIsClosed(t *testing.T) {
	for _, key := range []string{".host", "host.", "a..b", "../../etc/passwd", "Host", "host name", "/host", ""} {
		if key == "" {
			continue // empty means "never", which is every input today
		}
		err := Plugin{
			Name: "pg", Summary: "postgres", Capabilities: []Capability{{
				ID: "pg.query", Summary: "query", Safety: Read,
				Run:    func(context.Context, Request) (view.View, error) { return nil, nil },
				Inputs: []Field{{Name: "host", Type: String, Help: "h", Config: key}},
			}},
		}.Validate()
		if err == nil {
			t.Errorf("config key %q was accepted", key)
		}
	}
	for _, key := range []string{"host", "tls.mode", "a.b.c", "pool-size"} {
		err := Plugin{
			Name: "pg", Summary: "postgres", Capabilities: []Capability{{
				ID: "pg.query", Summary: "query", Safety: Read,
				Run:    func(context.Context, Request) (view.View, error) { return nil, nil },
				Inputs: []Field{{Name: "host", Type: String, Help: "h", Config: key}},
			}},
		}.Validate()
		if err != nil {
			t.Errorf("config key %q was refused: %v", key, err)
		}
	}
}

// A need rta does not know is refused rather than ignored. Dropping it would
// leave a plugin declaring a requirement no surface can show and no operator
// can grant — which reads as "asked for and denied" and is really "never
// asked", the worst shape a permission can have.
func TestAnUnknownNeedIsRefused(t *testing.T) {
	p := Plugin{
		Name: "demo", Summary: "s",
		Needs:        []Need{"root"},
		Capabilities: []Capability{{ID: "demo.get", Summary: "g", Safety: Read}},
	}
	err := p.Validate()
	if err == nil {
		t.Fatal("a plugin declaring an unknown need registered")
	}
	if !strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("the refusal does not say what is accepted: %v", err)
	}
}

// A location asked for twice is asked for once, and saying so here is what
// keeps every count downstream honest: `rta plugin allow` would offer the
// operator one location on two rows, and an index manifest cannot carry the
// duplicate at all — so a plugin that registered with one would be a plugin
// nobody could publish.
func TestADuplicateNeedIsRefused(t *testing.T) {
	p := Plugin{
		Name: "demo", Summary: "s",
		Needs:        []Need{NeedKubeconfig, NeedKubeconfig},
		Capabilities: []Capability{{ID: "demo.get", Summary: "g", Safety: Read, Run: noop}},
	}
	err := p.Validate()
	if err == nil {
		t.Fatal("a plugin asking for one location twice registered")
	}
	if !strings.Contains(err.Error(), "duplicate need") {
		t.Errorf("the refusal does not name what is wrong: %v", err)
	}
}

// And a known one passes, so the check is a filter rather than a wall.
func TestADeclaredNeedIsAccepted(t *testing.T) {
	p := Plugin{
		Name: "demo", Summary: "s",
		Needs:        []Need{NeedKubeconfig},
		Capabilities: []Capability{{ID: "demo.get", Summary: "g", Safety: Read, Run: noop}},
	}
	if err := p.Validate(); err != nil {
		t.Errorf("a plugin declaring a known need was refused: %v", err)
	}
}
