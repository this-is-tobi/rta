package profile

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// fenceRegistry is a plugin shaped like the ones a fence is for: a scope a
// call works in (`namespace`), the switch that reads past it, a number, a
// closed set, a list, a credential, and the address a forward fills.
func fenceRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "app", Summary: "app", Capabilities: []plugin.Capability{{
			ID: "app.status", Summary: "status", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{
				{Name: "namespace", Type: plugin.String, Config: "namespace"},
				{Name: "all-namespaces", Type: plugin.Bool, Config: "all-namespaces", Widens: "namespace"},
				{Name: "replicas", Type: plugin.Int, Config: "replicas", Min: 1, Max: 10},
				{Name: "mode", Type: plugin.String, Config: "mode", Options: []string{"fast", "safe"}},
				{Name: "names", Type: plugin.StringSlice, Config: "names"},
				{Name: "host", Type: plugin.String, Config: "host", Local: true, Endpoint: plugin.EndpointAddress},
				{Name: "file", Type: plugin.Path, Config: "file"},
				{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

const fenceHead = "profiles:\n  staging:\n    plugins:\n      app:\n"

func problemsOf(t *testing.T, body string) []Problem {
	t.Helper()
	return Check(load(t, fenceHead+body), fenceRegistry(t))
}

func reasons(problems []Problem) string {
	var out []string
	for _, p := range problems {
		out = append(out, p.Reason+" ("+p.Hint+")")
	}
	return strings.Join(out, "\n")
}

// A block the loader does not know is a value the operator believes is in
// effect, so `only:` is a key of a plugin entry and nothing reports it as one
// nobody reads.
func TestOnlyIsAKeyOfAPluginEntry(t *testing.T) {
	cfg := load(t, fenceHead+"        only:\n          namespace: [staging]\n")
	conn := cfg.Profiles["staging"].Plugins["app"]
	if got := conn.UnknownKeys(); len(got) != 0 {
		t.Fatalf("only: was reported as a key nothing reads: %v", got)
	}
	if list, _ := plugin.FenceList(conn.Only["namespace"]); len(list) != 1 || list[0] != "staging" {
		t.Errorf("the list was not kept as written: %#v", conn.Only)
	}
	if problems := Check(cfg, fenceRegistry(t)); len(problems) != 0 {
		t.Errorf("a plain fence was reported: %s", reasons(problems))
	}
}

// **A `set:` value outside its own list is a default no call could use.** The
// host would refuse it on every call, so the profile is refused at the load —
// by Lookup, which every call goes through, and reported by Check, which every
// page prints — rather than saved and refused by every call afterwards.
func TestASetValueOutsideItsOwnListIsAConfigurationError(t *testing.T) {
	body := "        set:\n          namespace: prod\n        only:\n          namespace: [staging, staging-jobs]\n"
	problems := problemsOf(t, body)
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "`set: namespace` is not one of the values `only: namespace` allows") {
		t.Fatalf("Check = %s", reasons(problems))
	}
	if problems[0].Plugin != "app" {
		t.Errorf("the problem does not say which entry: %+v", problems[0])
	}
	cfg := load(t, fenceHead+body)
	_, verr := Lookup(cfg, fenceRegistry(t).Capabilities()[0], "staging", fenceRegistry(t))
	if verr == nil || verr.Code != "core.profile.only" {
		t.Fatalf("Lookup did not refuse it: %v", verr)
	}
	if strings.Contains(verr.Error(), "prod") {
		t.Errorf("the refusal quotes the value the file states: %q", verr.Error())
	}

	// And the value inside the list is a statement like any other.
	inside := "        set:\n          namespace: staging-jobs\n        only:\n          namespace: [staging, staging-jobs]\n"
	if problems := problemsOf(t, inside); len(problems) != 0 {
		t.Errorf("a set value inside its list was refused: %s", reasons(problems))
	}
}

// Compared by the declared type, so a spelling the run reads as the same value
// is the same value here — an option in another case, a list's every element.
func TestAFenceComparesByTheInputsDeclaredType(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"an option in another case", "        set:\n          mode: SAFE\n        only:\n          mode: [safe]\n", true},
		{"a list inside its list", "        set:\n          names: [a, b]\n        only:\n          names: [a, b, c]\n", true},
		{"a list with one element outside", "        set:\n          names: [a, z]\n        only:\n          names: [a, b, c]\n", false},
		{"a number inside its list", "        set:\n          replicas: 3\n        only:\n          replicas: [2, 3]\n", true},
		{"a number outside its list", "        set:\n          replicas: 4\n        only:\n          replicas: [2, 3]\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := problemsOf(t, tc.body)
			if tc.ok && len(problems) != 0 {
				t.Errorf("refused: %s", reasons(problems))
			}
			if !tc.ok && len(problems) == 0 {
				t.Error("a set value outside the list was accepted")
			}
		})
	}
}

