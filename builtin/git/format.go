package git

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// reads is what a capability reads of a repository, which decides the
// formats it can answer in (repositoryFormat): its config and its hooks
// directory alone, as git.config and git.hooks read them; those and its refs,
// as git.remotes counts them; or its objects too, as every other capability
// here reads them.
type reads int

const (
	readsConfig reads = iota
	readsRefs
	readsObjects
)

// extension is one of the extensions git 2.50 reads (setup.c): whether only
// format version 1 has it (handle_extension) or a repository of either may
// set it (handle_extension_v0), whether it names something and so has to be
// set to a value (config_error_nonbool, "missing value"), and which values
// git takes, nil for any.
type extension struct {
	versionOne, valued bool
	takes              func(value string) bool
}

// knownExtensions is every extension git 2.50 reads, by its name lowercased,
// as git compares it. git refuses a repository of version 1 that sets any
// other, and passes over any other in one of version 0, which predates
// extensions.
var knownExtensions = map[string]extension{
	"noop":               {},
	"preciousobjects":    {takes: isGitBool},
	"partialclone":       {valued: true},
	"worktreeconfig":     {takes: isGitBool},
	"noop-v1":            {versionOne: true},
	"objectformat":       {versionOne: true, valued: true, takes: oneOf("sha1", "sha256")},
	"compatobjectformat": {versionOne: true, valued: true, takes: oneOf("sha1", "sha256")},
	"refstorage":         {versionOne: true, valued: true, takes: oneOf("files", "reftable")},
	"relativeworktrees":  {versionOne: true, takes: isGitBool},
}

// oneOf is a check that a value is one of values, spelled exactly: git takes
// sha256 and refuses SHA256.
func oneOf(values ...string) func(string) bool {
	return func(value string) bool { return slices.Contains(values, value) }
}

// isGitBool reports whether git reads value as a boolean at all (gitBool
// reads it): true, yes, on, false, no, off in any case, nothing, or a number,
// which git reads with a k, m or g after it too. Anything else stops git
// before it runs.
func isGitBool(value string) bool {
	switch strings.ToLower(value) {
	case "", "true", "yes", "on", "false", "no", "off":
		return true
	}
	if n := len(value); n > 1 && strings.ContainsRune("kKmMgG", rune(value[n-1])) {
		value = value[:n-1]
	}
	_, err := strconv.Atoi(value)
	return err == nil
}

