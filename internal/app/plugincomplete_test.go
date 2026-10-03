package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func writeCompletionFixture(t *testing.T) {
	t.Helper()
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	manifest := func(name, summary string) string {
		return "name: " + name + "\nversion: v0.1.0\nsummary: " + summary + "\nplatforms:\n- os: linux\n  arch: amd64\n" +
			"  url: https://example.test/" + name + "\n  sha256: " + strings.Repeat("ab", 32) + "\n" +
			"capabilities:\n- id: " + name + ".run\n  summary: runs\n  safety: read\n"
	}
	index := filepath.Join(data, "indexes", "mine", "index")
	if err := os.MkdirAll(index, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, summary := range map[string]string{"hello": "Greets", "pg": "Postgres", "vault": "Vault"} {
		if err := os.WriteFile(filepath.Join(index, name+".yaml"), []byte(manifest(name, summary)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lock := `{"plugins":[{"name":"pg","digest":"` + strings.Repeat("cd", 32) + `","version":"v0.1.0","index":"mine"}]}`
	if err := os.MkdirAll(filepath.Join(data, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "plugins", "rta.lock"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(completions []cobra.Completion) []string {
	out := make([]string, len(completions))
	for i, c := range completions {
		out[i], _, _ = strings.Cut(c, "\t")
	}
	return out
}

// `rta plugin install <TAB>` completed the files of the current directory: the
// names an attached index claims are on disk already, and what is installed is
// not worth offering again. upgrade and remove act on what is installed, and
// the index commands on the indexes — none of them had a completion either.
func TestPluginCommandsCompleteTheNamesTheyTake(t *testing.T) {
	writeCompletionFixture(t)

	got, directive := completeInstallable(nil, nil, "")
	if strings.Join(names(got), ",") != "hello,vault" || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("install offers %v (%v), want the claimed names that are not installed", names(got), directive)
	}
	if got, _ := completeInstallable(nil, []string{"hello"}, ""); len(got) != 0 {
		t.Errorf("a second argument was completed: %v", got)
	}

	got, _ = completeManaged(nil, nil, "")
	if strings.Join(names(got), ",") != "pg" || !strings.Contains(got[0], "v0.1.0 from mine") {
		t.Errorf("upgrade and remove offer %v, want the installed plugin described by its version and index", got)
	}

	got, _ = completeIndexes(nil, nil, "")
	if strings.Join(names(got), ",") != "mine" {
		t.Errorf("the index commands offer %v, want the attached index", got)
	}
}

func TestPluginCommandsAreWiredToTheirCompletions(t *testing.T) {
	root := NewRoot(testRegistry(t), "test")
	for path, want := range map[string]bool{
		"plugin install": true, "plugin upgrade": true, "plugin remove": true,
		"plugin index update": true, "plugin index remove": true,
	} {
		cmd, _, err := root.Find(strings.Fields(path))
		if err != nil || cmd == nil {
			t.Fatalf("%s: %v", path, err)
		}
		if (cmd.ValidArgsFunction != nil) != want {
			t.Errorf("%s: completion wired = %v, want %v", path, cmd.ValidArgsFunction != nil, want)
		}
	}
}
