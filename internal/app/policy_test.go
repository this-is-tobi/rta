package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/policy"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The grants a ceiling holds back are counted as a sentence counts them: it
// read "1 stored grant(s) would stand", the one line of policy show not
// worded for the number it gave.
func TestPolicyShowCountsTheGrantsItSuppresses(t *testing.T) {
	t.Setenv("RTA_POLICY", "")
	dir := t.TempDir()
	t.Chdir(dir)
	run := session(t, testRegistry(t))
	if err := os.WriteFile(filepath.Join(dir, policy.RepoFile), []byte("never: [kv.get, kv.env]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, c := range []struct {
		targets []string
		want    string
	}{
		{[]string{"kv.get"}, "1 stored grant would stand if this ceiling did not forbid it"},
		{[]string{"kv.get", "kv.env"}, "2 stored grants would stand if this ceiling did not forbid them"},
	} {
		var stored []grant.Grant
		for _, target := range c.targets {
			stored = append(stored, grant.Grant{Target: target, Agent: "claude", Issued: now, Expires: now.Add(time.Hour)})
		}
		if verr := grant.Save(stored); verr != nil {
			t.Fatal(verr)
		}
		out, errOut, err := run("policy", "show", "-o", "json")
		if err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
		if got := answerPairs(t, out)["grants suppressed"]; got != c.want {
			t.Errorf("grants suppressed = %q, want %q", got, c.want)
		}
	}
}

// A policy file already there is refused, coded and in the format asked for:
// it was the plain error fang styled as a box under `-o json`, which is where
// a script that sets a repository up would read it. The way out it offers is
// the file, not --force: it used to teach the flag, and the flag replaced a
// team's ceiling with the starter and asked nothing.
func TestPolicyInitOverAnExistingFileIsACodedRefusal(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, errOut, err := run(t, testRegistry(t), "policy", "init"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	_, _, err := run(t, testRegistry(t), "policy", "init", "-o", "json")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.policy.exists" {
		t.Fatalf("err = %#v, want core.policy.exists", err)
	}
	if strings.Contains(ve.Hint, "--force") || !strings.Contains(ve.Hint, "edit") {
		t.Errorf("hint = %q, want the file to edit and not --force", ve.Hint)
	}
	var buf bytes.Buffer
	if !RenderTopLevelError(&buf, NewRoot(testRegistry(t), "test"), err) {
		t.Fatal("the refusal was left for fang")
	}
}

// The ceiling a team wrote, which a starter written over it would loosen on
// every axis: a five-minute cap, three targets nobody may be granted, a
// connection nobody may name and a target that may only be granted for one
// record.
const teamCeiling = "maxTTL: 5m\nnever: [pg.dump, vault.snapshot, kv.get]\n" +
	"neverProfile: [production]\nrequireScope: [kv.get]\n"

// writePolicyHere puts body where `rta policy init` looks, in the directory
// the test is in.
func writePolicyHere(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(policy.RepoFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func policyHere(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(policy.RepoFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// `policy init --force` used to replace a team's ceiling with the starter and
// ask nothing, though the command says "never silently" two lines above the
// write. A ceiling is replaced by one no looser than it or not at all, and
// that holds whoever is asking: a person at a terminal who answers yes, and a
// script that passes --yes, are refused alike, with the axes that would have
// loosened named and the file left as it was.
func TestPolicyInitForceNeverWritesAStarterLooserThanTheCeilingThatStands(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T) *[]string{
		"without a terminal": func(t *testing.T) *[]string { return new([]string) },
		"at a terminal":      func(t *testing.T) *[]string { return askedOf(t, "y", nil) },
	} {
		for _, args := range [][]string{{"policy", "init", "--force"}, {"policy", "init", "--force", "--yes"}} {
			t.Run(name+" "+strings.Join(args[2:], " "), func(t *testing.T) {
				t.Chdir(t.TempDir())
				writePolicyHere(t, teamCeiling)
				asked := setup(t)
				_, _, err := run(t, testRegistry(t), args...)
				var ve *view.Error
				if !errors.As(err, &ve) || ve.Code != "core.policy.loosens" {
					t.Fatalf("err = %#v, want core.policy.loosens", err)
				}
				for _, axis := range []string{"maxTTL 5m would become 1h", "never: pg.dump, vault.snapshot, kv.get",
					"neverProfile: production", "requireScope: kv.get"} {
					if !strings.Contains(ve.Message, axis) {
						t.Errorf("the refusal does not name %q: %s", axis, ve.Message)
					}
				}
				if !strings.Contains(ve.Hint, "edit") || strings.Contains(ve.Hint, "--force") {
					t.Errorf("hint = %q, want editing the file on purpose as the way and not --force", ve.Hint)
				}
				if got := policyHere(t); got != teamCeiling {
					t.Errorf("the ceiling was rewritten:\n%s", got)
				}
				if len(*asked) != 0 {
					t.Errorf("asked %q about a replacement that could not happen", *asked)
				}
			})
		}
	}
}

// A replacement that loosens nothing is still somebody's file, and what goes
// with it (their comments, their roles, a cap longer than the starter's) is
// theirs to give up. With a person at a terminal that is a question showing
// what stands; with nobody there it is a refusal, and only --yes answers it
// for a script.
func TestPolicyInitForceAsksBeforeReplacingAFileThatIsNotTheStarter(t *testing.T) {
	const looser = "# our cap\nmaxTTL: 2h\n"

	t.Run("without a terminal --force alone refuses", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writePolicyHere(t, looser)
		_, _, err := run(t, testRegistry(t), "policy", "init", "--force")
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != CodeConfirmRequired {
			t.Fatalf("err = %#v, want %s", err, CodeConfirmRequired)
		}
		if !strings.Contains(ve.Hint, "--yes") {
			t.Errorf("hint = %q, want the one flag that answers for a script", ve.Hint)
		}
		if got := policyHere(t); got != looser {
			t.Errorf("the file was rewritten:\n%s", got)
		}
	})

	t.Run("without a terminal --yes answers for a script", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writePolicyHere(t, looser)
		if _, errOut, err := run(t, testRegistry(t), "policy", "init", "--force", "--yes"); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
		if got := policyHere(t); got != starterPolicy {
			t.Errorf("the file was not replaced:\n%s", got)
		}
	})

	t.Run("a dry run refuses as the real one would", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writePolicyHere(t, looser)
		_, _, err := run(t, testRegistry(t), "policy", "init", "--force", "--dry-run")
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != CodeConfirmRequired {
			t.Fatalf("err = %#v, want %s", err, CodeConfirmRequired)
		}
	})

	t.Run("at a terminal it shows what stands and asks", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writePolicyHere(t, looser)
		asked := askedOf(t, "n", nil)
		_, errOut, err := run(t, testRegistry(t), "policy", "init", "--force")
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != CodeConfirmRequired {
			t.Fatalf("a no was answered with %#v, want the declined confirmation", err)
		}
		if len(*asked) != 1 || !strings.Contains((*asked)[0], "[y/N]") {
			t.Errorf("asked %q, want one y/N question", *asked)
		}
		if !strings.Contains(errOut, "maxTTL") || !strings.Contains(errOut, "2h") || !strings.Contains(errOut, policy.RepoFile) {
			t.Errorf("the ceiling that stands was not shown before the question:\n%s", errOut)
		}
		if got := policyHere(t); got != looser {
			t.Errorf("a no rewrote the file:\n%s", got)
		}
	})

	t.Run("at a terminal a yes replaces it", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writePolicyHere(t, looser)
		asked := askedOf(t, "y", nil)
		out, errOut, err := run(t, testRegistry(t), "policy", "init", "--force", "-o", "json")
		if err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
		if len(*asked) != 1 || answerPairs(t, out)["replaced"] == "" {
			t.Errorf("asked %q, answered %q", *asked, out)
		}
		if got := policyHere(t); got != starterPolicy {
			t.Errorf("the file was not replaced:\n%s", got)
		}
	})

	t.Run("at a terminal --yes does not ask", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writePolicyHere(t, looser)
		asked := askedOf(t, "n", nil)
		if _, errOut, err := run(t, testRegistry(t), "policy", "init", "--force", "--yes"); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
		if len(*asked) != 0 || policyHere(t) != starterPolicy {
			t.Errorf("asked %q, and the file holds:\n%s", *asked, policyHere(t))
		}
	})

	t.Run("a dry run at a terminal asks nothing and writes nothing", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writePolicyHere(t, looser)
		asked := askedOf(t, "n", nil)
		out, errOut, err := run(t, testRegistry(t), "policy", "init", "--force", "--dry-run", "-o", "json")
		if err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
		if _, ok := answerPairs(t, out)["would replace"]; !ok || len(*asked) != 0 || policyHere(t) != looser {
			t.Errorf("asked %q, answered %q, and the file holds:\n%s", *asked, out, policyHere(t))
		}
	})

	t.Run("the starter itself is replaced without a word", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writePolicyHere(t, starterPolicy)
		if _, errOut, err := run(t, testRegistry(t), "policy", "init", "--force"); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
	})
}