// The grammar of a list is the declaration's, and every way to get it wrong is
// reported in words that never quote what the file says.
func TestAnOnlyListIsHeldToTheDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"a scalar where a list is declared", "        only:\n          namespace: staging\n", "where a list of allowed values is declared"},
		{"an empty list", "        only:\n          namespace: []\n", "lists no value"},
		{"text where a number is declared", "        only:\n          replicas: [\"2\"]\n", "an entry written as text where an integer is declared"},
		{"a number where text is declared", "        only:\n          namespace: [2]\n", "where text is declared"},
		{"an entry written with nothing after it", "        only:\n          namespace: [~]\n", "an entry written as nothing"},
		{"a value the input does not accept", "        only:\n          mode: [slow]\n", "lists a value the input does not accept"},
		{"a number outside the input's range", "        only:\n          replicas: [11]\n", "outside what the input takes"},
		{"a key nothing reads", "        only:\n          namspace: [staging]\n", `nothing in app reads "namspace", named under ` + "`only:`"},
		{"a key a profile may not fill", "        only:\n          file: [/etc/passwd]\n", "a config key in app, but a profile may not fill it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := problemsOf(t, tc.body)
			if len(problems) == 0 || !strings.Contains(problems[0].Reason, tc.want) {
				t.Fatalf("Check = %s, want %q", reasons(problems), tc.want)
			}
			for _, secret := range []string{"staging", "/etc/passwd", "slow"} {
				if strings.Contains(problems[0].Reason, secret) {
					t.Errorf("the report quotes %q from the file: %q", secret, problems[0].Reason)
				}
			}
		})
	}
	// Close to a real key, it says which — the hint `set:` gives.
	problems := problemsOf(t, "        only:\n          namspace: [staging]\n")
	if len(problems) == 0 || !strings.Contains(problems[0].Hint, `"namespace"`) {
		t.Errorf("a misspelt key is not pointed at the one it is near: %s", reasons(problems))
	}
}

// A forward fills the address itself, so a fence on it fences a value no call
// resolves to — the same refusal `set:` gets beside a coordinate.
func TestAFenceOnWhatAForwardFillsIsRefused(t *testing.T) {
	body := "        kube: homelab/databases/svc/app:5432\n        only:\n          host: [db.internal]\n"
	problems := problemsOf(t, body)
	if len(problems) == 0 || !strings.Contains(problems[0].Reason, "`only: host` fences a value the forward `kube:` opens overrides") {
		t.Fatalf("Check = %s", reasons(problems))
	}
	// Direct, the same line is an ordinary fence.
	if problems := problemsOf(t, "        only:\n          host: [db.internal]\n"); len(problems) != 0 {
		t.Errorf("a fence on a host with no forward was refused: %s", reasons(problems))
	}
}

