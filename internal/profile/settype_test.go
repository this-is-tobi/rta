package profile

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// **A `set:` value of the wrong shape is read as the zero, not ignored.**
//
// Every other problem this package reports is about a value nothing reads.
// This one is about a value that *is* read, as the opposite of what the file
// says — and YAML makes it easy to reach without noticing: `tls: "true"` is a
// string because somebody quoted it, and `tls: yes` is a string because YAML
// 1.2 stopped treating it as a boolean. Both leave the connection running
// without the transport security its own configuration states, while
// `rta profile list` said `ok`.

func tlsRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "db", Summary: "db", Capabilities: []plugin.Capability{{
			ID: "db.status", Summary: "status", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Config: "host", Local: true,
					Endpoint: plugin.EndpointHost},
				{Name: "port", Type: plugin.Int, Default: 5432, Config: "port", Local: true,
					Endpoint: plugin.EndpointPort},
				{Name: "tls", Type: plugin.Bool, Default: true, Config: "tls", Local: true},
				{Name: "mode", Type: plugin.String, Config: "mode", Local: true,
					Options: []string{"fast", "safe"}},
				{Name: "level", Type: plugin.Int, Config: "level", Local: true,
					Options: []string{"1", "2", "3"}},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestASetValueTheHandlerWouldReadAsZeroIsReported(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"quoted bool", "        set:\n          tls: \"true\"\n"},
		{"bare yes", "        set:\n          tls: yes\n"},
		{"quoted int", "        set:\n          port: \"5432\"\n"},
		{"stated with nothing after it", "        set:\n          tls:\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := load(t, "profiles:\n  staging:\n    plugins:\n      db:\n"+tc.body)
			problems := Check(cfg, tlsRegistry(t))
			if len(problems) == 0 {
				t.Fatal("accepted — the handler reads the zero and the profile reports `ok`")
			}
			if problems[0].Plugin != "db" {
				t.Errorf("the problem does not say which entry: %+v", problems[0])
			}
			if problems[0].Hint == "" {
				t.Error("no hint — told they are wrong, not how to be right")
			}
		})
	}
}

// The value is not echoed. This reads the operator's own file, and the same
// function runs over every `set:` key in it.
func TestTheReportDoesNotEchoTheStatedValue(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      db:
        set:
          tls: hunter2-not-a-bool
`)
	for _, p := range Check(cfg, tlsRegistry(t)) {
		if strings.Contains(p.Reason+p.Hint, "hunter2") {
			t.Errorf("the stated value reached the report: %+v", p)
		}
	}
}

// The correctly-typed file stays correct, and the values still arrive. A
// check that reported every connection would be indistinguishable from one
// that reported none.
func TestACorrectlyTypedConnectionIsAcceptedAndArrives(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      db:
        set:
          host: db.internal
          port: 6432
          tls: false
`)
	reg := tlsRegistry(t)
	if problems := Check(cfg, reg); len(problems) != 0 {
		t.Fatalf("a correctly typed connection was reported: %v", problems)
	}
	c := reg.Capabilities()[0]
	bound := Bind("staging", cfg.Profiles["staging"].Plugins["db"], c,
		func(string) (string, bool) { return "", false })
	req := plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Profile: bound, ProfileName: "staging"}), false, true)
	if req.String("host") != "db.internal" || req.Int("port") != 6432 || req.Bool("tls") {
		t.Errorf("host=%q port=%d tls=%v — a valid connection did not arrive",
			req.String("host"), req.Int("port"), req.Bool("tls"))
	}
}

// **What Check calls invalid is what Lookup refuses.** The rule this package
// keeps relearning: a problem reported by the page and sailed past by the run
// is worse than no problem at all, because the operator is told their file is
// broken while it quietly connects.
func TestAMistypedSetValueAlsoRefusesToResolve(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      db:
        set:
          tls: "true"
`)
	reg := tlsRegistry(t)
	if _, verr := Lookup(cfg, reg.Capabilities()[0], "staging", reg); verr == nil {
		t.Fatal("Check reports this profile and Lookup resolves it — the report is advice nobody has to take")
	}
}

// The same value under the same key, without the profile: `Check` is about
// what a connection states, so a namespace with no such key is untouched.
func TestOnlyTheDeclaredKeysAreJudged(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      db:
        set:
          host: db.internal
`)
	if problems := Check(cfg, tlsRegistry(t)); len(problems) != 0 {
		t.Fatalf("a connection stating one correct key was reported: %v", problems)
	}
}