// A file that cannot be read says nothing about what it holds, and `nevr:`
// for `never:` is exactly how a bound goes missing while the file still looks
// like one. So it is not judged by a flag that answers for a script: a person
// is shown why it does not read and asked.
func TestPolicyInitForceAsksAboutAFileThatDoesNotRead(t *testing.T) {
	const broken = "nevr: [pg.dump]\n"

	t.Chdir(t.TempDir())
	writePolicyHere(t, broken)
	_, _, err := run(t, testRegistry(t), "policy", "init", "--force", "--yes")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeConfirmRequired {
		t.Fatalf("--yes alone replaced a file nobody could read: %#v", err)
	}
	if policyHere(t) != broken {
		t.Fatal("the file was rewritten")
	}

	asked := askedOf(t, "y", nil)
	_, errOut, err := run(t, testRegistry(t), "policy", "init", "--force", "--yes")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if len(*asked) != 1 || !strings.Contains(errOut, "nevr") || policyHere(t) != starterPolicy {
		t.Errorf("asked %q, showed %q, and the file holds:\n%s", *asked, errOut, policyHere(t))
	}
}

// policy init answers with pairs, in the format asked for: it printed prose
// on stdout whatever -o said, so a script setting a repository up parsed a
// check mark. The file is named by its full path, and one --force replaced
// says so.
func TestPolicyInitAnswersWithAViewInTheFormatAskedFor(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	run := session(t, testRegistry(t))

	out, errOut, err := run("policy", "init", "--dry-run", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs := answerPairs(t, out)
	if got := pairs["would write"]; !filepath.IsAbs(got) || filepath.Base(got) != policy.RepoFile {
		t.Errorf("would write = %q, want the full path of %s", got, policy.RepoFile)
	}
	if _, err := os.Stat(policy.RepoFile); !os.IsNotExist(err) {
		t.Fatalf("--dry-run wrote the file: %v", err)
	}

	out, errOut, err = run("policy", "init", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs = answerPairs(t, out)
	written, err := os.ReadFile(pairs["wrote"])
	if err != nil || string(written) != starterPolicy {
		t.Fatalf("wrote = %q, which does not hold the starter policy: %v", pairs["wrote"], err)
	}
	if !strings.Contains(pairs["ceiling"], "maxTTL "+starterTTL) || !strings.Contains(string(written), "\nmaxTTL: "+starterTTL+"\n") {
		t.Errorf("ceiling = %q, want the maxTTL the file sets", pairs["ceiling"])
	}
	if !strings.Contains(pairs["next"], "rta policy require") {
		t.Errorf("next = %q, want the command that makes the file required", pairs["next"])
	}

	onATerminal(t)
	out, errOut, err = run("policy", "init", "--force", "--no-color")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	readsOnATerminal(t, out, "replaced", "ceiling", "next")
}

// policy require answers with pairs too, and whether the directory it ran in
// meets the requirement is one of them: that half of its prose went to
// stderr, so a script turning the requirement on never heard that the
// directory it stood in would now be refused.
func TestPolicyRequireAnswersWithAViewNamingWhetherThisDirectoryMeetsIt(t *testing.T) {
	t.Setenv("RTA_POLICY", "")
	dir := t.TempDir()
	t.Chdir(dir)
	run := session(t, testRegistry(t))
	own := policy.OperatorPath()

	out, errOut, err := run("policy", "require", "--dry-run", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs := answerPairs(t, out)
	if pairs["would write"] != own || pairs["requireRepoPolicy"] != "yes" || !strings.Contains(pairs["next"], "--dry-run") {
		t.Errorf("a dry run answered %v", pairs)
	}
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Fatalf("--dry-run wrote %s: %v", own, err)
	}

	out, errOut, err = run("policy", "require", "-o", "json")
	if err != nil || errOut != "" {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs = answerPairs(t, out)
	if pairs["wrote"] != own || pairs["requireRepoPolicy"] != "yes" {
		t.Errorf("answered %v, want the file written and the setting it holds", pairs)
	}
	if here := pairs["this directory"]; !strings.Contains(here, "no "+policy.RepoFile+" found") {
		t.Errorf("this directory = %q, want it to say the directory has no policy", here)
	}
	if !strings.Contains(pairs["next"], "rta policy init") {
		t.Errorf("next = %q, want the command that writes one", pairs["next"])
	}

	if err := os.WriteFile(filepath.Join(dir, policy.RepoFile), []byte("maxTTL: 1h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err = run("policy", "require", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs = answerPairs(t, out)
	if !strings.HasSuffix(pairs["unchanged"], "nothing written to "+own) || pairs["wrote"] != "" {
		t.Errorf("answered %v, want it to say nothing was written", pairs)
	}
	if here := pairs["this directory"]; !strings.HasPrefix(here, "has one: ") || strings.Contains(here, own) {
		t.Errorf("this directory = %q, want the repository's policy alone", here)
	}

	onATerminal(t)
	out, errOut, err = run("policy", "require", "--off", "--no-color")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	readsOnATerminal(t, out, "wrote", "requireRepoPolicy", "next")
	if strings.Contains(out, "this directory") {
		t.Errorf("turning the requirement off reported on this directory:\n%s", out)
	}
}

// The repository policy row names the files the walk up found, and those
// alone. It printed every file the ceiling was assembled from, so with a
// policy of the operator's own the row named that file as the repository's —
// and again on the row below it, which is the one about that file.
func TestPolicyShowNamesOnlyTheRepositorysFilesAsTheRepositoryPolicy(t *testing.T) {
	t.Setenv("RTA_POLICY", "")
	dir := t.TempDir()
	t.Chdir(dir)
	run := session(t, testRegistry(t))
	own := policy.OperatorPath()
	if err := os.MkdirAll(filepath.Dir(own), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("maxTTL: 2h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, policy.RepoFile), []byte("maxTTL: 1h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run("policy", "show", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs := answerPairs(t, out)
	if got := pairs["repository policy"]; filepath.Base(got) != policy.RepoFile || strings.Contains(got, own) {
		t.Errorf("repository policy = %q, want the one %s in %s", got, policy.RepoFile, dir)
	}
	if got := pairs["your own policy"]; got != own {
		t.Errorf("your own policy = %q, want %s", got, own)
	}
}

// With the config in the working-directory fallback there is no directory of
// the operator's own to put the demand in, and that is said as a coded
// refusal naming the fix rather than a plain "cannot locate".
func TestPolicyRequireWithNoConfigDirectoryIsACodedRefusal(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("RTA_CONFIG", "config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	root := NewRoot(testRegistry(t), "test")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"policy", "require", "-o", "json"})
	err := root.Execute()
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.policy.path" || !strings.Contains(ve.Hint, "RTA_CONFIG") {
		t.Fatalf("err = %#v, want core.policy.path naming RTA_CONFIG", err)
	}
	var buf bytes.Buffer
	if !RenderTopLevelError(&buf, root, err) || !json.Valid(buf.Bytes()) {
		t.Errorf("rendered %q, want the refusal as json", buf.String())
	}
}