// **A credential fetched after the check is a value the check never saw.** The
// host fences before anything is fetched or opened, so a reference aimed at a
// fenced input would fill it with something the list never held.
func TestASecretMappedOntoAFencedInputIsRefused(t *testing.T) {
	body := "        only:\n          namespace: [staging]\n        secrets:\n          namespace: kv:somewhere\n"
	problems := problemsOf(t, body)
	if len(problems) == 0 || !strings.Contains(problems[0].Reason, "`secrets: namespace` fills an input `only: namespace` fences") {
		t.Fatalf("Check = %s", reasons(problems))
	}
}

// The other door: a `set:` line that turns on the switch the plugin says reads
// past the fenced input contradicts the fence as plainly as a value outside it.
func TestASetLineThatTurnsOnAWideningSwitchIsRefused(t *testing.T) {
	on := "        set:\n          all-namespaces: true\n        only:\n          namespace: [staging]\n"
	problems := problemsOf(t, on)
	if len(problems) == 0 || !strings.Contains(problems[0].Reason, "turns on a switch that widens namespace") {
		t.Fatalf("Check = %s", reasons(problems))
	}
	// Off is the same statement as not saying it, and a switch with nothing
	// fenced is an ordinary default.
	for name, body := range map[string]string{
		"switched off":      "        set:\n          all-namespaces: false\n        only:\n          namespace: [staging]\n",
		"nothing fenced":    "        set:\n          all-namespaces: true\n",
		"another is fenced": "        set:\n          all-namespaces: true\n        only:\n          mode: [safe]\n",
	} {
		if problems := problemsOf(t, body); len(problems) != 0 {
			t.Errorf("%s: refused: %s", name, reasons(problems))
		}
	}
}

// What a grant is bound to has to notice a fence being added, loosened or
// removed, and a key moved between the two blocks that read it is a different
// statement — a value in `set:` is a default and the same one in `only:` is a
// wall.
func TestEditingAFenceMovesTheConnectionStamp(t *testing.T) {
	base := config.Connection{
		Set:  map[string]any{"namespace": "staging"},
		Only: map[string]any{"namespace": []any{"staging"}},
	}
	origin := ConnStamp("app", base)
	for name, c := range map[string]config.Connection{
		"a value added to the list": {
			Set:  map[string]any{"namespace": "staging"},
			Only: map[string]any{"namespace": []any{"staging", "staging-jobs"}},
		},
		"the list removed": {Set: map[string]any{"namespace": "staging"}},
		"another key fenced": {
			Set: map[string]any{"namespace": "staging"},
			Only: map[string]any{"namespace": []any{"staging"},
				"mode": []any{"safe"}},
		},
		"the list written as another type": {
			Set:  map[string]any{"namespace": "staging"},
			Only: map[string]any{"namespace": "staging"},
		},
		"the fence moved to set": {
			Set: map[string]any{"namespace": "staging", "only": []any{"staging"}},
		},
	} {
		if ConnStamp("app", c) == origin {
			t.Errorf("%s did not change the stamp, so a standing grant would keep covering it", name)
		}
	}
	if ConnStamp("app", base) != origin {
		t.Error("the same fence stamped differently twice")
	}
}

func appCapability(t *testing.T) plugin.Capability {
	t.Helper()
	c, ok := fenceRegistry(t).Capability("app.status")
	if !ok {
		t.Fatal("the fixture registers no app.status")
	}
	return c
}

