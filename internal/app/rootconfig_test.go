package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
)

// The tree is built from the configuration main already read, not from a
// second read of the file: the reading handed over says json, the file now
// says yaml, and the default of --output is the handed-over one. Without the
// option NewRoot still reads the file itself, which is what every other
// caller of it relies on.
func TestNewRootTakesTheConfigItIsGivenInsteadOfReadingTheFileAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("output: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
	t.Setenv("RTA_OUTPUT", "")
	reg := registry.New()
	reading, err := config.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("output: yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	defaultOutput := func(options ...RootOption) string {
		t.Helper()
		flag := NewRoot(reg, "test", options...).PersistentFlags().Lookup("output")
		if flag == nil {
			t.Fatal("the root has no --output flag")
		}
		return flag.DefValue
	}

	if got := defaultOutput(); got != "yaml" {
		t.Errorf("default output without the option = %q, want the file's yaml", got)
	}
	if got := defaultOutput(WithReading(reading, nil)); got != "json" {
		t.Errorf("default output with the option = %q, want the handed-over json", got)
	}
}

// A command reads the configuration's files once. With an environment switched
// on, a read at a terminal read them four times: for the tree, for the notice
// about keys rta does not read, to ask whether the call fans out, and to
// resolve the environment — each a parse of every file the tree had parsed
// already. Counted, and proved by what each step sees: the file is rewritten
// after the tree's read, and nothing the command does sees the rewrite.
func TestACommandReadsTheConfigurationOnce(t *testing.T) {
	reg := setRegistry(t)
	run := session(t, reg)
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=one.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	saved := stderrIsTerminal
	stderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { stderrIsTerminal = saved })
	reads := 0
	t.Cleanup(func() { readConfig = config.Read })
	readConfig = func() (config.Reading, error) {
		reads++
		return config.Read()
	}

	root := NewRoot(reg, "test")
	rewritten := strings.Replace(configOf(t), "one.internal", "two.internal", 1) + "oputput: json\n"
	if err := os.WriteFile(config.Path(), []byte(rewritten), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"db", "status"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("%v %q", err, errOut.String())
	}
	if reads != 1 {
		t.Errorf("the command read the configuration %d times, want once", reads)
	}
	if !strings.Contains(out.String(), "reached one.internal") {
		t.Errorf("the call ran on a second read of the file: %q", out.String())
	}
	if strings.Contains(errOut.String(), "rta does not read") {
		t.Errorf("the notice about ignored keys read the file again: %q", errOut.String())
	}
}
