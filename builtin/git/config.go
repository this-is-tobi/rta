package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-git/gcfg"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func configCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.config",
		Summary:      "Effective config across system, global and local scope",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "Every key set in system, global, local or worktree config, one row per file it's " +
			"set in — the files `git config --list --show-origin` reads, both global ones " +
			"included, before any of them override each other: worktree wins over local, local wins " +
			"over global, global wins over system, and the command scope, which GIT_CONFIG_COUNT and " +
			"GIT_CONFIG_PARAMETERS set as `git -c` sets it, wins over them all. The worktree scope " +
			"is config.worktree, which git reads where extensions.worktreeConfig is set, as " +
			"`git sparse-checkout` sets it. The " +
			"system scope is every system file git's usual builds read " +
			"(/etc/gitconfig, Homebrew's, Apple's developer tools'), and GIT_CONFIG_SYSTEM, " +
			"GIT_CONFIG_GLOBAL and GIT_CONFIG_NOSYSTEM are honoured as git honours them. A key " +
			"missing from a scope simply has no row there rather than one with an empty value. " +
			"Each row names the file it comes from. An include is followed as git follows it, " +
			"include.path and an includeIf whose gitdir:, gitdir/i:, onbranch: or " +
			"hasconfig:remote.*.url: condition holds, its keys read in the including file's scope " +
			"at the place of the include; one this cannot decide is not read, and a warning says so. " +
			"Over MCP only the repository's own config, local and worktree, is returned: the " +
			"machine-wide scopes and the environment's are the operator's, not the repository's, " +
			"and a file the repository's config includes from outside the server's roots counts for " +
			"git.hooks and the rest but none of its keys is shown, a warning naming the include. " +
			"Values that carry a credential are masked on every surface.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runConfig,
	}
}

