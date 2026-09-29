package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// **A secret typed under `set:` must not be printed back.**
//
// `secrets:` holds a reference and `set:` holds a value, so a password in the
// wrong block is inert — nothing reads it, and `profile show` already says so
// in its problems row. What it is not is harmless. The config file is written
// 0644 because it is documented to hold no secrets, and this command exists to
// be read and pasted; printing the literal turns one wrong line in a file into
// a credential on a screen and in a ticket.
//
// It is a mistake worth expecting rather than an exotic one. There is no
// command that writes a profile, so the block is hand-written from the
// documentation, and `set:` is the obvious-looking place to put a value.
func TestALiteralSecretUnderSetIsNotPrintedBack(t *testing.T) {
	const cfg = `profiles:
  staging:
    plugins:
      db:
        set:
          host: db.internal
          password: hunter2-in-the-wrong-block
`
	out, _, err := runWith(t, connRegistry(t), cfg, "profile", "show", "staging")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, "hunter2-in-the-wrong-block") {
		t.Errorf("the literal secret was printed back:\n%s", out)
	}
	if !strings.Contains(out, "redacted") {
		t.Errorf("the value was dropped rather than marked — somebody has to know it is there:\n%s", out)
	}
	// Redaction must be by the plugin's own declaration, not by blanking the
	// whole block: a host is not a secret and hiding it would make this command
	// useless for the thing it is actually for.
	if !strings.Contains(out, "db.internal") {
		t.Errorf("a non-secret was redacted too:\n%s", out)
	}
}

