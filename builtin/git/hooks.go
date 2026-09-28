package git

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func hooksCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.hooks",
		Summary:      "What's in the hooks directory, and which of it git would actually run",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "Every entry in the directory git runs this repository's hooks from — " +
			"core.hooksPath when any config git reads sets it, the repository's own hooks directory " +
			"otherwise — judged by the same rule git itself uses to decide whether one fires on " +
			"commit, push and the rest: named exactly (a `.sample` suffix never runs) and executable, " +
			"a symbolic link by what it leads to, as git follows one. Anything but a file that passes " +
			"that rule — a directory, a link to one, a named pipe — is one git tries to run and " +
			"cannot, failing the command it guards, and is listed as fails. Over MCP a link leading out of " +
			"the roots is not followed, and is listed active, as git may run what it leads to. " +
			"The config is read from every file git reads and from the environment, as git.config " +
			"reads them, each include followed as git follows it; one this cannot decide is not, and a " +
			"warning says how many were not. core.hooksPath set to nothing is the top of the filesystem, where git looks " +
			"for /pre-commit and the rest, and a warning says so; set with no value at all, it is " +
			"refused, as git refuses to run with it. " +
			"A hook is an arbitrary script that runs on this machine, so this reports what would " +
			"actually execute, and where each file is, not merely what a directory listing shows.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runHooks,
	}
}

func runHooks(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, done, verr := openRepoConfigOnly(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer done()

	fs, verr := hooksFilesystem(repo)
	if verr != nil {
		return nil, verr
	}
	dir, base, hidden, warnings, err := hooksDir(ctx, req, repo, fs)
	if verr := refusedByTheGate(err); verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, view.Errorf("git.hooks.failed", "reading core.hooksPath from git's config: %v", err)
	}
	// A directory this handler derives, from a config a caller can write
	// inside the root and from the operator's own, so it is put back to the
	// host as repoRoot puts back the repository: a core.hooksPath naming
	// ~/.githooks from a root drawn around one project lists nothing outside
	// it. And read at the place the host judged, symlinks resolved, so a
	// hooks directory linked out of the root is refused rather than followed.
	// A value read from a file the caller is not shown is not named in the
	// refusal either, being one of its values.
	judged, verr := req.Confine("path", dir)
	if verr != nil && hidden {
		return nil, view.Errorf(verr.Code, "path: core.hooksPath, set in a file the repository's config includes "+
			"from outside this server's roots, names a directory outside them").WithHint(verr.Hint)
	}
	if verr != nil {
		return nil, verr
	}
	// os.ReadDir returns entries already sorted by name.
	entries, err := os.ReadDir(judged)
	if err != nil && !errors.Is(err, iofs.ErrNotExist) {
		return nil, view.Errorf("git.hooks.failed", "reading hooks in %s: %v", shownFrom(base, dir), err)
	}

	t := view.Table{
		Columns: []view.Column{
			{Name: "Name"},
			{Name: "Status", Kind: view.KindStatus},
			{Name: "Path"},
		},
		Empty:    "no hooks in " + shownFrom(base, dir),
		Warnings: warnings,
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		mode, at, unfollowed := hookMode(req, judged, name, info)
		t.Rows = append(t.Rows, []string{
			strings.TrimSuffix(name, ".sample"), hookStatus(name, mode, unfollowed, at != "" && mayExecute(at, mode)),
			shownFrom(base, filepath.Join(dir, name)),
		})
	}
	t.Total = len(t.Rows)
	return t, nil
}

