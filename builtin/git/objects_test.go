package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A repository whose objects this reader cannot all see is refused, not
// answered.
//
// go-git finds packfiles by name and by nothing else: ObjectPacks keeps only
// `pack-<hash>.pack` and takes the hash from the filename itself. git makes
// no such promise. `git maintenance run --task=loose-objects` — which `git
// maintenance start` schedules, so a great many working repositories have
// run it — writes its pack as `loose-<hash>.pack`, and every object inside
// one is simply absent as far as this reader is concerned.
//
// Absent, and not an error, which is the whole problem: a tree that will not
// load reads as a tree that was never there. On this project's own checkout
// that turned a clean working tree into `git status` reporting three hundred
// and seventy-five files as newly staged, and it would truncate a log or a
// diff the same quiet way. Wrong-and-plausible about the state of somebody's
// repository is the one answer a boundary must not give.
func TestARepositoryWithAPackTheReaderSkipsIsRefused(t *testing.T) {
	dir, repo := testRepo(t)
	// A subdirectory, because the silent form of this needs a tree that
	// loads pointing at one that does not. With every object invisible the
	// reader fails outright and says so, which is the harmless shape; the
	// shape that shipped a wrong answer is a readable commit whose subtree
	// is in the pack nobody opens.
	commitFile(t, repo, dir, "sub/x.txt", "one\n", "initial")
	if err := repo.RepackObjects(&git.RepackConfig{}); err != nil {
		t.Fatal(err)
	}
	dropLooseObjects(t, dir)
	// The second commit writes a new root tree and a new commit as loose
	// objects, and reuses the subtree from the pack. Renaming the pack after
	// it is what leaves exactly the real arrangement: HEAD readable, sub/
	// not.
	commitFile(t, repo, dir, "top.txt", "two\n", "second")
	renamePackTheWayMaintenanceDoes(t, dir)

	v, err := runStatus(context.Background(), req(t, dir, nil))
	if err == nil {
		t.Fatalf("a repository whose objects are half invisible answered anyway: %+v", v)
	}
	verr := view.AsError(err, "git.status.failed")
	if verr.Code != "git.objects.unreadable" {
		t.Errorf("code = %q, want git.objects.unreadable (%v)", verr.Code, verr.Message)
	}
	// The one command that fixes it, since the operator cannot be expected to
	// know that a packfile's name is load-bearing.
	if !strings.Contains(verr.Hint, "git repack") {
		t.Errorf("hint = %q, want it to name the repack that renames the pack", verr.Hint)
	}
	// Named, because a repository can hold several and the operator is owed
	// the evidence rather than an assertion about their machine.
	if !strings.Contains(verr.Message, "loose-") {
		t.Errorf("message = %q, want it to name the packfile it will not open", verr.Message)
	}
}

// The three capabilities whose answers never come out of a packfile keep
// answering.
//
// `git config` reads .git/config, `git hooks` reads .git/hooks, and
// `git remotes` reads the refs and the configured remotes. A half-readable
// object database cannot make any of those wrong, so refusing them would
// report a fault in an answer that does not have one — the same discipline
// as the profile badge that stays silent unless it has something to say.
func TestTheCapabilitiesThatReadNoObjectsStillAnswer(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "sub/x.txt", "one\n", "initial")
	if err := repo.RepackObjects(&git.RepackConfig{}); err != nil {
		t.Fatal(err)
	}
	dropLooseObjects(t, dir)
	commitFile(t, repo, dir, "top.txt", "two\n", "second")
	renamePackTheWayMaintenanceDoes(t, dir)

	for name, h := range map[string]func(context.Context, plugin.Request) (view.View, error){
		"git.config":  runConfig,
		"git.hooks":   runHooks,
		"git.remotes": runRemotes,
	} {
		if _, err := h(context.Background(), req(t, dir, nil)); err != nil {
			t.Errorf("%s refused a repository whose objects it never reads: %v", name, err)
		}
	}
}