// The refusal a person gets names the input as they would type it, what the
// connection reaches, and the ways across.
func TestFencedWordsTheRefusalForWhoeverCanActOnIt(t *testing.T) {
	c := appCapability(t)
	conn := config.Connection{Only: map[string]any{"namespace": []any{"staging", "staging-jobs"}}}
	in := plugin.Inputs{Caller: map[string]any{"namespace": "prod"}}

	for _, tc := range []struct {
		surface plugin.Surface
		input   string
	}{
		{plugin.SurfaceCLI, "--namespace"},
		{plugin.SurfaceTUI, "the namespace box"},
		{plugin.SurfaceMCP, `the "namespace" argument`},
	} {
		verr := Fenced("staging", conn, c, in, noEnv, tc.surface)
		if verr == nil {
			t.Fatalf("%v: a value outside the fence was let through", tc.surface)
		}
		if verr.Code != "core.profile.fenced" || verr.Input != "namespace" {
			t.Errorf("%v: code = %q, naming the input %q", tc.surface, verr.Code, verr.Input)
		}
		if !strings.Contains(verr.Message, tc.input+" is outside what profile \"staging\" reaches: staging, staging-jobs") {
			t.Errorf("%v: message = %q", tc.surface, verr.Message)
		}
		if !strings.Contains(verr.Hint, "rta profile set staging --plugin app --only namespace=") ||
			!strings.Contains(verr.Hint, "another profile") {
			t.Errorf("%v: hint = %q, want the ways across", tc.surface, verr.Hint)
		}
	}

	// A blank is told apart from a wrong value, because the fix differs.
	blank := Fenced("staging", conn, c, plugin.Inputs{Caller: map[string]any{"namespace": ""}}, noEnv, plugin.SurfaceCLI)
	if blank == nil || blank.Input != "namespace" ||
		!strings.Contains(blank.Message, "--namespace is blank, and profile \"staging\" reaches only: staging, staging-jobs") {
		t.Errorf("a blank = %v", blank)
	}

	// A labeled instance is written under its own entry.
	instance := Fenced("staging/analytics", conn, c, in, noEnv, plugin.SurfaceCLI)
	if instance == nil || !strings.Contains(instance.Hint, "--plugin app/analytics --only namespace=") {
		t.Errorf("an instance's hint = %v", instance)
	}

	// And the switch.
	widened := Fenced("staging", conn, c, plugin.Inputs{Caller: map[string]any{"namespace": "staging", "all-namespaces": true}},
		noEnv, plugin.SurfaceCLI)
	// The switch is what to change, so it is the input the refusal names.
	if widened == nil || widened.Code != "core.profile.fenced.switch" || widened.Input != "all-namespaces" ||
		!strings.Contains(widened.Message, "--all-namespaces reads past --namespace, which profile \"staging\" fences to: staging, staging-jobs") {
		t.Errorf("a widening switch = %v", widened)
	}

	// Inside the fence, and a connection with none, say nothing.
	if verr := Fenced("staging", conn, c, plugin.Inputs{Caller: map[string]any{"namespace": "staging"}}, noEnv, plugin.SurfaceCLI); verr != nil {
		t.Errorf("a call inside the fence was refused: %v", verr)
	}
	if verr := Fenced("staging", config.Connection{}, c, in, noEnv, plugin.SurfaceCLI); verr != nil {
		t.Errorf("a connection with no fence refused a call: %v", verr)
	}
}

// The form completing a box asks the same question with that box left out: its
// text is the start of a value, and what the connection and the environment say of
// the other inputs, and the switch that reads past the box, are held as a run holds
// them.
func TestFenceBreachedLeavesOutTheValueOfTheUnfinishedBox(t *testing.T) {
	c := appCapability(t)
	conn := config.Connection{Only: map[string]any{
		"namespace": []any{"staging"}, "mode": []any{"safe"},
	}}
	ask := func(caller map[string]any, look func(string) (string, bool)) *plugin.FenceBreach {
		return FenceBreached("staging", conn, c, plugin.Inputs{Caller: caller, Unfinished: "namespace"}, look)
	}

	if breach := FenceBreached("staging", conn, c, plugin.Inputs{Caller: map[string]any{"namespace": "pr", "mode": "safe"}}, noEnv); breach == nil {
		t.Fatal("the whole check let the value through, so this proves nothing")
	}
	if breach := ask(map[string]any{"namespace": "pr", "mode": "safe"}, noEnv); breach != nil {
		t.Errorf("the box being typed was tested: %+v", breach)
	}
	if breach := ask(map[string]any{"namespace": "pr", "mode": "safe", "all-namespaces": true}, noEnv); breach == nil || !breach.Widened() {
		t.Errorf("the switch that reads past the box was let through: %+v", breach)
	}
	if breach := ask(map[string]any{"namespace": "pr", "mode": "fast"}, noEnv); breach == nil || breach.Input.Name != "mode" {
		t.Errorf("another input outside the fence was let through: %+v", breach)
	}
	env := func(name string) (string, bool) {
		if name == "RTA_PROFILE_STAGING_MODE" {
			return "fast", true
		}
		return "", false
	}
	if breach := ask(map[string]any{"namespace": "pr"}, env); breach == nil || breach.Input.Name != "mode" {
		t.Errorf("what the environment fills in was not seen: %+v", breach)
	}
	if breach := FenceBreached("staging", config.Connection{}, c, plugin.Inputs{Caller: map[string]any{"all-namespaces": true}, Unfinished: "namespace"}, noEnv); breach != nil {
		t.Errorf("a connection with no fence held a call: %+v", breach)
	}
}

