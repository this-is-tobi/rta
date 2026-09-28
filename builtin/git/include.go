package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/go-git/gcfg"
	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// **git reads a file an include names as though it were written in place of
// the directive, and this read none of them.** `[include] path = …` and
// `[includeIf "<condition>"] path = …` are how a partial clone's promisor, a
// core.hooksPath or a core.excludesFile comes to be set in a file the
// repository's own config does not hold: a company's shared config included
// from ~/.gitconfig, a work identity included for every repository under
// ~/work. git.hooks, the answer an audit of what runs on a commit relies on,
// listed the repository's own hooks while git ran the directory an included
// file named, and a partial clone set up that way reached the object readers
// as a whole one. So every decision taken from the config git reads
// (gitConfigs) follows each include as git follows it, and git.config shows
// what they set.
//
// **Except the repository's format, which git never takes from an include.**
// git decides the format version, each extension, core.worktree and core.bare
// from the repository's config file alone, before it reads any other
// (read_repository_format), and follows no include there: an include setting
// extensions.objectFormat = sha256 leaves the repository the SHA-1 one git
// opens, and one setting core.worktree leaves the working tree where it was.
// repositoryFormat, worktreeConfig and configuredWorktree read that file alone
// too.
//
// **An include can name any file on the machine, and an INI-shaped one parses
// as config.** A MySQL client's ~/.my.cnf, its password among its keys, is a
// file of sections and keys git reads as config without a complaint (git
// refuses ~/.aws/credentials, whose keys hold underscores), so a repository's
// .git/config, which a caller can write inside the root, could have
// git.config display another file's secrets. Over MCP an include the
// repository's own config makes of a file outside the server's roots is not
// followed at all, and git.config and git.hooks name it as one outside the
// roots (configReading.include).

// maxIncludeDepth is how deep git follows an include of an include, its
// MAX_INCLUDE_DEPTH: one more is an error git stops at ("exceeded maximum
// include depth"), which is how a file that includes itself, or two that
// include each other, end.
const maxIncludeDepth = 10

// maxIncludes and maxIncludedBytes bound what one reading of the config
// follows its includes into, in all.
//
// **git bounds how deep includes go, not how many there are.** A file that
// includes the next twice, ten files deep and no cycle among them, is a
// thousand files read, and a 4 MiB file of nothing but `path = next` lines
// includes the next three hundred thousand times, each doing the same: a
// config a caller writes inside the root would cost one call more reads than
// could ever end. An include is config git reads as though it were written in
// place of the directive, so everything the includes of one reading read is
// held together to the bound one file of config is held to, and to a thousand
// files, where a person's includes are a few, each of which git reads too.
const (
	maxIncludes      = 1000
	maxIncludedBytes = maxConfigBytes
)

// configLine is one line of config as git reads it, in the order it reads
// them: a section's header, where name is "", or a key in it, set to value,
// or with no value at all where none is true (valueless). emptySubsection is
// a key of the environment's with a subsection of nothing, `core..hooksPath`,
// which is a key git tells apart from core.hooksPath; a file cannot spell one.
type configLine struct {
	section, subsection, name, value string
	none, emptySubsection            bool
}

// fileLines is content, a file of git config, read into its lines with gcfg,
// the reader go-git reads config with, as go-git's decoder reads it.
func fileLines(content []byte) ([]configLine, error) {
	var lines []configLine
	read := func(section, subsection, name, value string, blank bool) error {
		lines = append(lines, configLine{section: section, subsection: subsection, name: name, value: value,
			none: blank && name != ""})
		return nil
	}
	err := gcfg.ReadWithCallback(bytes.NewReader(content), read)
	return lines, err
}

// rawLines is cfg, config already read, as the lines it holds: for a clone in
// memory, whose config go-git makes rather than reads, and which includes
// nothing.
func rawLines(cfg *gitconfig.Config) []configLine {
	var lines []configLine
	for _, s := range cfg.Raw.Sections {
		lines = append(lines, configLine{section: s.Name})
		for _, o := range s.Options {
			lines = append(lines, configLine{section: s.Name, name: o.Key, value: o.Value})
		}
		for _, sub := range s.Subsections {
			lines = append(lines, configLine{section: s.Name, subsection: sub.Name})
			for _, o := range sub.Options {
				lines = append(lines, configLine{section: s.Name, subsection: sub.Name, name: o.Key, value: o.Value})
			}
		}
	}
	return lines
}