// And the guard stays off the ordinary repository, which is the half that
// makes it worth having: a pack under the name go-git expects is read, and
// packing objects is not itself a fault.
func TestAnOrdinarilyPackedRepositoryIsStillAnswered(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "hello\n", "initial")
	if err := repo.RepackObjects(&git.RepackConfig{}); err != nil {
		t.Fatal(err)
	}
	dropLooseObjects(t, dir)

	tbl := table(t, runStatus, req(t, dir, nil))
	if len(tbl.Rows) != 0 {
		t.Errorf("rows = %v, want none — the checkout is clean and its pack is readable", tbl.Rows)
	}
}

// dropLooseObjects removes every loose object, leaving the packs as the only
// place the repository's history lives — which is the state `git maintenance`
// leaves behind, and the state in which a skipped pack costs real objects.
func dropLooseObjects(t *testing.T, dir string) {
	t.Helper()
	objects := filepath.Join(dir, ".git", "objects")
	entries, err := os.ReadDir(objects)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		// The two-hex fan-out directories, and nothing else: pack/ and info/
		// are not loose objects.
		if !e.IsDir() || len(e.Name()) != 2 {
			continue
		}
		if err := os.RemoveAll(filepath.Join(objects, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
}

// renamePackTheWayMaintenanceDoes gives the packs the name `git maintenance
// run --task=loose-objects` gives the one it writes.
func renamePackTheWayMaintenanceDoes(t *testing.T, dir string) {
	t.Helper()
	packDir := filepath.Join(dir, ".git", "objects", "pack")
	entries, err := os.ReadDir(packDir)
	if err != nil {
		t.Fatal(err)
	}
	renamed := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "pack-") {
			continue
		}
		to := "loose-" + strings.TrimPrefix(name, "pack-")
		if err := os.Rename(filepath.Join(packDir, name), filepath.Join(packDir, to)); err != nil {
			t.Fatal(err)
		}
		renamed++
	}
	if renamed == 0 {
		t.Fatal("no pack to rename — the repack did not produce one")
	}
}

// Reading a repository keeps its packfiles open between the objects it reads,
// where go-git's default storage opened the pack again for each, and every
// capability closes them when it returns: a descriptor left open by each call
// is one a long-running server runs out of. Counted across calls with the
// collector off, since a file the collector finds unreachable closes itself
// and would hide the leak this looks for.
func TestEveryCapabilityClosesThePacksItKeptOpen(t *testing.T) {
	descriptors := func() int {
		entries, err := os.ReadDir("/dev/fd")
		if err != nil {
			t.Skipf("no /dev/fd here to count descriptors in: %v", err)
		}
		return len(entries)
	}
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "sub/a.txt", "one\n", "initial")
	commitFile(t, repo, dir, "sub/a.txt", "one\ntwo\n", "second")
	if err := repo.RepackObjects(&git.RepackConfig{}); err != nil {
		t.Fatal(err)
	}
	dropLooseObjects(t, dir)
	writeFile(t, dir, "sub/a.txt", "one\ntwo\nthree\n")

	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	type call struct {
		name   string
		run    func() error
		values map[string]any
	}
	var calls []call
	for _, c := range Plugin().Capabilities {
		values := map[string]any{"file": filepath.Join(dir, "sub", "a.txt"), "detail": true}
		calls = append(calls, call{name: c.ID, values: values, run: func() error {
			_, err := c.Run(context.Background(), req(t, dir, values))
			return err
		}})
	}
	calls = append(calls,
		call{name: "git.diff --commit", run: func() error {
			_, err := runDiff(context.Background(), req(t, dir, map[string]any{"commit": "HEAD"}))
			return err
		}},
		call{name: "the commits git.diff suggests", run: func() error {
			if len(suggestCommits(context.Background(), req(t, dir, nil))) == 0 {
				return errors.New("no commits suggested")
			}
			return nil
		}})
	for _, c := range calls {
		if err := c.run(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		before := descriptors()
		for range 5 {
			_ = c.run()
		}
		if after := descriptors(); after > before {
			t.Errorf("%s left %d descriptors open over five calls", c.name, after-before)
		}
	}
}