// repositoryFormat is the refusal of a repository, which path names, whose
// format git itself refuses, or whose format this reader cannot read what
// reads needs of; nil for one it can. cfg is the repository's config.
//
// **go-git decides on a repository's format for every capability, and not as
// git does.** It refused any extension but its two no-ops, so a sha256 or a
// reftable repository was refused to git.config and git.hooks, whose answers
// are the config and the hooks directory and have nothing to do with how the
// objects or the refs are stored: a hooks audit could not be run there at
// all. It refused `extensions.objectFormat = sha1`, preciousObjects,
// relativeWorktrees (`git worktree add --relative-paths`) and a version 0
// repository's unknown extension, each of which git reads as the SHA-1
// repository of files it is. And a version 0 repository setting an extension
// only version 1 has is one git 2.50 refuses ("repo version is 0, but v1-only
// extension found"), which go-git's refusal answered with a hint that git
// reads it.
//
// So the format is decided here, as git decides it, and then by what the
// capability reads: an object format but SHA-1 is one the objects are
// unreadable in, refs kept in a reftable are unreadable as files, and neither
// changes the config or the hooks. What is left is handed to go-git with no
// extension at all (decidedFormat). An extension set on more than one line
// is judged by its last, the one git reads: every line was judged, and a
// config naming sha256 and then sha1 was refused as objects this cannot read,
// in a repository git reads as the SHA-1 one it is.
//
// git reads each extension's value as it reads the config, before it looks at
// the version, so a value it does not take stops it whatever the version,
// one it would then pass over included: `extensions.refStorage = bogus` is
// "bad config line" in a repository that sets no version at all. So does no
// value at all where the extension names something: `partialClone` alone on
// its line is "missing value", where go-git reads it as set to nothing and
// this opened a repository git refuses. blank is each key the config sets
// with no value (valuelessKeys).
//
// **A second object format that is the first is a repository git cannot
// open.** extensions.compatObjectFormat names the format git keeps a map to,
// beside the one the objects are named in: extensions.objectFormat, the last
// line of it as git reads it, or SHA-1 where none is set. Set to that same
// format, git 2.50 aborts as it sets the repository up ("BUG: hash_algo and
// compat_hash_algo match"), whatever command it was running, and this opened
// the repository and answered for it. It is refused as git refuses it, once
// the version has been judged, since git judges that first. And git takes
// the extension once only: a second line of it, to any value, the same one
// included, stops git as it reads the config ("already specified"), whatever
// the version, where the last line was read here as the one in force.
func repositoryFormat(path string, cfg *gitconfig.Config, blank map[string]valueless, what reads) *view.Error {
	version, written, err := formatVersion(cfg)
	if err != nil {
		return gitRefuses(path, fmt.Sprintf("its config sets core.repositoryformatversion = %s, which is not a "+
			"number", written), "git refuses to read a format version it cannot count")
	}
	var versionOne, invalid, unknown, unread []string
	objects, compat, compats := "sha1", "", 0
	options := cfg.Raw.Section("extensions").Options
	last := map[string]int{}
	for i, o := range options {
		last[strings.ToLower(o.Key)] = i
	}
	for i, o := range options {
		name, value := strings.ToLower(o.Key), strings.TrimSpace(o.Value)
		switch name {
		case "objectformat":
			objects = value
		case "compatobjectformat":
			compat, compats = value, compats+1
		}
		spelled := "extensions." + o.Key + " = " + o.Value
		none := o.Value == "" && blank[configKey("extensions", "", o.Key)].any
		if none {
			spelled = "extensions." + o.Key + " with no value"
		}
		ext, known := knownExtensions[name]
		switch {
		case !known:
			unknown = append(unknown, spelled)
		case none && ext.valued, ext.takes != nil && !ext.takes(value):
			invalid = append(invalid, spelled)
		case !ext.versionOne:
		case last[name] == i && !readable(name, value, what):
			versionOne = append(versionOne, spelled)
			unread = append(unread, spelled)
		default:
			versionOne = append(versionOne, spelled)
		}
	}
	switch {
	case len(invalid) > 0:
		return gitRefuses(path, fmt.Sprintf("its config sets %s, which git does not take",
			strings.Join(invalid, ", ")), "git stops at a value it does not take, whatever the format version")
	case compats > 1:
		return gitRefuses(path, "its config sets extensions.compatObjectFormat more than once, which git takes "+
			"once only", "git stops at the second line, whatever the format version (\"'extensions.compatobjectformat' "+
			"already specified\"): keeping one of them makes it a repository git opens")
	case version == unsetVersion:
		return nil
	case version > 1:
		return unsupportedFormat(path, []string{"core.repositoryformatversion = " + written}, false)
	case version >= 1 && len(unknown) > 0:
		return unsupportedFormat(path, unknown, false)
	case version == 0 && len(versionOne) > 0:
		return gitRefuses(path, fmt.Sprintf("its core.repositoryformatversion is 0, and its config sets %s, which "+
			"only format version 1 has", strings.Join(versionOne, ", ")),
			"git says \"repo version is 0, but v1-only extension found\"; setting core.repositoryformatversion "+
				"to 1 makes it a repository git reads, where the extension is meant")
	case compat != "" && compat == objects:
		return gitRefuses(path, fmt.Sprintf("its config sets extensions.compatObjectFormat = %s, the format its "+
			"objects are named in already", compat), "git 2.50 stops at it (\"BUG: hash_algo and compat_hash_algo "+
			"match\"): a second format that is the first one is none, and unsetting extensions.compatObjectFormat "+
			"makes it a repository git opens")
	case len(unread) > 0:
		return unsupportedFormat(path, unread, true)
	}
	return nil
}

// unsetVersion is the format version of a repository whose config sets
// none, as git's read_repository_format has it, -1: git reads such a
// repository as the SHA-1 one of files it was before extensions, and passes
// over every extension it sets, worktreeConfig and partialClone among them.
// `git rev-parse --show-object-format` says sha1 there whatever
// extensions.objectFormat says, where a repository of version 0 setting it is
// refused.
const unsetVersion = -1

// formatVersion is the format version cfg sets, as written, unsetVersion
// where it sets none; err where it is not a number.
func formatVersion(cfg *gitconfig.Config) (version int, written string, err error) {
	if !cfg.Raw.HasSection("core") {
		return unsetVersion, "", nil
	}
	core := cfg.Raw.Section("core")
	if !core.HasOption("repositoryformatversion") {
		return unsetVersion, "", nil
	}
	written = strings.TrimSpace(core.Option("repositoryformatversion"))
	version, err = strconv.Atoi(written)
	return version, written, err
}

