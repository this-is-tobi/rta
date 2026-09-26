package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
