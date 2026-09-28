package git

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// formatCapabilities is each capability by what it reads of a repository.
var formatCapabilities = map[string]plugin.Handler{
	"git.config": runConfig, "git.hooks": runHooks, "git.remotes": runRemotes, "git.status": runStatus,
	"git.log": runLog,
}

// withConfig is a repository with one commit whose config is config, after
// the [core] section every repository here has.
func withConfig(t *testing.T, config string) string {
	t.Helper()
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, ".git/config", "[core]\n\tbare = false\n"+config)
	return dir
}

// codes is the code each capability answers dir with, "" for an answer.
func codes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, run := range formatCapabilities {
		_, err := run(context.Background(), req(t, dir, nil))
		out[name] = errCode(err)
	}
	return out
}

// A repository in a format this reader does not read is refused, naming the
// extension, to the capabilities that read what the format changes: objects
// named by SHA-256 to the ones that read objects, refs kept in a reftable to
// the ones that read refs too. git.config and git.hooks read neither, and
// were refused both, so a hooks audit could not be run in such a repository.
func TestARepositoryInAFormatThisDoesNotReadIsRefusedOnlyWhereItChangesTheAnswer(t *testing.T) {
	for extension, refused := range map[string][]string{
		"objectFormat = sha256": {"git.log", "git.status"},
		"refStorage = reftable": {"git.log", "git.remotes", "git.status"},
	} {
		t.Run(extension, func(t *testing.T) {
			dir := withConfig(t, "\trepositoryformatversion = 1\n[extensions]\n\t"+extension+"\n")
			writeHook(t, dir, "pre-commit", true)
			for name, code := range codes(t, dir) {
				want := ""
				for _, r := range refused {
					if r == name {
						want = "git.repository.unsupported"
					}
				}
				if code != want {
					t.Errorf("%s = %q, want %q", name, code, want)
				}
			}
			_, err := runStatus(context.Background(), req(t, dir, nil))
			if err == nil || !strings.Contains(err.Error(), "extensions."+extension) {
				t.Errorf("the refusal %v does not name extensions.%s", err, extension)
			}
			if row := rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-commit"); row[1] != "active" {
				t.Errorf("pre-commit row = %v, want it active", row)
			}
		})
	}
}

// A repository made by git in either format is one git.config and git.hooks
// read, and git.remotes where its refs are files; every capability that
// reads what the format changes refuses naming why.
func TestARepositoryGitMadeInAnotherFormatIsAuditedAsGitMadeIt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git to make one with")
	}
	for name, flag := range map[string]string{"sha256": "--object-format=sha256", "reftable": "--ref-format=reftable"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, args := range [][]string{{"init", "-q", flag, dir}, {"-C", dir, "remote", "add", "origin", "https://example.com/r.git"}} {
				cmd := exec.Command("git", args...)
				cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Skipf("this git makes no %s repository: %v: %s", name, err, out)
				}
			}
			writeHook(t, dir, "pre-push", true)
			rowFor(t, table(t, runHooks, req(t, dir, nil)), "Name", "pre-push")
			rowFor(t, table(t, runConfig, req(t, dir, nil)), "Key", "core.repositoryformatversion")
			if name == "sha256" {
				rowFor(t, table(t, runRemotes, req(t, dir, nil)), "Remote", "origin")
			} else if _, err := runRemotes(context.Background(), req(t, dir, nil)); errCode(err) != "git.repository.unsupported" {
				t.Errorf("git.remotes = %v, want git.repository.unsupported", err)
			}
			if _, err := runStatus(context.Background(), req(t, dir, nil)); errCode(err) != "git.repository.unsupported" {
				t.Errorf("git.status = %v, want git.repository.unsupported", err)
			}
		})
	}
}

