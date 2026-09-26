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