// configSource is one file of config git reads for its scope, or the
// environment's, read into its lines and not yet followed into what it
// includes: from is what each piece of it is (scope, path and file).
type configSource struct {
	from  scopedConfig
	lines []configLine
}

// continued is a piece of config that goes on where f leaves off, in the same
// file, after an include: nothing read into it yet.
func (f scopedConfig) continued() scopedConfig {
	return scopedConfig{scope: f.scope, path: f.path, file: f.file, included: f.included,
		config: gitconfig.NewConfig(), blank: map[string]valueless{}}
}

// add reads l into f, as go-git's decoder adds a line to the config it reads.
func (f *scopedConfig) add(l configLine) {
	switch {
	case l.name == "" && l.subsection == "":
		f.config.Raw.Section(l.section)
	case l.name == "":
		f.config.Raw.Section(l.section).Subsection(l.subsection)
	default:
		key := configKey(l.section, l.subsection, l.name)
		if l.emptySubsection {
			f.config.Raw.Section(l.section).Subsection("").AddOption(l.name, l.value)
			key = strings.ToLower(l.section) + ".." + strings.ToLower(l.name)
		} else {
			f.config.Raw.AddOption(l.section, l.subsection, l.name, l.value)
		}
		f.blank[key] = valueless{any: f.blank[key].any || l.none, last: l.none}
	}
}

// configReading is one reading of the config git reads for a repository,
// which follows each include as git follows it (follow).
type configReading struct {
	req    plugin.Request
	repo   *git.Repository
	budget *statusBudget
	// gitDir is the repository's git directory as a gitdir: condition is
	// matched against it, symbolic links resolved, and gitDirFound as this
	// found it: git tries the one, then the other. "" where the repository
	// has none on disk, as a clone in memory has none.
	gitDir, gitDirFound string
	// branch is the branch HEAD names, as an onbranch: condition is matched
	// against it, "" where it names none; noBranch is why this cannot tell,
	// and headRead that both are read.
	branch, noBranch string
	headRead         bool
	// everything is every source of config git reads for the repository, for
	// the URLs a hasconfig:remote.*.url condition is matched against, which
	// urls holds once read.
	everything func() ([]configSource, error)
	urls       []string
	urlsRead   bool
	// collecting is the reading git makes for those URLs (remoteURLs).
	collecting bool
	// followed and read are how many includes this reading followed, and how
	// many bytes it read of them, which maxIncludes and maxIncludedBytes bound.
	followed int
	read     int64
}

// newConfigReading is a reading of the config git reads for repo, for req,
// held to one call's time for matching conditions, as a status is
// (statusBudget).
func newConfigReading(ctx context.Context, req plugin.Request, repo *git.Repository) *configReading {
	r := &configReading{req: req, repo: repo, budget: &statusBudget{ctx: ctx, deadline: statusDeadline(ctx)}}
	if store, ok := repo.Storer.(*filesystem.Storage); ok {
		r.gitDirFound = store.Filesystem().Root()
		r.gitDir = r.gitDirFound
		if resolved, err := filepath.EvalSymlinks(r.gitDirFound); err == nil {
			r.gitDir = resolved
		}
	}
	return r
}

// follow is sources read as git reads them, one after the other, each
// followed into what it includes (expand): the pieces of config git reads,
// in its order, a later one's value winning.
func (r *configReading) follow(sources []configSource) ([]scopedConfig, error) {
	var out []scopedConfig
	for _, s := range sources {
		pieces, err := r.expand(s.from, s.lines, 0, false)
		if err != nil {
			return nil, err
		}
		out = append(out, pieces...)
	}
	if r.budget.over != nil {
		if errors.Is(r.budget.over, errStatusTime) {
			return nil, fmt.Errorf("matching the config's includeIf conditions took longer than the %v one call "+
				"spends on them", statusTime)
		}
		return nil, r.budget.over
	}
	return out, nil
}

