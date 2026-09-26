package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/policy"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A policy file already there is refused, coded and in the format asked for,
// with --force named: it was the plain error fang styled as a box under
// `-o json`, which is where a script that sets a repository up would read it.
func TestPolicyInitOverAnExistingFileIsACodedRefusal(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, errOut, err := run(t, testRegistry(t), "policy", "init"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	_, _, err := run(t, testRegistry(t), "policy", "init", "-o", "json")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.policy.exists" || !strings.Contains(ve.Hint, "--force") {
		t.Fatalf("err = %#v, want core.policy.exists naming --force", err)
	}
	var buf bytes.Buffer
	if !RenderTopLevelError(&buf, NewRoot(testRegistry(t), "test"), err) {
		t.Fatal("the refusal was left for fang")
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