// The type check runs before the Options check and stops there. Options
// compares the text of a value, so it has nothing to say about one that is
// not text — and reporting both would bury the complaint the operator can act
// on under a second one caused by it.
func TestATypeProblemIsNotAlsoReportedAsAnOptionsProblem(t *testing.T) {
	// A quoted value, outside the set, for the numeric input that also
	// declares one: the shape where both checks have something to say. The
	// type check wins and the Options check does not get a second bite —
	// being told a value is not one of `1|2|3` is no help when the reason it
	// will not work is that it is text.
	cfg := load(t, `
profiles:
  staging:
    plugins:
      db:
        set:
          level: "9"
`)
	problems := Check(cfg, tlsRegistry(t))
	if len(problems) != 1 {
		t.Fatalf("want one problem, got %d: %v", len(problems), problems)
	}
	if strings.Contains(problems[0].Reason, "accepts") {
		t.Errorf("reported as an Options problem rather than a type one: %+v", problems[0])
	}
}

// And a value that is text but outside the declared set is still reported as
// exactly that.
func TestTheOptionsCheckStillReportsAValueOutsideTheSet(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      db:
        set:
          mode: reckless
`)
	problems := Check(cfg, tlsRegistry(t))
	if len(problems) != 1 || !strings.Contains(problems[0].Hint, "fast") {
		t.Fatalf("the Options check stopped working: %v", problems)
	}
}

// **A `secrets:` mapping a `set:` value shadows resolves nothing.** The two
// blocks can target the same input, and Fill never fetches a reference for an
// input Bind already supplied — so an operator who moves a password out of
// `set:` and into `secrets:` and leaves the old line behind has changed
// nothing, and the plaintext one is still what authenticates.
func TestASecretsMappingASetValueShadowsIsReported(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      db:
        set:
          host: db.internal
        secrets:
          host: kv:staging-host
`)
	problems := Check(cfg, tlsRegistry(t))
	if len(problems) == 0 {
		t.Fatal("a `secrets:` line that resolves nothing was accepted")
	}
	if !strings.Contains(problems[0].Reason, "never takes effect") {
		t.Errorf("reported as something else: %+v", problems[0])
	}
	// The hint has to name the plaintext one as the line to remove. Told only
	// "remove one", an operator picks the reference — which is the wrong half.
	if !strings.Contains(problems[0].Hint, "plaintext") {
		t.Errorf("the hint does not say which half to keep: %+v", problems[0])
	}
}

// A mapping onto an input nothing states is the ordinary, working case.
func TestAMappingOntoAnInputNoValueStatesIsFine(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      db:
        set:
          port: 6432
        secrets:
          host: kv:staging-host
`)
	if problems := Check(cfg, tlsRegistry(t)); len(problems) != 0 {
		t.Fatalf("a working mapping was reported: %v", problems)
	}
}

// A connection can be wrong in more than one way at once, and each rule
// should say its piece once. Under a forward, an endpoint input stated in
// both blocks would otherwise be reported three times — twice by
// checkSecretRefs alone, which is the same complaint with two endings.
func TestOneRuleSpeaksOncePerConnection(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      db:
        kube: prod/db/svc/postgres:5432
        set:
          host: db.internal
        secrets:
          host: kv:staging-host
`)
	problems := Check(cfg, tlsRegistry(t))
	byReason := map[string]int{}
	for _, p := range problems {
		byReason[p.Reason]++
	}
	for reason, n := range byReason {
		if n > 1 {
			t.Errorf("%q reported %d times", reason, n)
		}
	}
	// The `secrets:` half gets exactly one sentence, and it is the forward
	// one: under a coordinate that is the fact that decides, and being told
	// about the `set:` line as well is being told to fix the wrong thing.
	forward, shadow := 0, 0
	for _, p := range problems {
		if strings.Contains(p.Reason, "`secrets: host`") {
			forward++
		}
		if strings.Contains(p.Reason, "never takes effect") {
			shadow++
		}
	}
	if forward != 1 || shadow != 0 {
		t.Errorf("secrets reported %d forward and %d shadow problems, want 1 and 0: %v",
			forward, shadow, problems)
	}
}