// hookStatus is what git does with the entry name of a hooks directory, by
// the mode it judges it by (hookMode) and whether access(2) finds it
// executable (mayExecute): never runs a `.sample`, passes over what
// access(2) refuses (disabled), runs a file with an execute bit (active),
// and tries to run anything else access(2) lets through and cannot (fails).
// A link not looked through is active, as git may run what it leads to.
//
// **git runs what it finds at a hook's name, not only a file.** It asks
// access(2) whether the name is executable, and a directory that may be
// searched, a link to one, or a named pipe with an execute bit, all say yes;
// exec(2) then refuses anything but a file, and git fails with "cannot exec
// '.git/hooks/pre-commit': Permission denied" on every commit, a commit a
// pre-commit guards not made, a push a pre-push guards not sent. This left a
// directory out and listed a link to one nowhere, and a named pipe as active:
// a hooks directory whose pre-commit broke every commit answered that nothing
// ran, or that a script did. Listed as fails, which is what git does with it
// each time, and the word renderers already draw as a failure. One access(2)
// refuses is passed over as a file git will not run is, with the hint git
// prints for it, and is disabled.
//
// **access(2) is asked, as git asks it, not read off the mode.** Any execute
// bit was taken for access(2) saying yes, which it says by the bits of the
// entry's owner to its owner alone: a hook whose owner's bits have none, or
// another user's with an execute bit for its owner only, was listed active
// where git passes over it with its hint. Root may search a directory with
// no execute bit, and git run as root fails on one this listed disabled. And
// on macOS an ACL granting execute makes access(2) say yes of a script whose
// mode has no execute bit, which exec(2) then refuses: every commit failed,
// "cannot exec", where this answered that git passed over the script. So
// what access(2) lets through is run, and fails unless it is a file with an
// execute bit.
func hookStatus(name string, mode os.FileMode, unfollowed, executable bool) string {
	switch {
	case strings.HasSuffix(name, ".sample"):
		return "sample"
	case unfollowed:
		return "active"
	case !executable:
		return "disabled"
	case mode.IsRegular() && mode&0o111 != 0:
		return "active"
	}
	return "fails"
}

// hookMode is the mode git judges the entry name of the hooks directory dir
// by, whose own is info's: for a symbolic link, the mode of what it leads to,
// zero where it leads nowhere, which git runs nothing from. at is where that
// mode was read, the place access(2) is asked about (hookStatus), empty with
// a zero mode. unfollowed is a link the caller may not be told about the far
// end of, which is not looked through (plugin.Request.LinkTarget).
//
// **git follows a link at a hook's name, and this judged the link itself.**
// git runs a hook where access(2) finds it executable, which is a question
// about what the link leads to: a link to a script is a hook git runs,
// whatever the link's own mode, and a link to a directory is one it tries to
// run and fails on, as it does a directory (hookStatus). This read the link's
// own mode.
// On Linux that is always rwx, so a link to a file git will not run was
// listed active; on macOS a link's mode can be set apart from its target's
// (`chmod -h 644`), and a link to a script git ran on every commit was
// listed disabled, a hook hidden from the one answer an audit relies on.
//
// Over MCP, a link whose far end is outside the roots is not followed, as a
// path argument's is not, since its mode would say what is there: it is
// listed active, as what it leads to may be a script git runs, rather than
// judged by a mode that is not the one git asks about.
func hookMode(req plugin.Request, dir, name string, info os.FileInfo) (mode os.FileMode, at string, unfollowed bool) {
	path := filepath.Join(dir, name)
	if info.Mode()&os.ModeSymlink == 0 {
		return info.Mode(), path, false
	}
	target, err := os.Readlink(path)
	if err != nil {
		return 0, "", false
	}
	if req.LinkTarget(dir, target) != target {
		return 0, "", true
	}
	judged, verr := req.Confine("path", path)
	if verr != nil {
		return 0, "", true
	}
	far, err := os.Stat(judged)
	if err != nil {
		return 0, "", false
	}
	return far.Mode(), judged, false
}