// A partial clone lacks objects on purpose, and git fetches each as it needs
// it; this reader fetches nothing, and failed at the first it lacked. The
// capabilities that read objects refuse one up front, naming the remote git
// fetches from, whether git marked it as `git clone --filter` does now,
// remote.<name>.promisor, true or with no value at all on any line, a later
// false taking nothing away, or by a remote.<name>.partialCloneFilter alone,
// which git makes a promisor of whatever the promisor line says; or as older
// git did, extensions.partialClone, which git reads only where the config
// sets a format version. The ones that read no object answer.
func TestAPartialCloneIsRefusedToTheCapabilitiesThatReadObjects(t *testing.T) {
	machineConfig(t, "")
	const origin = "[remote \"origin\"]\n\turl = https://example.com/r.git\n"
	for config, partial := range map[string]bool{
		origin + "\tpromisor = true\n\tpartialclonefilter = blob:none\n": true,
		origin + "\tpromisor\n":                                                                true,
		origin + "\tpromisor = false\n":                                                        false,
		origin + "\tpartialCloneFilter = blob:none\n":                                          true,
		origin + "\tpromisor = false\n\tpartialclonefilter = tree:0\n":                         true,
		origin + "\tpartialclonefilter =\n":                                                    true,
		origin + "\tpromisor = true\n[remote \"origin\"]\n\tpromisor = false\n":                true,
		origin + "\tpromisor\n\tpromisor = false\n":                                            true,
		"\trepositoryformatversion = 1\n" + origin + "[extensions]\n\tpartialClone = origin\n": true,
		origin + "[extensions]\n\tpartialClone = origin\n":                                     false,
	} {
		dir := withConfig(t, config)
		for name, code := range codes(t, dir) {
			want := ""
			if partial && (name == "git.status" || name == "git.log") {
				want = "git.objects.partial"
			}
			if code != want {
				t.Errorf("with %q, %s = %q, want %q", config, name, code, want)
			}
		}
		if _, err := runStatus(context.Background(), req(t, dir, nil)); partial && (err == nil || !strings.Contains(err.Error(), "from origin")) {
			t.Errorf("the refusal %v does not name the remote", err)
		}
	}
}

// git reads remote.<name>.promisor from every file of config it reads and
// from its environment, as `git -c` sets it: a line in the operator's global
// or system file makes git fetch what a repository lacks from that remote, as
// the repository's own line would. This read the repository's config alone,
// and let such a clone through. It is refused, the hint naming the file; the
// format's extensions.partialClone is read from the repository's config
// alone, as git reads it. A file git reads and this cannot is refused, as git
// runs nothing with it. Where git is on PATH, it is asked whether it fetches.
func TestAPromisorSetInAnyConfigGitReadsMakesAPartialClone(t *testing.T) {
	origin := "[remote \"origin\"]\n\turl = " + filepath.Join(t.TempDir(), "gone") + "\n"
	for what, c := range map[string]struct {
		global, system, parameters string
		want, named                string
	}{
		"global":                   {global: "[remote \"origin\"]\n\tpromisor = true\n", want: "git.objects.partial", named: ".gitconfig"},
		"global, with no value":    {global: "[remote \"origin\"]\n\tpromisor\n", want: "git.objects.partial", named: ".gitconfig"},
		"global, false":            {global: "[remote \"origin\"]\n\tpromisor = false\n"},
		"global filter":            {global: "[remote \"origin\"]\n\tpartialCloneFilter = blob:none\n", want: "git.objects.partial", named: ".gitconfig"},
		"system":                   {system: "[remote \"origin\"]\n\tpromisor = yes\n", want: "git.objects.partial", named: "system"},
		"git -c":                   {parameters: "'remote.origin.promisor'='true'", want: "git.objects.partial", named: "environment"},
		"git -c, with no value":    {parameters: "'remote.origin.promisor'", want: "git.objects.partial", named: "environment"},
		"global partialClone":      {global: "[extensions]\n\tpartialClone = origin\n"},
		"git -c partialClone":      {parameters: "'extensions.partialClone'='origin'"},
		"a global git cannot read": {global: "[remote \"origin\"\n", want: "git.config.unreadable"},
	} {
		home := machineConfig(t, c.global)
		if c.system != "" {
			writeFile(t, home, "system-gitconfig", c.system)
			t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, "system-gitconfig"))
		} else {
			t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
		}
		if c.parameters != "" {
			t.Setenv("GIT_CONFIG_PARAMETERS", c.parameters)
		}
		dir := withConfig(t, "\trepositoryformatversion = 1\n"+origin)
		for name, code := range codes(t, dir) {
			want := ""
			switch {
			case name == "git.status" || name == "git.log":
				want = c.want
			case c.want == "git.config.unreadable":
				// git.config, git.hooks and git.remotes read the same
				// files, and fail on it in their own words.
				want = name + ".failed"
			}
			if code != want {
				t.Errorf("%s, %s = %q, want %q", what, name, code, want)
			}
		}
		_, err := runLog(context.Background(), req(t, dir, nil))
		var verr *view.Error
		if c.named != "" && (!errors.As(err, &verr) || !strings.Contains(verr.Message, "from origin") ||
			!strings.Contains(verr.Hint, c.named)) {
			t.Errorf("%s, the refusal %v does not name origin and %s", what, err, c.named)
		}
		if fetches, ok := fetchesByGit(t, dir, "origin"); ok && c.want != "git.config.unreadable" && fetches != (c.want != "") {
			t.Errorf("%s, git fetches what the repository lacks: %v, and this answers %q", what, fetches, c.want)
		}
	}
}