// What a profile reads from the environment for an input is one of the values
// the check sees: a fence that held `set:` and not `RTA_PROFILE_<P>_<INPUT>`
// would be one the next export walks through.
func TestTheFenceSeesWhatTheEnvironmentFillsIn(t *testing.T) {
	c := appCapability(t)
	conn := config.Connection{Only: map[string]any{"namespace": []any{"staging"}}}
	env := func(name string) (string, bool) {
		if name == "RTA_PROFILE_STAGING_NAMESPACE" {
			return "prod", true
		}
		return "", false
	}
	if verr := Fenced("staging", conn, c, plugin.Inputs{}, env, plugin.SurfaceCLI); verr == nil {
		t.Error("an environment variable carried a value past the fence")
	}
	if verr := Fenced("staging", conn, c, plugin.Inputs{}, noEnv, plugin.SurfaceCLI); verr != nil {
		t.Errorf("the one value the fence allows was refused when nothing said otherwise: %v", verr)
	}
}

// A list the loader would refuse, if it ever got this far, is a fence that
// allows nothing — not silence about the connection.
func TestAnAllowlistThatIsNotAListFailsClosed(t *testing.T) {
	c := appCapability(t)
	for name, written := range map[string]any{
		"a scalar":      "staging",
		"nothing":       nil,
		"a block":       map[string]any{"a": "b"},
		"an empty list": []any{},
	} {
		conn := config.Connection{Only: map[string]any{"namespace": written}}
		if fence := FenceOf(conn); fence == nil || len(fence["namespace"]) != 0 {
			t.Errorf("%s: FenceOf = %#v, want a fence that allows nothing", name, fence)
		}
		if verr := Fenced("staging", conn, c, plugin.Inputs{Caller: map[string]any{"namespace": "staging"}}, noEnv, plugin.SurfaceCLI); verr == nil {
			t.Errorf("%s: a call was let through a fence that could not be read", name)
		}
	}
	if FenceOf(config.Connection{}) != nil {
		t.Error("a connection that fences nothing has a fence")
	}
}

// A list of two hundred namespaces is not two hundred words on a terminal, and
// a terminal acts on an escape in a file whoever wrote it.
func TestARefusalCountsWhatItDoesNotList(t *testing.T) {
	c := appCapability(t)
	var many []any
	for i := range 13 {
		many = append(many, "ns-"+string(rune('a'+i)))
	}
	many[0] = "ns-\x1b]0;x\x07a"
	verr := Fenced("staging", config.Connection{Only: map[string]any{"namespace": many}}, c,
		plugin.Inputs{Caller: map[string]any{"namespace": "other"}}, noEnv, plugin.SurfaceCLI)
	if verr == nil || !strings.Contains(verr.Message, "and 3 more") {
		t.Fatalf("the refusal does not count the rest: %v", verr)
	}
	if strings.ContainsRune(verr.Message, '\x1b') {
		t.Errorf("the refusal carries an escape from the file: %q", verr.Message)
	}
}