// hooksDir is the directory git runs this repository's hooks from, and the
// directory git runs them in, which a relative one is taken from and which a
// path is shown from.
//
// **core.hooksPath first, from every config git reads.** git reads the hooks
// directory only when core.hooksPath is unset in the repository's config, the
// operator's global one and the system's — husky sets it, and so do many
// organisations for every repository at once. This listed the hooks directory
// whatever the config said, and answered "no active hooks" for a repository
// whose pre-commit git ran on every commit. The capability is the answer
// somebody auditing a checkout relies on for what would execute, so the
// operator's own config is read for this one key on every surface, MCP
// included — git.config withholds those scopes there because they hold
// credentials, and a directory is not one; leaving it unread would answer
// wrongly instead, and the directory it names is put to the host like any
// other. An include in any of those files is followed as git follows it, and
// hidden is a value read from a file the caller is not shown
// (scopedConfig.hidden).
//
// A relative value is taken from where git runs a hook, the working tree's
// root, or the git directory itself in a bare repository; ~ is the
// operator's home, as git expands it. Set to nothing, it is the top of the
// filesystem, where git looks for each hook by its name (hooksPathSetting).
// Unset, it is the hooks directory of the common git directory, which a
// linked worktree shares with its main checkout. warnings are what the
// answer cannot vouch for, and what it has to explain.
func hooksDir(ctx context.Context, req plugin.Request, repo *git.Repository, fs billy.Filesystem) (dir, base string,
	hidden bool, warnings []view.Error, err error,
) {
	base = fs.Root()
	if wt, werr := repo.Worktree(); werr == nil {
		base = wt.Filesystem.Root()
	}
	value, set, hidden, warnings, err := hooksPathSetting(ctx, req, repo)
	switch {
	case err != nil:
		return "", "", false, nil, err
	case !set:
		return filepath.Join(commonGitDir(fs), "hooks"), base, false, warnings, nil
	case value == "":
		return filesystemTop, base, hidden, warnings, nil
	}
	return against(base, plugin.ExpandHome(value)), base, hidden, warnings, nil
}

// filesystemTop is the directory a core.hooksPath set to nothing leaves git
// looking in: git joins the value and a hook's name with a slash, and the
// empty value makes /pre-commit of it.
var filesystemTop = string(filepath.Separator)

// hooksPathSetting is core.hooksPath as git resolves it: the value in the
// last of the files git reads that sets it (machineConfigSources, then the
// repository's own, then its worktree's), or in the environment, which git
// reads after them all (commandConfig); set is false where none does. And a
// warning for each way the answer may not be the one the git that runs the
// hooks reaches, or needs saying.
//
// **Set to nothing is set, and set with no value is an error.** `hooksPath
// =` makes git look for each hook at /<name>: `git rev-parse --git-path
// hooks/pre-commit` prints /pre-commit, and git runs one there. `hooksPath`
// alone, or `git -c core.hooksPath` with no =, is a value git refuses ("missing
// value for 'core.hookspath'"), and stops before it runs anything: no hook,
// and no command. This skipped both, and listed the hooks of whatever an
// earlier file named, or the repository's own, as the ones that run. A value
// set to nothing is listed, and said; one set to none is refused, as git
// refuses it. git reads the last setting alone, so one that a later file
// sets again is no error.
//
// A file one of them includes is read where git reads it, in its place
// (configReading.expand); one whose include this cannot decide is not, and
// could set it: the ones that count are at or after the piece the value came
// from, since what git reads later wins, which is all of them when none sets
// it. hidden is a value read from a file the caller is not shown. And two system
// files setting it differently are two builds of git running hooks from two
// directories, only one of which this lists: that is said, naming the files
// and not the values, since over MCP the operator's own config is read for
// this one key and a directory outside the root is not shown.
func hooksPathSetting(ctx context.Context, req plugin.Request, repo *git.Repository) (value string, set, hidden bool,
	_ []view.Error, _ error,
) {
	files, err := gitConfigs(ctx, req, repo)
	if err != nil {
		return "", false, false, nil, err
	}
	// Each system file with the value it leaves core.hooksPath at, what it
	// includes read with it: a build of git reads its own system file and the
	// files that one includes, and none of the others.
	from, system := -1, ""
	var systems []string
	builds := map[string]string{}
	for i, f := range files {
		if f.scope == "system" && !f.included {
			system = f.path
		}
		core := f.config.Raw.Section("core")
		if !core.HasOption("hooksPath") {
			continue
		}
		from = i
		if f.scope == "system" {
			if _, seen := builds[system]; !seen {
				systems = append(systems, system)
			}
			builds[system] = core.Option("hooksPath")
		}
	}
	values := map[string]bool{}
	for _, v := range builds {
		values[v] = true
	}
	var warnings []view.Error
	if w := includesUndecided("git.hooks.include",
		"core.hooksPath was not looked for there, and git may run hooks from another directory",
		files[max(from, 0):]); w != nil {
		warnings = append(warnings, *w)
	}
	if from < 0 {
		return "", false, false, warnings, nil
	}
	last := files[from]
	if last.blank[configKey("core", "", "hooksPath")].last {
		return "", true, false, nil, fmt.Errorf("%s sets core.hooksPath with no value, which git refuses to run with "+
			"(missing value): no hook runs, and no git command either", last.place())
	}
	if last.scope == "system" && len(values) > 1 {
		warnings = append(warnings, view.Error{
			Code: "git.hooks.system",
			Message: fmt.Sprintf("core.hooksPath is set differently in %s, which different builds of git "+
				"read as their system config: this lists the directory %s names",
				strings.Join(systems, ", "), last.path),
			Hint: "`git config --show-origin core.hooksPath` names the one the git on your PATH reads",
		})
	}
	value = last.config.Raw.Section("core").Option("hooksPath")
	if value == "" {
		warnings = append(warnings, view.Error{
			Code: "git.hooks.empty",
			Message: fmt.Sprintf("%s sets core.hooksPath to nothing, so git looks for each hook at the top of "+
				"the filesystem, /pre-commit and the rest: this lists that directory", last.place()),
			Hint: "unset it there, and git runs the repository's own hooks",
		})
	}
	return value, true, last.hidden, warnings, nil
}