// fetchesByGit is whether the git on PATH, asked for an object dir does not
// hold, fetches it from remote, as git's trace spells the name; ok is false
// where there is no git to ask.
func fetchesByGit(t *testing.T, dir, remote string) (fetches, ok bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		return false, false
	}
	cmd := exec.Command("git", "-C", dir, "cat-file", "-e", strings.Repeat("1", 40))
	cmd.Env = append(os.Environ(), "GIT_TRACE=1")
	out, _ := cmd.CombinedOutput()
	return strings.Contains(string(out), "run_command: git") && strings.Contains(string(out), " fetch "+remote+" "), true
}

// extensions.partialClone set to nothing names a remote, the one named
// nothing: git reads the repository as a partial clone, and fetches what it
// lacks by running git fetch with the empty name. This read the empty name as
// none. Where the config sets no format version git passes over every
// extension, and it is no partial clone; with no value at all it is one git
// refuses to open (TestARepositorysFormatIsDecidedAsGitDecidesIt).
func TestAPartialCloneExtensionSetToNothingIsAPartialClone(t *testing.T) {
	machineConfig(t, "")
	for config, partial := range map[string]bool{
		"\trepositoryformatversion = 1\n[extensions]\n\tpartialClone =\n": true,
		"\trepositoryformatversion = 0\n[extensions]\n\tpartialClone =\n": true,
		"[extensions]\n\tpartialClone =\n":                                false,
	} {
		dir := withConfig(t, config)
		_, err := runLog(context.Background(), req(t, dir, nil))
		if got := errCode(err) == "git.objects.partial"; got != partial ||
			partial && !strings.Contains(err.Error(), `lacks from "" as`) {
			t.Errorf("with %q, git.log = %v, want it refused as a partial clone: %v", config, err, partial)
		}
		if fetches, ok := fetchesByGit(t, dir, "''"); ok && fetches != partial {
			t.Errorf("with %q, git fetches what the repository lacks: %v", config, fetches)
		}
	}
}

// A partial clone git made is refused as one.
func TestAPartialCloneGitMadeIsRefusedAsOne(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git to make one with")
	}
	src, repo := testRepo(t)
	commitFile(t, repo, src, "a.txt", "v1\n", "initial")
	clone := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "-c", "uploadpack.allowFilter=true", "clone", "-q", "--no-local", "--filter=blob:none",
		"file://"+src, clone)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git made no partial clone: %v: %s", err, out)
	}
	if _, err := runLog(context.Background(), req(t, clone, nil)); errCode(err) != "git.objects.partial" {
		t.Errorf("git.log of a partial clone = %v, want git.objects.partial", err)
	}
	rowFor(t, table(t, runRemotes, req(t, clone, nil)), "Remote", "origin")
}