// Each extension, and each format version, as git 2.50 decides on it: a
// repository that sets no version passes over every extension, as the SHA-1
// repository of files it was before there were any; a version 0 one passes
// over an extension it does not know, and refuses one only version 1 has; a
// version 1 one refuses an extension git does not know and a value it does
// not take; and SHA-1 named as the object format, a map to another format
// kept beside it, objects kept precious or worktrees named relatively, is
// the repository it always was, where a map to the format the objects are
// named in already is one git 2.50 aborts at, and this opened. A value git
// does not take stops it whatever the version, none included, and so does no
// value at all for an extension that names something, `partialClone` alone
// on its line, which go-git reads as set to nothing and this opened. go-git
// refused most of them, by rules of its own, and the refusal said git read
// them. Where git is on PATH, it is asked too.
func TestARepositorysFormatIsDecidedAsGitDecidesIt(t *testing.T) {
	// What git on PATH says about these depends on how that git was built, not
	// on the repository: a map to a second object format is kept by code git
	// 2.55 builds only with Rust, and a git built without it refuses every
	// repository that names one ("compatibility hash algorithm support
	// requires Rust"), where 2.50 opened it. The objects are SHA-1 either way,
	// which is all a reader reads, so the answer here stays the same and only
	// the comparison with git is left out.
	buildDecides := map[string]bool{
		"\trepositoryformatversion = 1\n[extensions]\n\tcompatObjectFormat = sha256\n": true,
	}
	for config, want := range map[string]string{
		"[extensions]\n\tobjectFormat = sha256\n":                                                                   "",
		"[extensions]\n\trefStorage = reftable\n":                                                                   "",
		"[extensions]\n\tsomethingNew = yes\n":                                                                      "",
		"[extensions]\n\tpreciousObjects = true\n":                                                                  "",
		"[extensions]\n\tpreciousObjects = 1k\n":                                                                    "",
		"[extensions]\n\tpreciousObjects = OFF\n":                                                                   "",
		"[extensions]\n\tpreciousObjects = 1x\n":                                                                    "git.repository.invalid",
		"[extensions]\n\trefStorage = bogus\n":                                                                      "git.repository.invalid",
		"[extensions]\n\tobjectFormat = SHA256\n":                                                                   "git.repository.invalid",
		"[extensions]\n\tpartialClone\n":                                                                            "git.repository.invalid",
		"[extensions]\n\tpartialClone =\n":                                                                          "",
		"\trepositoryformatversion = 0\n[extensions]\n\trelativeWorktrees = true\n":                                 "git.repository.invalid",
		"\trepositoryformatversion = 0\n[extensions]\n\tworktreeConfig = maybe\n":                                   "git.repository.invalid",
		"\trepositoryformatversion = 1\n[extensions]\n\trelativeWorktrees = true\n":                                 "",
		"\trepositoryformatversion = 1\n[extensions]\n\trelativeWorktrees = maybe\n":                                "git.repository.invalid",
		"\trepositoryformatversion = 0\n[extensions]\n\tobjectFormat = sha256\n":                                    "git.repository.invalid",
		"\trepositoryformatversion = 0\n[extensions]\n\trefStorage = files\n":                                       "git.repository.invalid",
		"\trepositoryformatversion = 0\n[extensions]\n\tnoop-v1 = true\n":                                           "git.repository.invalid",
		"\trepositoryformatversion = 0\n[extensions]\n\tsomethingNew = yes\n":                                       "",
		"\trepositoryformatversion = 1\n[extensions]\n\tobjectFormat = sha1\n":                                      "",
		"\trepositoryformatversion = 1\n[extensions]\n\tcompatObjectFormat = sha256\n":                              "",
		"\trepositoryformatversion = 1\n[extensions]\n\tcompatObjectFormat = sha1\n":                                "git.repository.invalid",
		"\trepositoryformatversion = 1\n[extensions]\n\tobjectFormat = sha256\n\tcompatObjectFormat = sha256\n":     "git.repository.invalid",
		"\trepositoryformatversion = 1\n[extensions]\n\tcompatObjectFormat = sha256\n\tobjectFormat = sha256\n":     "git.repository.invalid",
		"[extensions]\n\tcompatObjectFormat = sha1\n":                                                               "",
		"[extensions]\n\tcompatObjectFormat = sha256\n\tcompatObjectFormat = sha256\n":                              "git.repository.invalid",
		"\trepositoryformatversion = 1\n[extensions]\n\tcompatObjectFormat = sha256\n\tCompatObjectFormat = sha1\n": "git.repository.invalid",
		"\trepositoryformatversion = 1\n[extensions]\n\tsomethingNew = yes\n":                                       "git.repository.unsupported",
		"\trepositoryformatversion = 1\n[extensions]\n\tobjectFormat = SHA256\n":                                    "git.repository.invalid",
		"\trepositoryformatversion = 1\n[extensions]\n\tobjectFormat\n":                                             "git.repository.invalid",
		"\trepositoryformatversion = 1\n[extensions]\n\tpartialClone\n":                                             "git.repository.invalid",
		"\trepositoryformatversion = 2\n":                                                                           "git.repository.unsupported",
		"\trepositoryformatversion = one\n":                                                                         "git.repository.invalid",
	} {
		dir := withConfig(t, config)
		for name, code := range codes(t, dir) {
			if code != want {
				t.Errorf("with %q, %s = %q, want %q", config, name, code, want)
			}
		}
		if _, err := exec.LookPath("git"); err != nil || buildDecides[config] {
			continue
		}
		cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if gitOpens := err == nil; gitOpens != (want == "") {
			t.Errorf("with %q, git opens it: %v (%s), and this answers %q", config, gitOpens, out, want)
		}
	}
}