// extensionsInEffect reports whether git reads the extensions cfg sets at
// all: not where it sets no format version (unsetVersion).
func extensionsInEffect(cfg *gitconfig.Config) bool {
	version, _, err := formatVersion(cfg)
	return err == nil && version != unsetVersion
}

// readable reports whether a capability that reads what can read a
// repository whose config sets the version 1 extension name to value: refs
// kept in a reftable are unreadable as files, and objects named by anything
// but SHA-1 are unreadable at all. extensions.compatObjectFormat changes
// neither: git keeps, beside objects named by SHA-1, a map from each name to
// the one it has in the other format, and `git rev-parse HEAD` there names
// the SHA-1 object this reads.
func readable(name, value string, what reads) bool {
	switch name {
	case "refstorage":
		return what == readsConfig || value == "files"
	case "objectformat":
		return what != readsObjects || value == "sha1"
	}
	return true
}

// gitRefuses is the refusal of a repository git itself refuses to open, why,
// and what makes it one git opens.
func gitRefuses(path, why, hint string) *view.Error {
	return view.Errorf("git.repository.invalid", "%s is a repository git refuses to open: %s", path, why).
		WithHint(hint)
}

// unsupportedFormat is the refusal of a repository in a format this reader
// does not read, naming the settings that make it one, as the config spells
// them; gitReads says whether git does.
//
// **A repository git reads is not "not a git repository".** `git init
// --object-format=sha256` sets extensions.objectFormat, and `git init
// --ref-format=reftable`, or `git refs migrate`, extensions.refStorage: the
// first names its objects by SHA-256 and the second keeps its refs in a
// reftable, and go-git reads neither. Every capability here passed go-git's
// refusal on as git.notarepo, telling the caller to find a directory inside a
// git repository while standing in one.
func unsupportedFormat(path string, set []string, gitReads bool) *view.Error {
	hint := "git itself reads it; this reader opens a repository whose objects are named by SHA-1, whose refs " +
		"are files, and which holds every object it names"
	if !gitReads {
		hint = "git 2.50 does not read it either: a newer git may"
	}
	return view.Errorf("git.repository.unsupported", "%s is a git repository in a format this reader does not "+
		"support yet: its config sets %s", path, strings.Join(set, ", ")).WithHint(hint)
}

// decidedFormat is a repository's storage as git.Open is handed it, once
// repositoryFormat has decided on its format: its config with no extension
// and no format version, which go-git would otherwise decide on again, by
// its own rules. The storage itself, whose config is the whole file, is the
// one every capability reads (openAt).
type decidedFormat struct{ *filesystem.Storage }

func (s decidedFormat) Config() (*gitconfig.Config, error) {
	cfg, err := s.Storage.Config()
	if err != nil {
		return nil, err
	}
	cfg.Raw.RemoveSection("extensions")
	cfg.Core.RepositoryFormatVersion = ""
	return cfg, nil
}

// partialClone refuses the repository root holds to a capability that reads
// its objects where git reads it as a partial clone (notPartial), by every
// file of config git reads for it (gitConfigs). One of them that cannot be
// read is refused too: whether the repository is a partial clone may be set
// there, and git itself stops at a file of config it cannot parse, or that
// is not a file.
func partialClone(ctx context.Context, req plugin.Request, repo *git.Repository, root string) *view.Error {
	own, err := repo.Config()
	var files []scopedConfig
	if err == nil {
		files, err = gitConfigs(ctx, req, repo)
	}
	if verr := refusedByTheGate(err); verr != nil {
		return verr
	}
	if err != nil {
		return view.Errorf("git.config.unreadable", "%s: reading the config git reads for it, which says whether "+
			"it is a partial clone: %v", root, err).
			WithHint("git reads the same files for every repository; `git config --list --show-origin` names them")
	}
	return notPartial(files, own, root)
}