// The same, through --output json. That is the form that gets pasted, and a
// redaction applied only to the pretty renderer would miss the path where it
// matters most.
func TestTheRedactionSurvivesEveryOutputFormat(t *testing.T) {
	const cfg = `profiles:
  staging:
    plugins:
      db:
        set:
          password: hunter2-in-the-wrong-block
`
	// csv among them: it writes a key/value view as its pairs, where it used to
	// refuse anything but a table.
	for _, format := range []string{"json", "yaml", "csv", "md"} {
		out, _, err := runWith(t, connRegistry(t), cfg, "profile", "show", "staging", "-o", format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if strings.Contains(out, "hunter2-in-the-wrong-block") {
			t.Errorf("--output %s printed the literal secret:\n%s", format, out)
		}
	}
}

// A plugin nobody registered is not a plugin that says no.
//
// `rta profile show` builds most of its rows by asking the registry what the
// namespace declares, and an empty answer has two causes arguing for opposite
// conclusions: the plugin offers no such input, or nothing under that
// namespace registered at all. The second is the ordinary state after any
// rebuild — trust is keyed on the digest, so new bytes register nothing until
// somebody approves them — and reading it as the first told an operator their
// `kube:` coordinate was unusable and their `secrets:` mapping named an input
// that does not exist, with a hint offering to delete each. Both lines were
// right; the approval was missing.
func TestTheCardDoesNotJudgeAPluginNobodyRegistered(t *testing.T) {
	const cfg = `profiles:
  staging:
    plugins:
      absent@0123456789ab:
        kube: ctx/ns/svc/thing:5432
        secrets:
          password: kv:some-entry
`
	out, _, err := runWith(t, connRegistry(t), cfg, "profile", "show", "staging")
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range []string{
		"declares no input a tunnel can fill",
		"names an input absent does not offer",
	} {
		if strings.Contains(out, claim) {
			t.Errorf("the card asserted %q about a declaration it never read:\n%s", claim, out)
		}
	}
	// It still says something, and what it says is the actionable thing.
	if !strings.Contains(out, "problem") {
		t.Errorf("an unregistered plugin was reported as no problem at all:\n%s", out)
	}
	// And the configured values still print, so the page remains a record of
	// what the operator wrote.
	if !strings.Contains(out, "kv:some-entry") {
		t.Errorf("the secrets mapping vanished from the page:\n%s", out)
	}
}

// A problem is said once however many entries it is about, naming them: an
// unregistered plugin under two instance labels was two identical rows, and
// neither said which entry of the page above it was meant.
func TestProfileShowSaysAProblemOnceNamingItsEntries(t *testing.T) {
	const cfg = `profiles:
  staging:
    plugins:
      ghost/a@0123456789ab:
        set:
          host: a.internal
      ghost/b@0123456789ab:
        set:
          host: b.internal
      phantom@0123456789ab: {}
`
	out, _, err := runWith(t, connRegistry(t), cfg, "profile", "show", "staging", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Pairs []struct{ Key, Value string } `json:"pairs"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	var problems []string
	for _, p := range page.Pairs {
		if p.Key == "problem" {
			problems = append(problems, p.Value)
		}
	}
	want := []string{
		`under ghost/a@0123456789ab, ghost/b@0123456789ab: profile names "ghost", which is not a registered plugin`,
		`under phantom@0123456789ab: profile names "phantom", which is not a registered plugin`,
	}
	if len(problems) != len(want) {
		t.Fatalf("problems = %q, want one for each of %q", problems, want)
	}
	for i, w := range want {
		if !strings.HasPrefix(problems[i], w+" — ") {
			t.Errorf("problem %d = %q, want it to open %q and go on to its hint", i, problems[i], w)
		}
	}
}

// An empty profile list is still a table to anything that parses it. The
// sentence saying what would fill it is for a screen; in -o json it was a
// text view, and the CLI page's own `jq '.rows[] | ...'` example failed with
// "Cannot iterate over null" on a machine with no profiles, while -o csv
// refused a text view and exited 2 — the code for something unexpected.
//
// It said so by swapping in a text view for pretty output, which put the
// sentence into a pipe as well and left -o md a heading row over nothing; the
// table carries it now, drawn where every other listing's is.
func TestAnEmptyProfileListIsATableToAParser(t *testing.T) {
	const empty = "profiles: {}\n"
	saved := isTTY
	t.Cleanup(func() { isTTY = saved })
	isTTY = func() bool { return true }
	out, _, err := runWith(t, connRegistry(t), empty, "profile", "list", "-o", "pretty")
	if err != nil || !strings.Contains(out, "No profile is configured yet") {
		t.Errorf("pretty on a terminal = %q, %v; want the sentence saying what would fill it", out, err)
	}
	isTTY = func() bool { return false }
	out, _, err = runWith(t, connRegistry(t), empty, "profile", "list", "-o", "pretty")
	if err != nil || strings.Contains(out, "No profile is configured yet") || !strings.Contains(out, "PROFILE") {
		t.Errorf("pretty into a pipe = %q, %v; want the headings and no sentence", out, err)
	}
	out, _, err = runWith(t, connRegistry(t), empty, "profile", "list", "-o", "md")
	if err != nil || !strings.Contains(out, "No profile is configured yet") || strings.Contains(out, "| Profile") {
		t.Errorf("md = %q, %v; want the sentence in place of the grid", out, err)
	}

	out, _, err = runWith(t, connRegistry(t), empty, "profile", "list", "-o", "json")
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	var env struct {
		Type string     `json:"type"`
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.Type != "table" || env.Rows == nil || len(env.Rows) != 0 {
		t.Errorf("json = %s (%v); want a table whose rows are an empty array", out, err)
	}

	out, _, err = runWith(t, connRegistry(t), empty, "profile", "list", "-o", "csv")
	if err != nil || strings.TrimSpace(out) != "Profile,Plugins,Status,Note" {
		t.Errorf("csv = %q, %v; want the header row alone", out, err)
	}
}

const instancesConfig = `profiles:
  staging:
    note: the staging environment
    plugins:
      db:
        set:
          host: db.internal
      db/analytics:
        set:
          host: analytics.internal
      db/logs:
        set:
          host: logs.internal
`

// `rta profile show staging/analytics` shows the one connection the
// reference names, inside the environment it belongs to: the hint for a
// value an instance sets names it, and the page it named refused the
// reference as a profile nobody configured.
func TestProfileShowTakesAnInstanceReference(t *testing.T) {
	out, stderr, err := runWith(t, connRegistry(t), instancesConfig, "profile", "show", "staging/analytics")
	if err != nil {
		t.Fatalf("show staging/analytics: %v\n%s", err, stderr)
	}
	for _, want := range []string{"staging", "analytics", "db/analytics", "analytics.internal", "the staging environment"} {
		if !strings.Contains(out, want) {
			t.Errorf("the instance's page lacks %q:\n%s", want, out)
		}
	}
	for _, other := range []string{"db.internal", "logs.internal", "db/logs"} {
		if strings.Contains(out, other) {
			t.Errorf("the instance's page shows %q, another connection's:\n%s", other, out)
		}
	}

	// The whole environment is still the bare name's page.
	out, _, err = runWith(t, connRegistry(t), instancesConfig, "profile", "show", "staging")
	if err != nil || !strings.Contains(out, "db.internal") || !strings.Contains(out, "logs.internal") {
		t.Errorf("show staging = %v:\n%s", err, out)
	}

	// A label the environment does not hold is named as the label, with the
	// ones it does; a profile nobody configured is named by its name.
	_, stderr, err = runWith(t, connRegistry(t), instancesConfig, "profile", "show", "staging/analytcs")
	if err == nil || !strings.Contains(stderr, "core.profile.instance") ||
		!strings.Contains(stderr, "staging/analytics") || !strings.Contains(stderr, "staging/logs") {
		t.Errorf("show staging/analytcs = %v:\n%s", err, stderr)
	}
	_, stderr, err = runWith(t, connRegistry(t), instancesConfig, "profile", "show", "prod/analytics")
	if err == nil || !strings.Contains(stderr, "core.profile.unknown") || !strings.Contains(stderr, `"prod"`) {
		t.Errorf("show prod/analytics = %v:\n%s", err, stderr)
	}
}

// `rta profile show` completes an environment's instances beside it, each
// described by where it points; `rta use`, which refuses one, does not.
func TestProfileShowCompletesInstanceReferences(t *testing.T) {
	out, _, err := runWith(t, connRegistry(t), instancesConfig, "__complete", "profile", "show", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"staging\tthe staging environment", "staging/analytics\t", "staging/logs\t"} {
		if !strings.Contains(out, want) {
			t.Errorf("show completes without %q:\n%s", want, out)
		}
	}
	out, _, err = runWith(t, connRegistry(t), instancesConfig, "__complete", "use", "")
	if err != nil || !strings.Contains(out, "staging\t") || strings.Contains(out, "staging/") {
		t.Errorf("use completes %v:\n%s", err, out)
	}
}

// The command a refusal of an instance's value names runs, and shows the
// block the value is set in. The value is one a profile may hold — another
// capability reading the key takes it — and the one called refuses it.
func TestTheBlockARefusedInstanceValueNamesIsOneProfileShowShows(t *testing.T) {
	sslmode := func(options ...string) plugin.Field {
		return plugin.Field{Name: "sslmode", Type: plugin.String, Default: "disable", Config: "sslmode",
			Local: true, Options: options, Help: "TLS"}
	}
	ok := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "db", Summary: "db plugin",
		Capabilities: []plugin.Capability{
			{ID: "db.status", Summary: "status", Safety: plugin.Read, Run: ok,
				Inputs: []plugin.Field{sslmode("disable", "require")}},
			{ID: "db.check", Summary: "check", Safety: plugin.Read, Run: ok,
				Inputs: []plugin.Field{sslmode("disable", "require", "verify-full")}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	const cfg = `profiles:
  staging:
    plugins:
      db:
        set:
          sslmode: require
      db/analytics:
        set:
          sslmode: verify-full
`
	_, stderr, err := runWith(t, reg, cfg, "db", "status", "--profile", "staging/analytics")
	if err == nil || !strings.Contains(stderr, "`rta profile show staging/analytics`") {
		t.Fatalf("the refusal = %v, and does not name the page:\n%s", err, stderr)
	}
	out, stderr, err := runWith(t, reg, cfg, "profile", "show", "staging/analytics")
	if err != nil || !strings.Contains(out, "verify-full") || strings.Contains(out, "require") {
		t.Errorf("the page it names = %v, and does not show the block alone:\n%s%s", err, out, stderr)
	}
}