func runConfig(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, done, verr := openRepoConfigOnly(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer done()

	t := view.Table{Columns: []view.Column{
		{Name: "Scope"},
		{Name: "Key"},
		{Name: "Value"},
		{Name: "Origin"},
	}}

	// Local scope for either kind of repository this plugin opens:
	// filesystem storage's own .git/config on disk, or the config a remote
	// clone synthesized in memory (its remote and branch tracking, at
	// minimum), which repositoryConfigs reads alike.
	_, repository, err := repositoryConfigs(repo)
	if err != nil {
		return nil, view.Errorf("git.config.failed", "reading repository config: %v", err)
	}
	r := newConfigReading(ctx, req, repo)
	r.everything = everyConfig(repository)

	// **The machine-wide scopes are the operator's, not the repository's.**
	//
	// The package doc says every capability here is Read because "a
	// repository's history and diffs are not credentials", and that is true
	// of the other seven. It was never true of this one: system and global
	// config are not the repository at all, and they are where git keeps
	// credentials by convention — `url.https://oauth2:glpat-…@gitlab.com/
	// .insteadOf` is GitLab's own documented rewrite, `http.<url>.extraHeader`
	// carries an Authorization header for Azure DevOps, and `github.token` is
	// a plain PAT. So a Read capability with no grant handed an MCP caller the
	// operator's forge credentials, from outside the path root, on a default
	// server.
	//
	// Withheld from MCP rather than gated, because a grant is the wrong
	// instrument here: the agent has a real use for the repository's own
	// remotes and branch tracking, and no use at all for the operator's
	// machine-wide identity. Local stays; a person at a terminal, on their own
	// machine, still sees all three — the same rule Field.Local states for
	// inputs, applied to scopes. The environment's is the operator's too, and
	// withheld with them: `git -c http.extraHeader=...` is how a CI system
	// hands git a token for one command.
	//
	// Every file git reads for these scopes, not go-git's LoadConfig, which
	// reads the first global file that exists and stops: with both
	// ~/.config/git/config and ~/.gitconfig present, git reads the two and
	// this showed one, so a key set only in the other was missing from the
	// answer to what git is configured with. A scope with no file on this
	// machine is missing rows, never a failure. Over MCP they are still read
	// where a hasconfig:remote.*.url condition asks for every remote's URL,
	// for the answer to that alone (configReading.collect).
	sources := repository
	if req.Surface() != plugin.SurfaceMCP {
		if sources, err = r.everything(); err != nil {
			return nil, view.Errorf("git.config.failed", "%v", err)
		}
	}
	pieces, err := r.follow(sources)
	if verr := refusedByTheGate(err); verr != nil {
		return nil, verr
	}
	if err != nil {
		return nil, view.Errorf("git.config.failed", "reading the files the config includes: %v", err)
	}
	base := ""
	if store, onDisk := repo.Storer.(*filesystem.Storage); onDisk {
		base = store.Filesystem().Root()
		if wt, werr := repo.Worktree(); werr == nil {
			base = wt.Filesystem.Root()
		}
	}
	// What a file the caller is not shown includes is its content too: an
	// include in one that this cannot decide is not counted either.
	var shown []scopedConfig
	for _, p := range pieces {
		if !p.hidden {
			addConfigRows(&t, p.scope, p.origin(base), p.config)
			shown = append(shown, p)
		}
	}

	t.Total = len(t.Rows)
	if w := includesUndecided("git.config.include", "the keys set there are missing from this table", shown); w != nil {
		t.Warnings = append(t.Warnings, *w)
	}
	if len(r.outside) > 0 {
		t.Warnings = append(t.Warnings, view.Error{
			Code: "git.config.include.outside",
			Message: fmt.Sprintf("the repository's config includes %s outside this server's roots, %s: git reads "+
				"%s, and git.hooks, git.status and the rest count what %s, but none of %s keys is shown",
				format.Plural(len(r.outside), "a file", format.CountOf(len(r.outside), "file")), strings.Join(r.outside, ", "),
				format.Plural(len(r.outside), "it", "them"), format.Plural(len(r.outside), "it sets", "they set"),
				format.Plural(len(r.outside), "its", "their")),
			Hint: "an include can name any file on the machine, and one of sections and keys, a credentials file " +
				"among them, reads as config: `git config --list --show-origin` at a terminal shows it",
		})
	}
	return t, nil
}

// everyConfig is every source of config git reads for a repository whose own
// are repository, in the order git reads them, read once, when first asked:
// the operator's own files, the repository's, and the environment's.
func everyConfig(repository []configSource) func() ([]configSource, error) {
	var (
		all  []configSource
		err  error
		read bool
	)
	return func() ([]configSource, error) {
		if read {
			return all, err
		}
		read = true
		machine, merr := machineConfigs()
		if merr != nil {
			err = fmt.Errorf("reading the machine-wide config: %w", merr)
			return nil, err
		}
		command, cerr := commandConfig()
		if cerr != nil {
			err = fmt.Errorf("reading the config git's environment sets: %w", cerr)
			return nil, err
		}
		all = append(append(machine, repository...), command.orNone()...)
		return all, nil
	}
}

// orNone is s alone, or nothing where there is no s.
func (s *configSource) orNone() []configSource {
	if s == nil {
		return nil
	}
	return []configSource{*s}
}

// The files git's system scope is read from, which depend on how git was
// built and are not written down anywhere this can read without running it.
//
// **/etc/gitconfig is only where a git built for /usr looks.** The system file
// is compiled in as the build's own prefix: Homebrew's git reads
// /opt/homebrew/etc/gitconfig (/usr/local/etc/gitconfig on an Intel Mac, and
// under /home/linuxbrew on Linux), and a git built from source reads
// /usr/local/etc/gitconfig. Apple's git reads /etc/gitconfig and, before it,
// the gitconfig of the developer tools it ships in, which is where macOS sets
// the credential helper. This read /etc/gitconfig alone, as go-git does, so a
// core.hooksPath set for every repository in any of the others was missing
// from git.config and from git.hooks, the capability an audit of what runs
// on a commit relies on.
//
// So every one of them that exists is read, as the system scope. Only one
// build of git reads each, and a machine with two builds has two system
// scopes; git.hooks says so where they disagree (hooksPathSetting). Apple's
// files come first, as Apple's git reads them first. Variables so a test can
// point them away from this machine's.
var (
	vendorGitConfigs = []string{
		"/Library/Developer/CommandLineTools/usr/share/git-core/gitconfig",
		"/Applications/Xcode.app/Contents/Developer/usr/share/git-core/gitconfig",
	}
	systemGitConfigs = []string{
		"/etc/gitconfig",
		"/usr/local/etc/gitconfig",
		"/opt/homebrew/etc/gitconfig",
		"/home/linuxbrew/.linuxbrew/etc/gitconfig",
	}
)

// scopedConfig is a piece of git config, the whole of a file or the part of
// one between the includes git follows in it (configReading.expand): the
// scope git reads it as, which a file an include names takes from the file
// naming it; where it is, as a message names it (place), "" for the
// repository's own files; and each key it sets with no value at all
// (valueless).
type scopedConfig struct {
	scope, path string
	config      *gitconfig.Config
	blank       map[string]valueless
	// file is the file it was read from, as git names it: the one a relative
	// include in it is taken from, and the origin its rows name. "" for the
	// environment's, and for a clone in memory's, which have none.
	file string
	// included is a file git reads because another includes it.
	included bool
	// hidden is, over MCP, a file the repository's own config includes from
	// outside the server's roots, or one such a file includes: it counts for
	// every answer here, and none of its keys or values is shown.
	hidden bool
	// undecided is why the include this piece ends at was not followed, where
	// whether or where git reads it turns on what this cannot tell.
	undecided string
}

// valueless is how a file of config sets a key with no value at all, a name
// alone on its line or `git -c key` with no =: anywhere in it, and in the
// last line that sets it.
//
// **go-git reads such a key as set to nothing, and git does not.** `hooksPath
// =` is a value, the empty one, and `hooksPath` alone is none, which git
// refuses a path key for ("missing value") and stops before running
// anything. go-git's reader hands both on as "", so git.hooks read the second
// as the first, and either as unset. Each file is read again for it
// (valuelessKeys), by the key as configKey spells it.
type valueless struct{ any, last bool }

// configKey is a key as git compares it: its section and name in lower case,
// and its subsection as written.
func configKey(section, subsection, name string) string {
	if subsection == "" {
		return strings.ToLower(section) + "." + strings.ToLower(name)
	}
	return strings.ToLower(section) + "." + subsection + "." + strings.ToLower(name)
}

// valuelessKeys is each key content, a file of git config, sets with no
// value at all. It is read with gcfg, the reader go-git reads config with,
// whose callback tells a name alone apart from one set to nothing, which
// go-git passes over; a file gcfg refuses is one go-git refused already.
func valuelessKeys(content []byte) map[string]valueless {
	keys := map[string]valueless{}
	_ = gcfg.ReadWithCallback(bytes.NewReader(content), func(section, subsection, name, _ string, blank bool) error {
		if name != "" {
			key := configKey(section, subsection, name)
			keys[key] = valueless{any: keys[key].any || blank, last: blank}
		}
		return nil
	})
	return keys
}

// valuelessIn is where the first of files that sets section.name with no
// value anywhere in it is (place), "" where none does.
func valuelessIn(files []scopedConfig, section, name string) string {
	for _, f := range files {
		if f.blank[configKey(section, "", name)].any {
			return f.place()
		}
	}
	return ""
}

// place is where f is, as a warning or a refusal names it.
func (f scopedConfig) place() string {
	switch {
	case f.path != "":
		return f.path
	case f.scope == "local":
		return "the repository's config"
	case f.scope == "worktree":
		return "config.worktree"
	case f.scope == "command":
		return "the config in git's environment"
	}
	return "the " + f.scope + " config"
}

// machineConfigSources is every file of the operator's own git config git
// could read, in the order it reads them: the system scope's, then the global
// scope's two, $XDG_CONFIG_HOME/git/config (~/.config/git/config when that is
// unset) and ~/.gitconfig. A later file's value wins over an earlier one's,
// as it does in git.
//
// The environment is honoured as git honours it, since git runs a hook with
// the same one: GIT_CONFIG_NOSYSTEM set true reads no system file, Apple's
// included; GIT_CONFIG_SYSTEM names the system file in place of the build's
// own, and Apple's are still read beside it, as Apple's git reads them; and
// GIT_CONFIG_GLOBAL names the one global file in place of both. Either set
// to nothing reads no file for that scope. DEVELOPER_DIR, which chooses the
// developer tools Apple's git runs from, adds theirs.
func machineConfigSources() []scopedConfig {
	var out []scopedConfig
	seen := map[string]bool{}
	add := func(scope, path string) {
		if path == "" || seen[filepath.Clean(path)] {
			return
		}
		seen[filepath.Clean(path)] = true
		out = append(out, scopedConfig{scope: scope, path: path})
	}
	if !envBool("GIT_CONFIG_NOSYSTEM") {
		for _, p := range vendorGitConfigs {
			add("system", p)
		}
		if dev := os.Getenv("DEVELOPER_DIR"); dev != "" {
			add("system", filepath.Join(dev, "usr", "share", "git-core", "gitconfig"))
		}
		if p, set := os.LookupEnv("GIT_CONFIG_SYSTEM"); set {
			add("system", p)
		} else {
			for _, p := range systemGitConfigs {
				add("system", p)
			}
		}
	}
	if p, set := os.LookupEnv("GIT_CONFIG_GLOBAL"); set {
		add("global", p)
	} else if home, err := os.UserHomeDir(); err == nil {
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		add("global", filepath.Join(xdg, "git", "config"))
		add("global", filepath.Join(home, ".gitconfig"))
	}
	return out
}

// envBool is an environment variable read as git reads a boolean one
// (gitBool), false when it is unset.
func envBool(name string) bool { return gitBool(os.Getenv(name)) }

// gitBool is a value read as git reads a boolean: true, yes, on or a number
// other than zero; false when empty, and for anything else, which git
// refuses to run with at all.
func gitBool(value string) bool {
	switch v := strings.ToLower(strings.TrimSpace(value)); v {
	case "true", "yes", "on":
		return true
	default:
		n, err := strconv.Atoi(v)
		return err == nil && n != 0
	}
}

// worktreeConfig is config.worktree, the config of the working tree being
// read, which git reads after the repository's own where that sets
// extensions.worktreeConfig — `git sparse-checkout` sets it, and `git config
// --worktree` writes there — and nil where it does not, or there is none. It
// is the repository's, as its config is: read through the filesystem that
// bounds a git directory's files (regularFiles), from the working tree's own
// git directory, which for a linked worktree is not the common one.
//
// Only where the config sets a format version: git passes over every
// extension a repository without one sets (unsetVersion), this one included,
// and a core.hooksPath in a config.worktree git does not read was listed as
// the directory it runs hooks from. own is the whole of the repository's own
// config, which git decides that from, as it decides the format, following
// no include there.
func worktreeConfig(store *filesystem.Storage, own *gitconfig.Config) (*configSource, error) {
	if !extensionsInEffect(own) || !own.Raw.HasSection("extensions") ||
		!gitBool(own.Raw.Section("extensions").Option("worktreeConfig")) {
		return nil, nil
	}
	content, err := readGitDirFile(store.Filesystem(), "config.worktree")
	if errors.Is(err, iofs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines, err := fileLines(content)
	if err != nil {
		return nil, err
	}
	return &configSource{from: scopedConfig{scope: "worktree", file: filepath.Join(store.Filesystem().Root(),
		"config.worktree")}, lines: lines}, nil
}

// repositoryConfigs is the repository's own config, and its working tree's
// where git reads that (worktreeConfig), read into their lines; own is the
// whole of the first as go-git reads it, which the repository's format is
// decided from. A clone in memory has no file of config: its own is what
// go-git made for it.
func repositoryConfigs(repo *git.Repository) (own *gitconfig.Config, _ []configSource, _ error) {
	own, err := repo.Config()
	if err != nil {
		return nil, nil, err
	}
	store, onDisk := repo.Storer.(*filesystem.Storage)
	if !onDisk {
		return own, []configSource{{from: scopedConfig{scope: "local"}, lines: rawLines(own)}}, nil
	}
	var lines []configLine
	content, err := readGitDirFile(store.Filesystem(), "config")
	if err == nil {
		lines, err = fileLines(content)
	}
	if err != nil && !errors.Is(err, iofs.ErrNotExist) {
		return nil, nil, err
	}
	sources := []configSource{{from: scopedConfig{scope: "local",
		file: filepath.Join(commonGitDir(store.Filesystem()), "config")}, lines: lines}}
	perWorktree, err := worktreeConfig(store, own)
	if err != nil {
		return nil, nil, fmt.Errorf("config.worktree: %w", err)
	}
	if perWorktree != nil {
		sources = append(sources, *perWorktree)
	}
	return own, sources, nil
}

// readGitDirFile is the whole of name in a git directory, read through fs,
// which bounds it (regularFiles).
func readGitDirFile(fs billy.Filesystem, name string) ([]byte, error) {
	f, err := fs.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// machineConfigs is the operator's own git config, every file of it that
// exists (machineConfigSources), read into its lines.
func machineConfigs() ([]configSource, error) {
	var out []configSource
	for _, s := range machineConfigSources() {
		content, err := readConfigFile(s.path)
		// Passed over where git passes over one: not there, under something
		// that is not a directory, or not this user's to read. Most of the
		// system files are another build's, and a Linuxbrew prefix this user
		// cannot enter is no reason to fail the call; a git run by this user
		// cannot read one either.
		if errors.Is(err, iofs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, iofs.ErrPermission) {
			continue
		}
		var lines []configLine
		if err == nil {
			lines, err = fileLines(content)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.path, err)
		}
		s.file = s.path
		out = append(out, configSource{from: s, lines: lines})
	}
	return out, nil
}

// readConfigFile is the whole of path, a file of config git reads that is
// not the repository's own.
//
// Opened without waiting, and read only when it is a file. git.hooks reads
// these over MCP too, and a named pipe in place of one — which unpacking an
// archive into a root that holds the home directory can leave, and which an
// include can name anywhere — blocked open(2) until a writer came, which no
// context can interrupt, as the repository's own files did before openAt.
//
// And held to the bound a repository's own config is held to, since it is
// read into memory whole: a sparse file of gigabytes costs its writer no disk.
//
// The null device aside, which reads as a file with nothing in it:
// GIT_CONFIG_GLOBAL=/dev/null is how a CI job or a test runs git with none of
// the machine's config, and git reads it so. Refused as not a file, it failed
// git.hooks and git.config, and left git.status without the excludes file.
//
// An error is the reason alone, without the file: the caller names it as a
// message about it may, which for a file the caller is not shown is not by
// where it is (configReading.include).
func readConfigFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		var opening *iofs.PathError
		if errors.As(err, &opening) {
			return nil, opening.Err
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	switch {
	case err != nil:
		return nil, err
	case isNullDevice(info):
		return nil, nil
	case !info.Mode().IsRegular():
		return nil, errors.New("not a regular file")
	case info.Size() > maxConfigBytes:
		return nil, tooLarge(maxConfigBytes)
	}
	return io.ReadAll(io.LimitReader(f, info.Size()))
}

// isNullDevice reports whether info is the null device's, os.DevNull.
func isNullDevice(info os.FileInfo) bool {
	null, err := os.Stat(os.DevNull)
	return err == nil && os.SameFile(info, null)
}

// commandConfig is the config git's environment sets for one command, the
// scope git calls command and reads after every file, so that it wins over
// all of them: GIT_CONFIG_COUNT pairs of GIT_CONFIG_KEY_<n> and
// GIT_CONFIG_VALUE_<n>, then GIT_CONFIG_PARAMETERS, which `git -c` and `git
// --config-env` hand every command git runs, a hook or an alias among them.
// nil where neither is set. Read into lines as a file is, so that an
// include set there is followed where it is set (configReading.expand).
//
// **Read as git reads it, since git runs a hook with the same environment.**
// A core.hooksPath set there is where git runs hooks from, and git.hooks,
// reading the files alone, listed .git/hooks while git ran another directory's
// pre-commit. And what git refuses to run with — a count that is not one, a
// key or a value missing, a key with no section — fails here too: git runs
// no hook at all with it, and an answer naming a directory would be wrong.
func commandConfig() (*configSource, error) {
	count, counted := os.LookupEnv("GIT_CONFIG_COUNT")
	parameters, given := os.LookupEnv("GIT_CONFIG_PARAMETERS")
	if !counted && !given {
		return nil, nil
	}
	cfg := &configSource{from: scopedConfig{scope: "command"}}
	if counted {
		// Read as git reads it, with C's strtoul: white space before it and a
		// sign are taken, and anything after it is not. `GIT_CONFIG_COUNT=" 1"`
		// is one pair to git, and failed the call here.
		n := 0
		if count != "" {
			var err error
			if n, err = strconv.Atoi(strings.TrimLeft(count, cSpace)); err != nil || n < 0 {
				return nil, fmt.Errorf("GIT_CONFIG_COUNT is %q, not a count", count)
			}
		}
		for i := range n {
			key, found := os.LookupEnv("GIT_CONFIG_KEY_" + strconv.Itoa(i))
			if !found {
				return nil, fmt.Errorf("GIT_CONFIG_COUNT is %d, and GIT_CONFIG_KEY_%d is not set", n, i)
			}
			value, found := os.LookupEnv("GIT_CONFIG_VALUE_" + strconv.Itoa(i))
			if !found {
				return nil, fmt.Errorf("GIT_CONFIG_COUNT is %d, and GIT_CONFIG_VALUE_%d is not set", n, i)
			}
			if err := addCommandKey(cfg, key, value, false); err != nil {
				return nil, fmt.Errorf("GIT_CONFIG_KEY_%d: %w", i, err)
			}
		}
	}
	if given {
		if err := parseConfigParameters(cfg, parameters); err != nil {
			return nil, fmt.Errorf("GIT_CONFIG_PARAMETERS: %w", err)
		}
	}
	return cfg, nil
}

// addCommandKey adds key, spelled section.name or section.subsection.name as
// git spells one on its command line, set to value, or with no value at all
// where none is set, as `git -c key` with no = sets one.
//
// A key git's git_config_parse_key refuses is refused: a section or a name
// holding anything but letters, digits and dashes, a name that does not start
// with a letter, a subsection holding a line break. git refuses to run at all
// with one in its environment ("invalid key"), so no hook runs, and
// git.hooks naming the directory a key like `core.hooks path` sets would be
// an answer about a command that never happens. And what it reads is read: a
// key with no section is one with no dot but its first, or none at all, and
// `.sub.name`, an empty section before a subsection, is one git runs with.
//
// A refusal names the key with a URL's credentials masked, as git.config's
// rows show it: `url.https://oauth2:<token>@host/.insteadOf` carries the
// token in the key, and the refusal reaches a terminal, and an MCP caller
// through git.hooks, where the command scope's rows never do.
func addCommandKey(cfg *configSource, key, value string, none bool) error {
	first, last := strings.IndexByte(key, '.'), strings.LastIndexByte(key, '.')
	switch {
	case last <= 0:
		return fmt.Errorf("%q has no section", maskURLCredentials(key))
	case last == len(key)-1:
		return fmt.Errorf("%q has no name after its section", maskURLCredentials(key))
	case first > 0 && !keyWord(key[:first]) || !keyWord(key[last+1:]) || !isLetter(key[last+1]) ||
		strings.IndexByte(key[first:last], '\n') >= 0:
		return fmt.Errorf("%q is not a key git reads", maskURLCredentials(key))
	}
	line := configLine{section: key[:first], name: key[last+1:], value: value, none: none}
	if first != last {
		line.subsection, line.emptySubsection = key[first+1:last], first+1 == last
	}
	cfg.lines = append(cfg.lines, line)
	return nil
}

// errConfigParameters is GIT_CONFIG_PARAMETERS in a shape git does not write
// and refuses to read.
var errConfigParameters = errors.New("not in the shape git writes it")

// parseConfigParameters adds the pairs of GIT_CONFIG_PARAMETERS to cfg, read
// as git's parse_config_env_list reads them: each a single-quoted word
// (sqDequote), 'key'='value' as git writes it now, 'key'= for a key with no
// value, and 'key=value' or 'key' as older git wrote it, separated by space.
// 'key'= and 'key' set no value at all, and 'key=', or 'key'= and an empty
// quoted word, the empty one, as `git -c key` and `git -c key=` set them.
func parseConfigParameters(cfg *configSource, env string) error {
	for rest := env; rest != ""; rest = strings.TrimLeft(rest, gitSpace) {
		key, after, ok := sqDequote(rest)
		if !ok {
			return errConfigParameters
		}
		value, none := "", false
		switch {
		case after == "" || isSpace(after[0]):
			// The older 'key=value', split at its first =, which git trims.
			var set bool
			key, value, set = strings.Cut(key, "=")
			key, none = strings.Trim(key, gitSpace), !set
		case after[0] == '=' && (len(after) == 1 || isSpace(after[1])):
			after, none = after[1:], true
		case after[0] == '=' && after[1] == '\'':
			if value, after, ok = sqDequote(after[1:]); !ok || after != "" && !isSpace(after[0]) {
				return errConfigParameters
			}
		default:
			return errConfigParameters
		}
		if err := addCommandKey(cfg, key, value, none); err != nil {
			return err
		}
		rest = after
	}
	return nil
}

// sqDequote is the single-quoted word s starts with, and what follows it, as
// git's sq_dequote_step reads one: the text between the quotes, where a quote
// or a ! outside them, backslashed and followed by a quote that opens them
// again, is part of the word. ok is false where s starts with no quote or
// never closes it.
func sqDequote(s string) (word, rest string, ok bool) {
	if s == "" || s[0] != '\'' {
		return "", "", false
	}
	var b strings.Builder
	for i := 1; i < len(s); {
		c := s[i]
		i++
		if c != '\'' {
			b.WriteByte(c)
			continue
		}
		if i+2 < len(s) && s[i] == '\\' && (s[i+1] == '\'' || s[i+1] == '!') && s[i+2] == '\'' {
			b.WriteByte(s[i+1])
			i += 3
			continue
		}
		return b.String(), s[i:], true
	}
	return "", "", false
}

// cSpace is the bytes C's isspace calls space, which strtoul skips.
const cSpace = " \t\n\v\f\r"

// gitSpace is the bytes git's own isspace calls space: what it splits the
// words of GIT_CONFIG_PARAMETERS on, and trims from the key of an older
// 'key=value'. git-compat-util.h puts its own ctype in place of C's, and a
// vertical tab or a form feed is not space to it: a word run on by one, or a
// key led by one, is a command git refuses to run ("bogus format", "invalid
// key"), and was read here, as was a key led by a no-break space, which Go's
// TrimSpace took off.
const gitSpace = " \t\n\r"

// isSpace is a byte git's isspace calls space.
func isSpace(c byte) bool { return strings.IndexByte(gitSpace, c) >= 0 }

// keyWord reports whether s is a section or a name git takes in a key: one or
// more letters, digits and dashes, git's iskeychar.
func keyWord(s string) bool {
	for i := range len(s) {
		if c := s[i]; c != '-' && !isLetter(c) && (c < '0' || c > '9') {
			return false
		}
	}
	return s != ""
}

// isLetter is an ASCII letter, as git's own isalpha takes one.
func isLetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

// includesUndecided is the warning, as code, that pieces end at includes this
// did not follow, since whether or where git reads them turns on what this
// cannot tell (scopedConfig.undecided), and why; nil where none does.
// missing says what the answer lacks for it.
//
// **Said rather than guessed at.** An include naming git's install prefix
// names a directory only the git that reads it knows, and an onbranch:
// condition in a repository whose refs are kept in a reftable turns on a
// branch this does not read: following either as though it were known is how
// an audit reads the wrong file, so each is said to be unread, and the answer
// is never quietly missing what it names.
func includesUndecided(code, missing string, pieces []scopedConfig) *view.Error {
	n, why := 0, []string{}
	for _, p := range pieces {
		if p.undecided == "" {
			continue
		}
		if n++; !slices.Contains(why, p.undecided) {
			why = append(why, p.undecided)
		}
	}
	if n == 0 {
		return nil
	}
	return &view.Error{
		Code: code,
		Message: fmt.Sprintf("%s the config includes %s not read, since whether or where git reads %s turns on "+
			"what this cannot tell (%s), so %s", format.CountOf(n, "file"), format.Plural(n, "is", "are"),
			format.Plural(n, "it", "them"), strings.Join(why, "; "), missing),
		Hint: "`git config --list --show-origin` follows includes, and names the file each key comes from",
	}
}

// addConfigRows adds a row to t for each key cfg sets, in scope, read from
// origin.
func addConfigRows(t *view.Table, scope, origin string, cfg *gitconfig.Config) {
	for _, s := range cfg.Raw.Sections {
		for _, o := range s.Options {
			key := s.Name + "." + o.Key
			t.Rows = append(t.Rows, []string{scope, key, maskConfigValue(key, o.Value), origin})
		}
		for _, sub := range s.Subsections {
			for _, o := range sub.Options {
				// The subsection is part of the key and is itself a place a
				// credential hides: `url.https://tok@host/.insteadOf` carries
				// it in the *name*, not the value.
				key := s.Name + "." + sub.Name + "." + o.Key
				t.Rows = append(t.Rows, []string{scope, maskURLCredentials(key), maskConfigValue(key, o.Value), origin})
			}
		}
	}
}

// origin is the file f was read from as a row names it: from base, the
// repository's working tree, where it is inside, as git.hooks shows a path,
// its links resolved where they lead there, and in full, as it is named,
// where it is not.
func (f scopedConfig) origin(base string) string {
	switch {
	case f.file != "":
		if shown := shownFrom(base, f.file); !filepath.IsAbs(shown) {
			return shown
		}
		if resolved, err := filepath.EvalSymlinks(f.file); err == nil {
			if shown := shownFrom(base, resolved); !filepath.IsAbs(shown) {
				return shown
			}
		}
		return f.file
	case f.scope == "command":
		return "environment"
	}
	return "memory"
}

// secretName matches the name of a config key, its dashes taken out, whose
// value is a credential by convention.
//
// A name test, and therefore a heuristic — which is why it is the *second*
// line here rather than the only one. maskURLCredentials below is the
// syntactically certain half, the kind net.info's masking relies on, and it
// catches the shape that actually appears in the wild.
//
// **Anywhere in the name, not the name alone.** github.token and
// client.password were masked, and client.access-token, default.secret-key and
// git send-email's own sendemail.smtpPass were not: a file of config names a
// credential among other words, and a repository's config can include any
// file of sections and keys on the machine. A key whose name only mentions
// one, core.askPass naming the program that asks, is left alone; one that is
// a switch, http.sslCertPasswordProtected, is masked for a name that is
// nearly always a secret's.
//
// **And a name ending in pass, which is how git's own keys spell a
// password**: imap.pass, git imap-send's, gitcvs.dbPass and
// sendemail.smtpPass each hold one, and the first two were shown in the clear
// where the name was read for the longer words alone. askPass is the one git
// key ending so that names a program rather than a secret (secretKey).
var secretName = regexp.MustCompile(`(?i)token|secret|passw(or)?d|apikey|accesskey|privatekey|bearer|extraheader`)

// secretKey reports whether key, as git.config spells it, names a credential
// (secretName, or a name ending in pass), by the name after its last dot.
func secretKey(key string) bool {
	name := strings.ToLower(strings.ReplaceAll(key[strings.LastIndexByte(key, '.')+1:], "-", ""))
	return secretName.MatchString(name) || strings.HasSuffix(name, "pass") && !strings.HasSuffix(name, "askpass")
}

// maskConfigValue hides a value that carries a credential.
//
// Applied on every surface, not only MCP, for net.info's reason: a person
// asking "what is my git config" did not ask for their PAT in a terminal
// transcript, a tmux scrollback, or `-o json` piped somewhere. The value is
// still on disk in a file they own, one `git config` away.
func maskConfigValue(key, value string) string {
	if value == "" {
		return value
	}
	if secretKey(key) {
		return view.Mask
	}
	return maskURLCredentials(value)
}

// maskURLCredentials replaces the password in any URL carrying userinfo.
//
// **This is the syntactically certain half.** `scheme://user:secret@host` has
// exactly one reading, so masking it needs no guess about what a key means —
// and it is the shape git's own documented credential patterns take, in
// `url.<base>.insteadOf` (where it lives in the key) and in remote URLs
// (where it lives in the value).
//
// The username survives: it is identity rather than secret, it is what makes
// the row worth reading at all, and GitLab's own pattern puts the constant
// "oauth2" there.
func maskURLCredentials(s string) string {
	out := s
	for _, m := range urlUserinfo.FindAllStringSubmatch(s, -1) {
		out = strings.Replace(out, m[0], m[1]+m[2]+":"+view.Mask+"@", 1)
	}
	// A second, independent pass for the colon-free spelling: GitHub's own
	// documented form for a PAT in a remote URL is `https://<token>@host/...`
	// — no colon, so urlUserinfo's pattern never matches it, and the token
	// reached an MCP agent in the clear. There is no way to tell that spelling
	// apart from an ordinary bare username from the string alone, so — the
	// same trade maskProxy already takes for a proxy URL — the whole userinfo
	// is masked rather than guessed at: `https://alice@github.com/...` shows
	// a masked username too. Runs after urlUserinfo on purpose, over its
	// output: anything that pass already replaced now carries the colon this
	// pattern requires to be absent, so it is not masked a second time.
	for _, m := range urlBareUserinfo.FindAllStringSubmatch(out, -1) {
		out = strings.Replace(out, m[0], m[1]+view.Mask+"@", 1)
	}
	return out
}

// urlUserinfo matches scheme://user:password@ — the password group is what
// gets replaced. Deliberately requires the colon: `ssh://git@host` names a
// user and no secret, and masking it would hide something that is not one.
var urlUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/:@\s]+):[^@/\s]+@`)

// urlBareUserinfo matches http(s)://user@ with no colon at all — GitHub's
// PAT-as-username spelling, and an ordinary bare username the same shape.
// Restricted to http(s), unlike urlUserinfo above: ssh://git@host names the
// conventional SSH user and no secret, and the SCP-like git@host:path form
// has no "://" for this pattern to anchor on in the first place — both are
// left alone, same as urlUserinfo already leaves them.
var urlBareUserinfo = regexp.MustCompile(`(https?://)([^/:@\s]+)@`)
