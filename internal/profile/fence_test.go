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