// gitConfigs is every piece of config git reads for repo, for req, in the
// order it reads them, a later one's value winning: the operator's own
// (machineConfigs), the repository's, its worktree's, and the environment's
// (commandConfig), each followed into what it includes, as git follows it
// (configReading).
func gitConfigs(ctx context.Context, req plugin.Request, repo *git.Repository) ([]scopedConfig, error) {
	_, repository, err := repositoryConfigs(repo)
	if err != nil {
		return nil, err
	}
	r := newConfigReading(ctx, req, repo)
	r.everything = everyConfig(repository)
	sources, err := r.everything()
	if err != nil {
		return nil, err
	}
	return r.follow(sources)
}

// shownFrom is p as a row shows it: from base when it is inside, which the
// repository's own hooks directory and a hooksPath in the checkout are, and
// in full when it is not.
func shownFrom(base, p string) string {
	if rel, err := filepath.Rel(base, p); err == nil && !climbsOut(rel) {
		return filepath.ToSlash(rel)
	}
	return p
}

// commonGitDir is the directory a git directory keeps its objects, refs,
// config and hooks in: the one its commondir file names, and itself when it
// has none.
func commonGitDir(fs billy.Filesystem) string {
	if named := commonDir(fs); named != "" {
		return against(fs.Root(), named)
	}
	return fs.Root()
}

// hooksFilesystem returns the billy.Filesystem hooks live under — split out
// from runHooks so a memory-backed clone's refusal can be tested directly
// against a real *git.Repository (built via cloneRepo) without needing a
// reachable remote host to drive it through openRepo's own routing.
//
// Hooks are files on disk, exec'd by the git binary itself — go-git never
// consults them, and a remote path here was never written to disk at all
// (cloneRepo keeps everything in memory), so there is nothing to list.
func hooksFilesystem(repo *git.Repository) (billy.Filesystem, *view.Error) {
	fss, ok := repo.Storer.(*filesystem.Storage)
	if !ok {
		return nil, view.Errorf("git.hooks.unavailable", "no hooks directory for this repository").
			WithHint("hooks live on disk — a remote URL is cloned entirely in memory and never gets one")
	}
	return fss.Filesystem(), nil
}
