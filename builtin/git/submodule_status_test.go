package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkoutWithSubmodule is a repository whose one submodule is cloned and
// committed at the commit the index records, built by git itself: what go-git
// would write for one is not what the status has to read.
func checkoutWithSubmodule(t *testing.T) (parent, lib string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git to build a submodule with")
	}
	root := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{
			"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "protocol.file.allow=always",
			"-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false",
		}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	origin := filepath.Join(root, "origin")
	parent = filepath.Join(root, "parent")
	for _, dir := range []string{origin, parent} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		run(dir, "init", "-q")
	}
	writeFile(t, origin, "lib.txt", "one\n")
	run(origin, "add", "lib.txt")
	run(origin, "commit", "-qm", "init")
	run(parent, "submodule", "add", "-q", origin, "lib")
	run(parent, "commit", "-qm", "add lib")
	return parent, filepath.Join(parent, "lib")
}

// git lists a submodule with a change inside it as ` M lib`, whether the change
// is an edit, a staged one or a file nothing tracks. go-git compares a
// submodule's HEAD with the index's commit and nothing more, so one edited in
// place read as clean: the status said there was nothing to commit about a
// checkout git reports as modified.
func TestStatusReportsASubmoduleWithAChangeInsideIt(t *testing.T) {
	parent, lib := checkoutWithSubmodule(t)
	if tbl := table(t, runStatus, req(t, parent, nil)); len(tbl.Rows) != 0 {
		t.Fatalf("a clean checkout lists %v", tbl.Rows)
	}

	for name, change := range map[string]func(){
		"an untracked file": func() { writeFile(t, lib, "new.txt", "x\n") },
		"an edited file":    func() { writeFile(t, lib, "lib.txt", "two\n") },
	} {
		change()
		tbl := table(t, runStatus, req(t, parent, nil))
		if len(tbl.Rows) != 1 || tbl.Rows[0][0] != "lib" || tbl.Rows[0][2] != "M" {
			t.Errorf("a submodule with %s in it lists %v, want lib modified in the worktree column", name, tbl.Rows)
		}
		_ = os.Remove(filepath.Join(lib, "new.txt"))
		writeFile(t, lib, "lib.txt", "one\n")
	}

	if tbl := table(t, runStatus, req(t, parent, nil)); len(tbl.Rows) != 0 {
		t.Errorf("a submodule put back lists %v", tbl.Rows)
	}
}

// And git's own switch for leaving a submodule's content out is honoured where
// the config git reads has it.
func TestStatusLeavesOutASubmoduleGitIsToldToIgnore(t *testing.T) {
	parent, lib := checkoutWithSubmodule(t)
	writeFile(t, lib, "new.txt", "x\n")
	cfg := filepath.Join(parent, ".git", "config")
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}

	for ignore, wantRows := range map[string]int{"untracked": 0, "dirty": 0, "all": 0, "none": 1} {
		text := string(body) + "[submodule \"lib\"]\n\tignore = " + ignore + "\n"
		if err := os.WriteFile(cfg, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := len(table(t, runStatus, req(t, parent, nil)).Rows); got != wantRows {
			t.Errorf("ignore = %s lists %d rows, want %d", ignore, got, wantRows)
		}
	}
}

// porcelainRows is how many paths `git status --porcelain` lists, the oracle for a
// setting the status has to read the way git does.
func porcelainRows(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	return len(strings.Fields(strings.ReplaceAll(string(out), "\n", " "))) / 2
}

// The place a repository says most often to leave a submodule out is the
// .gitmodules it commits, under the submodule's name, which is not always its
// path, and a setting in the config files it does not commit beats it.
func TestStatusReadsTheIgnoreTheGitmodulesDeclares(t *testing.T) {
	parent, lib := checkoutWithSubmodule(t)
	writeFile(t, lib, "lib.txt", "two\n")
	modules := filepath.Join(parent, ".gitmodules")
	body, err := os.ReadFile(modules)
	if err != nil {
		t.Fatal(err)
	}
	named := strings.ReplaceAll(string(body), `submodule "lib"`, `submodule "vendored"`)
	cfg := filepath.Join(parent, ".git", "config")
	cfgBody, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct{ modules, config string }{
		"dirty in .gitmodules":                 {named + "\tignore = dirty\n", ""},
		"all in .gitmodules, under a name":     {named + "\tignore = all\n", ""},
		"none in .gitmodules":                  {named + "\tignore = none\n", ""},
		"all beaten by none in the config":     {named + "\tignore = all\n", "[submodule \"vendored\"]\n\tignore = none\n"},
		"none beaten by all in the config":     {named + "\tignore = none\n", "[submodule \"vendored\"]\n\tignore = all\n"},
		"a setting for another name is not it": {named + "\tignore = all\n", "[submodule \"lib\"]\n\tignore = none\n"},
	} {
		if err := os.WriteFile(modules, []byte(c.modules), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg, []byte(string(cfgBody)+c.config), 0o644); err != nil {
			t.Fatal(err)
		}
		want := porcelainRows(t, parent)
		if got := len(table(t, runStatus, req(t, parent, nil)).Rows); got != want {
			t.Errorf("%s: %d rows, where git lists %d", name, got, want)
		}
	}
}
