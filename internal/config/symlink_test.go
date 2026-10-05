package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config linked in from a dotfiles repository is still a link after rta
// writes it, and the repository's copy is what changed.
//
// Fails without writeTarget: the atomic rename replaces the link with a regular
// file, the repository's copy keeps its old text, and the Lstat below sees a
// plain file where the link was.
func TestAWriteFollowsAConfigThatIsASymlinkAndKeepsTheLink(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "dotfiles", "rta.yaml")
	link := filepath.Join(dir, "config", "config.yaml")
	for _, d := range []string{filepath.Dir(repo), filepath.Dir(link)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(repo, []byte("# kept in the repository\noutput: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", link)

	if err := Mutate(func(c Config) (Config, bool) {
		c.Dashboard.Columns = 3
		return c, true
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer a symlink: the write replaced the link", link)
	}
	got, err := os.ReadFile(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "columns: 3") || !strings.Contains(string(got), "# kept in the repository") {
		t.Errorf("the linked file was not the one written:\n%s", got)
	}
	if info, err := os.Stat(repo); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the linked file's mode changed: %v %v", info, err)
	}
}

// A link that points nowhere is written over as before, not followed into a
// path somebody else chose.
func TestADanglingConfigLinkIsWrittenWhereItIs(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(filepath.Join(dir, "missing", "rta.yaml"), link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", link)

	if err := Mutate(func(c Config) (Config, bool) {
		c.Output = "json"
		return c, true
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); err == nil {
		t.Error("a dangling link was followed into a directory it names")
	}
}

// The working-directory fallback is a file a cloned repository can supply, so a
// link in it is not followed: following it would let that repository have rta
// write over a file elsewhere in the account.
//
// Fails without the trustedPath check in writeTarget: the victim is rewritten.
func TestALinkInTheWorkingDirectoryFallbackIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("output: pretty # victim\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, ".rta.yaml")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	if err := Mutate(func(c Config) (Config, bool) {
		c.Output = "json"
		return c, true
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "output: pretty # victim\n" {
		t.Errorf("the file the link names was written: %q", got)
	}
}