// includeVerdict is what git does at a line of config: pass over it, follow
// the include it is, or what this cannot tell (cannotTell).
type includeVerdict int

const (
	passes includeVerdict = iota
	follows
	cannotTell
)

// expand is lines, the lines of from's file, read into pieces of config at
// depth, the number of includes that led to it: one piece to each include git
// follows, which ends it, then the pieces of the file it includes, then one
// going on after it — so that a key set after an include wins over the file
// it includes, and one set before it loses, as git reads them in place. An
// include this cannot decide ends a piece too, which records why
// (scopedConfig.undecided), so that an answer knows whether what it read
// could have been set there after it, and so does an include not followed
// over MCP, as a file outside the roots (scopedConfig.outside). forbid is a
// file git reads, looking for remote URLs (remoteURLs), where one may not be
// set.
func (r *configReading) expand(from scopedConfig, lines []configLine, depth int, forbid bool) ([]scopedConfig, error) {
	var out []scopedConfig
	cur := from.continued()
	for _, l := range lines {
		cur.add(l)
		if r.collecting {
			if err := r.collect(cur, l, forbid); err != nil {
				return nil, err
			}
		}
		value, conditional, why, verdict, err := r.directive(cur, l)
		if err == nil && verdict == follows {
			value, why, err = includePath(cur, value)
			if why != "" {
				verdict = cannotTell
			}
		}
		switch {
		case err != nil:
			return nil, err
		case verdict == passes:
			continue
		case verdict == cannotTell:
			cur.undecided = why
			out, cur = append(out, cur), cur.continued()
			continue
		}
		included, outside, err := r.include(cur, value, depth+1, forbid || r.collecting && conditional)
		switch {
		case err != nil:
			return nil, err
		case outside:
			cur.outside = l.value
			out, cur = append(out, cur), cur.continued()
		case included != nil:
			out = append(append(out, cur), included...)
			cur = cur.continued()
		}
	}
	return append(out, cur), nil
}

// directive is what git does at l, a line of the piece f: follow the include
// it is to value (follows), pass over it (passes), or what this cannot tell,
// and why (cannotTell). conditional is an includeIf's.
//
// **Read as git's git_config_include reads it.** include.path, and a path
// under includeIf whose condition holds, spelled in any case but the
// condition's, which is compared as written: `[includeIf "GitDir:…"]` is a
// condition git does not know, and false, as is one it has no prefix for.
// `[include "x"] path` is no include at all. The condition is evaluated for
// every key under it, as git evaluates it, before the key is looked at. An
// include with no value at all, where git follows it, is a value git refuses
// to run with ("missing value for 'include.path'").
func (r *configReading) directive(f scopedConfig, l configLine) (value string, conditional bool, why string,
	verdict includeVerdict, err error,
) {
	key := "include.path"
	switch {
	case l.name == "":
		return "", false, "", passes, nil
	case strings.EqualFold(l.section, "include") && l.subsection == "" && !l.emptySubsection &&
		strings.EqualFold(l.name, "path"):
	case strings.EqualFold(l.section, "includeIf") && l.subsection != "":
		holds, why, err := r.condition(f, l.subsection)
		switch {
		case err != nil:
			return "", true, "", passes, err
		case !strings.EqualFold(l.name, "path"), !holds && why == "":
			return "", true, "", passes, nil
		case why != "":
			return "", true, why, cannotTell, nil
		}
		key, conditional = "includeIf."+l.subsection+".path", true
	default:
		return "", false, "", passes, nil
	}
	if l.none {
		return "", conditional, "", passes, fmt.Errorf("%s sets %s with no value, which git refuses to run with "+
			"(missing value for 'include.path')", f.place(), key)
	}
	return l.value, conditional, "", follows, nil
}

