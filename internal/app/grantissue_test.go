package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/view"
)

// runIn runs args against the real tree under the config body given, the data
// directory its own.
func runIn(t *testing.T, config string, args ...string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

const stagingOfBuiltIns = `
profiles:
  staging:
    plugins:
      kv:
        set: {path: /tmp/staging-kv}
      sys: {}
`

func heldTargets(t *testing.T) []string {
	t.Helper()
	grants, verr := core.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	var out []string
	for _, g := range grants {
		out = append(out, g.Target+"@"+g.Profile+"@"+g.Agent)
	}
	slices.Sort(out)
	return out
}

// `rta grant issue --profile` against the catalogue rta ships: an environment
// of two built-in plugins is issued as its reads, widens a class at a time,
// and goes back by the environment and the agent — through the real command
// tree, so the flags, the optional role and the options picker are the ones a
// person meets.
func TestGrantIssueReadsAnEnvironmentOfBuiltInPlugins(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	if _, err := runIn(t, stagingOfBuiltIns, "grant", "issue", "--profile", "staging", "--agent", "claude"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	got := heldTargets(t)
	for _, want := range []string{"kv.list@staging@claude", "kv.status@staging@claude", "sys.cpu@staging@claude"} {
		if !slices.Contains(got, want) {
			t.Errorf("the reads of staging lack %q: %q", want, got)
		}
	}
	for _, never := range []string{"kv.get@", "kv.set@", "kv.rename@"} {
		for _, g := range got {
			if strings.HasPrefix(g, never) {
				t.Errorf("a reads bundle issued %q", g)
			}
		}
	}

	if _, err := runIn(t, stagingOfBuiltIns, "grant", "issue", "--profile", "staging", "--agent", "claude", "--safety", "write"); err != nil {
		t.Fatalf("issue --safety write: %v", err)
	}
	if got := heldTargets(t); !slices.Contains(got, "kv.get@staging@claude") || !slices.Contains(got, "kv.set@staging@claude") {
		t.Errorf("the write class did not take in the writes: %q", got)
	}

	out, err := runIn(t, stagingOfBuiltIns, "grant", "revoke", "--profile", "staging", "--agent", "claude")
	if err != nil || !strings.Contains(out, "revoked ") {
		t.Fatalf("revoke: %q, %v", out, err)
	}
	if got := heldTargets(t); len(got) != 0 {
		t.Errorf("the bundle did not go back as one unit: %q", got)
	}
}

// A role or an environment, said as the command line's own refusals — and no
// --server, for either: the operator channel signs one grant per envelope, so
// a bundle is not carried by it, and the unknown flag is what a role has
// always been refused with.
func TestGrantIssueIsLocalAndTakesOneOfARoleAndAnEnvironment(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	for _, args := range [][]string{
		{"grant", "issue", "--profile", "staging", "--server", "lab"},
		{"grant", "issue", "dev", "--server", "lab"},
	} {
		ve := hintFor(t, args...)
		if !strings.Contains(ve.Message, "unknown flag: --server") || !strings.Contains(ve.Hint, "rta grant issue --help") {
			t.Errorf("`rta %s` was told %q / %q", strings.Join(args, " "), ve.Message, ve.Hint)
		}
	}
	for _, c := range []struct {
		args []string
		code string
	}{
		{[]string{"grant", "issue"}, "grant.issue.nothing"},
		{[]string{"grant", "issue", "dev", "--profile", "staging"}, "grant.issue.both"},
		{[]string{"grant", "issue", "--profile", "staging", "--safety", "everything"}, "core.input.option"},
	} {
		_, err := runIn(t, stagingOfBuiltIns, c.args...)
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != c.code {
			t.Errorf("`rta %s` ended in %v, want %s", strings.Join(c.args, " "), err, c.code)
		}
	}
	if got := heldTargets(t); len(got) != 0 {
		t.Errorf("a refusal left %q", got)
	}
}

// The passphrase of a guard-gated command is asked at a prompt on the CLI and
// refused as a flag on every run, so nothing that lists flags offers it: not
// --help, not completion, not the usage line of the explain card. The flag is
// still parsed, so a person who types it learns why and what to do, and the
// masked box of the form — which is what the input is declared for — is
// untouched. A passphrase the CLI does take (kv's, keys add's) is listed as
// ever.
func TestAPassphraseTheCommandLineRefusesIsNotOfferedThere(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	for _, command := range [][]string{{"grant", "issue"}, {"grant", "allow"}, {"grant", "renew"}, {"agent", "allow"}} {
		help, err := runIn(t, stagingOfBuiltIns, append(command, "--help")...)
		if err != nil || strings.Contains(help, "--passphrase") {
			t.Errorf("%v --help lists the passphrase flag (%v):\n%s", command, err, help)
		}
		offered, _ := runIn(t, stagingOfBuiltIns, append(append([]string{"__complete"}, command...), "--pass")...)
		if strings.Contains(offered, "--passphrase") {
			t.Errorf("completion of %v offers --passphrase:\n%s", command, offered)
		}
	}
	for _, command := range [][]string{{"kv", "get"}, {"keys", "add"}} {
		help, err := runIn(t, stagingOfBuiltIns, append(command, "--help")...)
		if err != nil || !strings.Contains(help, "--passphrase") {
			t.Errorf("%v --help lost a passphrase flag the command line takes (%v):\n%s", command, err, help)
		}
	}

	card, err := runIn(t, stagingOfBuiltIns, "explain", "grant.issue", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Pairs []view.Pair `json:"pairs"`
	}
	if err := json.Unmarshal([]byte(card), &env); err != nil {
		t.Fatalf("explain answered %q: %v", card, err)
	}
	for _, p := range env.Pairs {
		switch p.Key {
		case "cli":
			if strings.Contains(p.Value, "passphrase") {
				t.Errorf("the usage line offers the passphrase: %s", p.Value)
			}
		case "input:passphrase":
			if !strings.Contains(p.Value, "asked at a prompt on the CLI, which refuses it as a flag") {
				t.Errorf("the input line does not say how the passphrase arrives: %s", p.Value)
			}
		}
	}

	_, err = runIn(t, stagingOfBuiltIns, "grant", "issue", "--profile", "staging", "--agent", "claude", "--passphrase", "hunter2")
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "core.guard.passphrase.argv" {
		t.Errorf("a passphrase typed anyway = %v, want core.guard.passphrase.argv", err)
	}
	if len(heldTargets(t)) != 0 {
		t.Errorf("a refused run issued %q", heldTargets(t))
	}
}