// notPartial refuses a partial clone, which root holds and files are the
// config git reads for (gitConfigs), to a capability that reads its objects;
// own is the whole of the repository's own config, which its format is read
// from.
//
// **A partial clone lacks objects on purpose.** `git clone --filter=blob:none`
// fetches the history and leaves each file's content on the server until git
// needs it, and git fetches it then; this reader fetches nothing, and failed
// at the first object the clone did not hold, as whatever capability was
// running, or read a tree it lacked as an empty one. git marks the remote it
// fetches from as a promisor, remote.<name>.promisor, and an older git named
// it in extensions.partialClone too, which git reads only where the config
// sets a format version (unsetVersion); either is refused here up front, as a
// pack this reader cannot open is (objectsAllReadable), naming why. A
// promisor set with no value at all, `promisor` alone on its line, is one git
// reads as true, and go-git as nothing (valuelessKeys).
//
// **extensions.partialClone set to nothing names a remote too**, the one
// named nothing: git takes the empty value as a name, reads the repository as
// a partial clone, and fetches what it lacks by running git fetch with the
// empty name, where this read it as none. The refusal names it as "". Set
// with no value at all, it is a value git refuses to open the repository
// with, and so is it here, before this is asked (repositoryFormat).
//
// **Any line that makes a remote a promisor makes it one.** git's
// promisor_remote_config adds the remote at each such line and takes none
// away, so `promisor = true` then `promisor = false` is a promisor git
// fetches from: reading the last value alone let such a clone through, to
// fail at the first object it lacked.
//
// **A filter makes a promisor too.** promisor_remote_config makes a promisor
// of each remote a remote.<name>.partialCloneFilter line names, set to
// anything, nothing included, whatever the remote's promisor line says or
// where it has none: git fetches what the repository lacks from it. This
// read the promisor line alone. A filter with no value at all is one git
// refuses to fetch with ("missing value"), and a reader that fetches nothing
// is refused it all the same.
//
// **In every file git reads, not the repository's alone.** git reads
// remote.<name>.promisor through repo_config, the system, global,
// repository, worktree and command scopes at once, so a line in ~/.gitconfig
// or `git -c remote.origin.promisor=true` makes git fetch what it lacks from
// origin in a repository whose own config says nothing of it; this read the
// repository's own config alone, and let such a clone through. What the
// refusal names of the operator's files is the file, as git.hooks names one,
// and the remote, masked as git.config masks a key. A file one of them
// includes is read where git reads it, and named as they are; a remote named
// in one the caller is not shown (scopedConfig.hidden) is not named.
// extensions.partialClone is part of the repository's format, which git reads
// from the repository's own config alone, following no include, as this does.
func notPartial(files []scopedConfig, own *gitconfig.Config, root string) *view.Error {
	var promisors, elsewhere []string
	unnamed := false
	mark := func(name string) {
		if !slices.Contains(promisors, name) {
			promisors = append(promisors, name)
		}
	}
	if extensionsInEffect(own) && own.Raw.HasSection("extensions") {
		if ext := own.Raw.Section("extensions"); ext.HasOption("partialClone") {
			mark(ext.Option("partialClone"))
		}
	}
	for _, f := range files {
		cfg := f.config
		if !cfg.Raw.HasSection("remote") {
			continue
		}
		for _, sub := range cfg.Raw.Section("remote").Subsections {
			if !f.blank[configKey("remote", sub.Name, "promisor")].any &&
				!slices.ContainsFunc(sub.Options.GetAll("promisor"), gitBool) && !sub.HasOption("partialCloneFilter") {
				continue
			}
			if f.hidden {
				unnamed = true
			} else {
				mark(sub.Name)
			}
			if (f.included || f.scope != "local" && f.scope != "worktree") && !slices.Contains(elsewhere, f.place()) {
				elsewhere = append(elsewhere, f.place())
			}
		}
	}
	if len(promisors) == 0 && !unnamed {
		return nil
	}
	sort.Strings(promisors)
	for i, name := range promisors {
		promisors[i] = maskURLCredentials(name)
		if name == "" {
			promisors[i] = `""`
		}
	}
	if unnamed {
		promisors = append(promisors, "a remote named in a file included from outside the roots")
	}
	hint := "git.config, git.hooks and git.remotes answer here; for history and files, run this on a clone made " +
		"without --filter, which holds every object"
	if len(elsewhere) > 0 {
		hint = "a remote is made a promisor in " + strings.Join(elsewhere, ", ") + ", which git reads for every " +
			"repository: where this one is no partial clone, unset it there; git.config, git.hooks and " +
			"git.remotes answer here either way"
	}
	return view.Errorf("git.objects.partial", "%s is a partial clone: git fetches the objects it lacks from %s as "+
		"it needs them, and this reader fetches nothing", root, strings.Join(promisors, ", ")).WithHint(hint)
}