// condition is whether the condition of an includeIf, cond, holds for this
// repository, as git's include_condition_is_true decides it; why is what
// keeps this from telling, where something does.
func (r *configReading) condition(f scopedConfig, cond string) (holds bool, why string, err error) {
	switch {
	case strings.HasPrefix(cond, "gitdir:"):
		return r.gitDirMatches(f, strings.TrimPrefix(cond, "gitdir:"), false)
	case strings.HasPrefix(cond, "gitdir/i:"):
		return r.gitDirMatches(f, strings.TrimPrefix(cond, "gitdir/i:"), true)
	case strings.HasPrefix(cond, "onbranch:"):
		return r.onBranch(strings.TrimPrefix(cond, "onbranch:"))
	case strings.HasPrefix(cond, remoteURLCondition):
		return r.hasRemoteURL(strings.TrimPrefix(cond, remoteURLCondition))
	}
	return false, "", nil
}

// remoteURLCondition is the prefix of the one hasconfig: condition git
// knows, which a URL of any remote in the config matches.
const remoteURLCondition = "hasconfig:remote.*.url:"

// gitDirMatches is whether the repository's git directory matches pattern, a
// gitdir: or, where fold is set, a gitdir/i: condition, as git's
// include_by_gitdir matches it: ~ expanded, the home directory's links
// resolved; a pattern starting with ./ taken from the directory of f, the
// file it is written in, links resolved, and that part compared as it is
// written rather than as a pattern; another relative one matched anywhere,
// with **/ in front of it; one ending in / matching everything under it; and
// matched with git's own matcher, * matching no slash, first against the git
// directory with its links resolved and then as it was found, so that
// gitdir:~/work/ holds for a repository reached through a link into ~/work.
// A clone in memory has no git directory, and matches none, as git run
// outside a repository matches none.
func (r *configReading) gitDirMatches(f scopedConfig, pattern string, fold bool) (bool, string, error) {
	if r.gitDir == "" {
		return false, "", nil
	}
	if strings.HasPrefix(pattern, installPrefix) {
		return false, prefixUnknown, nil
	}
	if expanded, ok := interpolatePath(pattern, true); ok {
		pattern = expanded
	}
	prefix := 0
	switch {
	case strings.HasPrefix(pattern, "./"):
		// git says "relative config include conditionals must come from files"
		// of one in its environment, and takes the condition as false.
		if f.file == "" {
			return false, "", nil
		}
		dir := filepath.Dir(f.file)
		if resolved, err := filepath.EvalSymlinks(f.file); err == nil {
			dir = filepath.Dir(resolved)
		}
		pattern, prefix = dir+pattern[1:], len(dir)+1
	case !filepath.IsAbs(pattern):
		pattern = "**/" + pattern
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	flags := wmPathname
	if fold {
		flags |= wmCasefold
	}
	for _, text := range []string{r.gitDir, r.gitDirFound} {
		if len(text) >= prefix && sameBytes(pattern[:prefix], text[:prefix], fold) &&
			wildmatch(pattern[prefix:], text[prefix:], flags, r.budget) {
			return true, "", nil
		}
	}
	return false, "", nil
}

// sameBytes reports whether a and b are the same bytes, a letter in either
// case where fold is set, as C's strncmp and strncasecmp compare them.
func sameBytes(a, b string, fold bool) bool {
	if !fold || len(a) != len(b) {
		return a == b
	}
	for i := range len(a) {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

// onBranch is whether the branch HEAD names matches pattern, an onbranch:
// condition, as git's include_by_branch matches it: the branch's name, the
// part after refs/heads/, where HEAD names a branch, whether or not it has a
// commit yet, and a detached HEAD matching nothing; one ending in / matching
// every branch under it.
func (r *configReading) onBranch(pattern string) (bool, string, error) {
	if !r.headRead {
		r.branch, r.noBranch, r.headRead = headBranch(r.repo), "", true
		if own, err := r.repo.Config(); err == nil && refsInReftable(own) {
			r.branch, r.noBranch = "", "the branch it turns on is kept in a reftable, which this does not read"
		}
	}
	if r.noBranch != "" || r.branch == "" {
		return false, r.noBranch, nil
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	return wildmatch(pattern, r.branch, wmPathname, r.budget), "", nil
}

// symrefMaxDepth is how many symbolic refs git follows from one before it
// gives up on it, its SYMREF_MAXDEPTH.
const symrefMaxDepth = 5

// headBranch is the branch repo's HEAD names, as git resolves it for an
// onbranch: condition: through each symbolic ref to the last, which need not
// exist yet, "" where HEAD is detached or names no branch.
func headBranch(repo *git.Repository) string {
	name, symbolic := plumbing.HEAD, false
	for hops := 0; ; hops++ {
		ref, err := repo.Storer.Reference(name)
		if err != nil || ref.Type() != plumbing.SymbolicReference {
			break
		}
		if hops == symrefMaxDepth {
			return ""
		}
		name, symbolic = ref.Target(), true
	}
	branch, ok := strings.CutPrefix(name.String(), "refs/heads/")
	if !symbolic || !ok {
		return ""
	}
	return branch
}

// refsInReftable reports whether the repository whose own config is cfg keeps
// its refs in a reftable, where git reads it as one (extensionsInEffect).
func refsInReftable(cfg *gitconfig.Config) bool {
	return extensionsInEffect(cfg) && cfg.Raw.HasSection("extensions") &&
		cfg.Raw.Section("extensions").Option("refStorage") == "reftable"
}

// hasRemoteURL is whether a URL of any remote the config names matches
// pattern, a hasconfig:remote.*.url: condition, as git's
// include_by_remote_url matches it, with git's matcher, * matching no slash.
// While the URLs themselves are being read, every such condition holds, as it
// does for git (remoteURLs).
func (r *configReading) hasRemoteURL(pattern string) (bool, string, error) {
	if r.collecting {
		return true, "", nil
	}
	urls, err := r.remoteURLs()
	if err != nil {
		return false, "", err
	}
	matches := func(url string) bool { return wildmatch(pattern, url, wmPathname, r.budget) }
	return slices.ContainsFunc(urls, matches), "", nil
}

// remoteURLs is the URL of every remote the config git reads names, read as
// git's populate_remote_urls reads them, once, the first time a condition asks.
//
// **git reads the whole of its config a second time for them**, every scope
// and every file an include names, including the ones an includeIf names
// whatever its hasconfig: condition says, so a URL set after the condition,
// or in another file, counts. A file an includeIf names, or one included from
// such a file, may set no URL in that reading: git refuses to run where one
// does ("remote URLs cannot be configured in file directly or indirectly
// included by includeIf.hasconfig:remote.*.url"), whatever condition the
// includeIf has, and so is it refused here (collect).
func (r *configReading) remoteURLs() ([]string, error) {
	if r.urlsRead {
		return r.urls, nil
	}
	sources, err := r.everything()
	if err != nil {
		return nil, err
	}
	c := &configReading{req: r.req, repo: r.repo, budget: r.budget, gitDir: r.gitDir, gitDirFound: r.gitDirFound,
		branch: r.branch, noBranch: r.noBranch, headRead: r.headRead, collecting: true}
	if _, err := c.follow(sources); err != nil {
		return nil, err
	}
	r.urls, r.urlsRead = c.urls, true
	return r.urls, nil
}

// collect keeps l, a line of the piece f, where it sets a remote's URL, for
// remoteURLs; forbid is a file an includeIf names, where git refuses one.
//
// **Over MCP a URL the caller is not shown is matched with its credentials
// masked**, as git.config would show it: the operator's own config's and the
// environment's. The condition is
// a glob the caller writes into the repository's config, and whether the file
// it names was followed shows in the answer, so matching the URL as written
// would let a caller spell out, one character after another, a token kept in
// a URL it can never read. A pattern that is not after a credential matches
// the masked URL as it matches the URL.
func (r *configReading) collect(f scopedConfig, l configLine, forbid bool) error {
	if l.name == "" || !strings.EqualFold(l.section, "remote") || l.subsection == "" && !l.emptySubsection ||
		!strings.EqualFold(l.name, "url") {
		return nil
	}
	if forbid {
		return fmt.Errorf("%s sets a remote's URL, and an includeIf includes it, which git refuses to run with where "+
			"the config has an includeIf.hasconfig:remote.*.url condition (remote URLs cannot be configured in file "+
			"directly or indirectly included by includeIf.hasconfig:remote.*.url)", f.place())
	}
	if l.none {
		return nil
	}
	url := l.value
	if r.req.Surface() == plugin.SurfaceMCP && f.scope != "local" && f.scope != "worktree" {
		url = maskURLCredentials(url)
	}
	r.urls = append(r.urls, url)
	return nil
}

// include is the pieces of path, the file an include in the piece f names,
// where git follows it at depth; nil where there is no such file, which git
// passes over. outside is an include not followed, over MCP, as one of a file
// outside the roots.
//
// **Over MCP an include the repository's own config makes of a file outside
// the roots is not followed at all**, in the repository's config, its
// worktree's, or a file either includes from inside the roots, which are the
// files a caller can write. Whether git reads a file shows in every answer
// here — one that is not there is passed over, one that is not config refuses
// the call, one that parses counts, and one past the bytes one reading follows
// refuses it — so following such an include, even without showing a key of
// what it reads, told a caller of any file on the machine whether it exists,
// whether it parses as config, and, by bisection against that bound, how large
// it is. So the file is not opened, nor looked at: the gate's judgement of the
// path, the one every path an MCP caller sends gets, is all that is asked, and
// every answer is the one an include of a missing file gets, whatever is
// there. git reads it, and may run with what it sets, so git.config and
// git.hooks name the include (scopedConfig.outside). A file the operator's own
// config or the environment includes is the operator's, and read as they are,
// wherever it is.
//
// **Outside the roots is the one refusal of the gate that is passed over.**
// Every other refuses the file whatever would read it, and so refuses the
// call here: rta's own state or configuration, which nothing an agent reaches
// may read however little of it an answer shows (an include naming the
// secret store's identity from inside a root drawn around the home directory
// was read, and counted), and on Windows a network share, whose opening is
// the very connection the gate refuses it to prevent.
//
// Counted among the includes one reading follows before the gate is asked,
// since each judgement is a look at the filesystem, and a config of nothing
// but includes costs as many.
func (r *configReading) include(f scopedConfig, path string, depth int, forbid bool) (_ []scopedConfig,
	outside bool, _ error,
) {
	if r.followed++; r.followed > maxIncludes {
		return nil, false, fmt.Errorf("the config includes more than %d files in all, which this does not read: %s "+
			"includes %s past them", maxIncludes, f.place(), path)
	}
	read := path
	if f.scope == "local" || f.scope == "worktree" {
		judged, verr := r.req.Confine("path", path)
		switch {
		case verr != nil && verr.Code == outsideTheRoots:
			return nil, true, nil
		case verr != nil:
			return nil, false, verr
		}
		read = judged
	}
	content, err := readConfigFile(read)
	if errors.Is(err, iofs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s includes %s, which git stops at: %w", f.place(), path, err)
	}
	if depth > maxIncludeDepth {
		return nil, false, fmt.Errorf("%s includes %s past the %d includes of includes git follows (exceeded maximum "+
			"include depth), as a file that includes itself, or files that include each other, do", f.place(), path,
			maxIncludeDepth)
	}
	if r.read += int64(len(content)); r.read > maxIncludedBytes {
		return nil, false, fmt.Errorf("%s includes %s, and the files the config includes are larger than the %s this "+
			"reads of them in all", f.place(), path, format.Bytes(int64(maxIncludedBytes)))
	}
	lines, err := fileLines(content)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	pieces, err := r.expand(scopedConfig{scope: f.scope, path: path, file: path, included: true}, lines, depth, forbid)
	return pieces, false, err
}

// outsideTheRoots is the path gate's refusal of a path outside the server's
// roots, the one an include is passed over for (configReading.include).
const outsideTheRoots = "core.mcp.path.outside"

// refusedByTheGate is err where it is the path gate's refusal of a file an
// include names, which a capability hands on as the gate worded it, as it
// hands on the gate's refusal of any other path it reaches; nil otherwise.
func refusedByTheGate(err error) *view.Error {
	var verr *view.Error
	if errors.As(err, &verr) && strings.HasPrefix(verr.Code, "core.mcp.path.") {
		return verr
	}
	return nil
}

// installPrefix is how a path names the directory git was installed under,
// which only the git that reads it knows, and prefixUnknown what an include
// naming it is not read for.
const (
	installPrefix = "%(prefix)/"
	prefixUnknown = "it names the directory git was installed under"
)

// includePath is the file an include of value in the piece f names, as git's
// handle_path_include resolves it: ~ expanded (interpolatePath), and a
// relative path taken from the directory of f, the file it is written in, as
// that is spelled rather than where its links lead. why is what keeps this
// from telling, and err what git stops at: a ~user it cannot expand, and a
// relative path in its environment, which is no file ("relative config
// includes must come from files").
func includePath(f scopedConfig, value string) (path, why string, err error) {
	expanded, why := configPathname(value)
	switch {
	case why == homeUnset:
		return "", "", fmt.Errorf("%s includes %s, and %s (could not expand include path)", f.place(), value, why)
	case why != "":
		return "", why, nil
	case filepath.IsAbs(expanded):
		return expanded, "", nil
	case f.file == "":
		return "", "", fmt.Errorf("%s includes %s, a relative path, which git takes from the file the include is "+
			"written in and refuses here (relative config includes must come from files)", f.place(), value)
	}
	// Joined as git joins them, without cleaning: the kernel resolves a link
	// before the .. after it, and Clean would take the .. off the name first.
	return filepath.Dir(f.file) + string(filepath.Separator) + expanded, "", nil
}

// Why a path git's config holds is one this cannot tell the place of
// (configPathname), besides the install prefix (prefixUnknown).
const (
	homeUnset   = "HOME is not set for git to expand ~ by"
	userUnknown = "its home directory is one this cannot look up"
)

// configPathname is value, a path git's config holds, as git's
// git_config_pathname and an include read it (interpolatePath): ~ and ~user
// expanded, and not yet taken from any directory. why is what keeps this from
// telling where it leads, where something does: a path under git's install
// prefix (prefixUnknown), ~ with no HOME (homeUnset), which git refuses to run
// with, and a ~user this cannot look up (userUnknown), which git expands or
// refuses to run with.
//
// **Not the ~ alone that plugin.ExpandHome expands.** git reads
// `core.hooksPath = ~ci/hooks` as the hooks in ci's home, and this read it as
// a directory named ~ci in the working tree: git.hooks listed what was there
// as the hooks git runs, and git.status applied a core.excludesFile under it
// that git never reads.
func configPathname(value string) (path, why string) {
	if strings.HasPrefix(value, installPrefix) {
		return "", prefixUnknown
	}
	expanded, ok := interpolatePath(value, false)
	switch {
	case ok:
		return expanded, ""
	case value == "~" || strings.HasPrefix(value, "~/"):
		return "", homeUnset
	}
	return "", userUnknown
}

// interpolatePath is p with a leading ~ expanded as git's interpolate_path
// expands it: ~ alone or before a slash to $HOME, its links resolved where
// realHome is set, and ~user to that user's home directory; ok is false where
// there is no such home to expand it to.
//
// A ~user this cannot look up is one git may: built without cgo, os/user
// reads /etc/passwd alone on Linux, where git asks the system's name service,
// which can hold accounts from a directory such as LDAP. On macOS it asks the
// system's own lookup, cgo or not, as git does.
func interpolatePath(p string, realHome bool) (string, bool) {
	if !strings.HasPrefix(p, "~") {
		return p, true
	}
	slash := strings.IndexByte(p, '/')
	if slash < 0 {
		slash = len(p)
	}
	name, rest := p[1:slash], p[slash:]
	if name != "" {
		u, err := user.Lookup(name)
		if err != nil {
			return "", false
		}
		return u.HomeDir + rest, true
	}
	home, set := os.LookupEnv("HOME")
	if !set {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil && realHome {
		home = resolved
	}
	return home + rest, true
}
