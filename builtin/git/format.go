package git

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/storage/filesystem"

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
// extension at all (decidedFormat).
//
// git reads each extension's value as it reads the config, before it looks at
// the version, so a value it does not take stops it whatever the version,
// one it would then pass over included: `extensions.refStorage = bogus` is
// "bad config line" in a repository that sets no version at all. So does no
// value at all where the extension names something: `partialClone` alone on
// its line is "missing value", where go-git reads it as set to nothing and
// this opened a repository git refuses. blank is each key the config sets
// with no value (valuelessKeys).
func repositoryFormat(path string, cfg *gitconfig.Config, blank map[string]valueless, what reads) *view.Error {
	version, written, err := formatVersion(cfg)
	if err != nil {
		return gitRefuses(path, fmt.Sprintf("its config sets core.repositoryformatversion = %s, which is not a "+
			"number", written), "git refuses to read a format version it cannot count")
	}
	var versionOne, invalid, unknown, unread []string
	for _, o := range cfg.Raw.Section("extensions").Options {
		name, value := strings.ToLower(o.Key), strings.TrimSpace(o.Value)
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
		case !readable(name, value, what):
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
