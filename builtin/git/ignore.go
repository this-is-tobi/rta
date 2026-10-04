package git

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// maxIgnoreBytes and maxIgnorePatterns are what one status reads of the
// working tree's ignore files, and the patterns it applies from them, in all.
//
// **go-git read every ignore file whole and kept every pattern, and nothing
// bounded either.** Its status read the .gitignore of each directory it did
// not already ignore, then matched each directory it walked, and each change
// it found, against every pattern read so far: the cost was the tree times
// the patterns, from files a caller can write or a commit carry. A planted
// .gitignore of a hundred thousand patterns that match nothing cost a
// git.status of a thousand untracked directories 15 s of CPU, where git
// takes 2.6 s, and one of a million 154 s and 200 MB; git's own limit, 100
// MB, is room for ten million. The files are read here now (ignoresRead),
// and held to the same bounds.
//
// Measured against large real repositories rather than guessed: Chromium's
// 285 ignore files hold 2354 patterns in 54 KiB, the largest 9.7 KiB, and
// Linux's 405 hold about 1800 in 38 KiB. One status reads 1 MiB and applies
// 10000 patterns, about twenty and four times those, in the order it reads
// the files: the excludes file and info/exclude first, then each directory's
// .gitignore from the root down, as the untracked paths under it are
// reached. An ignore file that would take it past either is not applied at
// all rather than in part, as git applies none of a pattern file past 100 MB
// and warns, and the answer says so (unapplied): what the file ignores is
// then listed as untracked, which git would not list. Variables so a test
// can lower them.
var (
	maxIgnoreBytes    int64 = 1 << 20
	maxIgnorePatterns       = 10000
)

// statusFiles is a working tree as go-git's status reads it, which is kept
// from applying any ignore file: each untracked path it lists is put to
// git's own rules afterwards (ignoresRead.ignored).
//
// **go-git's ignore matching is not git's.** It matches a pattern with
// filepath.Match, which reads a bracket expression otherwise than git
// (wildmatch), a ** otherwise, and a directory pattern as reaching the
// directory itself where git's reaches what is in it; and it matches every
// directory it walks and every change it finds against every pattern of
// every file it read, in code this cannot hold to a bound or a deadline.
// So its status is handed a working tree whose own .gitignore reads `*`,
// which ignores each directory under the root before go-git has read any
// ignore file in it, and a pattern after that one which keeps every path
// (keepEverything): go-git reads that one file and ignores nothing. A
// .git/info/exclude it looks for in the working tree is not there, as its
// own working tree filesystem never let it be: the repository's own is read
// from where git keeps it (rootExcludeSources).
//
// **One open serves go-git's patterns and its comparison.** The status reads
// a tracked file again, to hash it, when its timestamp no longer matches the
// index's, through the same filesystem: the .gitignore it is handed `*` for
// is hashed as `*`, and called modified. restoreRootIgnore puts that right
// from the disk.
//
// **And nothing is read of it once the call's time has run out** (budget):
// each listing, open, stat and link read go-git asks for fails from then on,
// which ends its walk of the tree where it is, and the status is refused.
type statusFiles struct {
	billy.Filesystem
	budget *statusBudget
}

func (f statusFiles) ReadDir(path string) ([]os.FileInfo, error) {
	if f.budget.past() {
		return nil, f.budget.refuse("readdir", path)
	}
	f.budget.dirs++
	return f.Filesystem.ReadDir(path)
}

func (f statusFiles) Lstat(name string) (os.FileInfo, error) {
	if f.budget.past() {
		return nil, f.budget.refuse("lstat", name)
	}
	return f.Filesystem.Lstat(name)
}

func (f statusFiles) Stat(name string) (os.FileInfo, error) {
	if f.budget.past() {
		return nil, f.budget.refuse("stat", name)
	}
	return f.Filesystem.Stat(name)
}

func (f statusFiles) Readlink(name string) (string, error) {
	if f.budget.past() {
		return "", f.budget.refuse("readlink", name)
	}
	return f.Filesystem.Readlink(name)
}

// rootIgnore is the working tree's own .gitignore, the first ignore file
// go-git reads and the one it is handed `*` for.
const rootIgnore = ".gitignore"

// everything is the one pattern go-git's status is handed to read.
var everything = []byte("*\n")

func (f statusFiles) Open(name string) (billy.File, error) {
	return f.OpenFile(name, os.O_RDONLY, 0)
}

func (f statusFiles) OpenFile(name string, flag int, perm os.FileMode) (billy.File, error) {
	if f.budget.past() {
		return nil, f.budget.refuse("open", name)
	}
	if flag == os.O_RDONLY {
		switch filepath.ToSlash(filepath.Clean(name)) {
		case rootIgnore:
			return &readIgnoreFile{Reader: bytes.NewReader(everything), name: name}, nil
		case ".git/info/exclude":
			return nil, &iofs.PathError{Op: "open", Path: name, Err: iofs.ErrNotExist}
		}
	}
	f.budget.files++
	file, err := f.Filesystem.OpenFile(name, flag, perm)
	if err != nil || f.budget == nil {
		return file, err
	}
	return budgetedFile{File: file, budget: f.budget}, nil
}

// budgetedFile is a file statusFiles opened, whose reads fail once the
// call's time has run out, as its listings and opens do.
//
// **One file can hold the whole of a call's time.** go-git hashes a tracked
// file whose timestamp moved from its first byte to its last, and no listing
// or open comes between: a status of a tree holding one sparse file of 8 GiB
// took 8.5 s, the budget running out a quarter of the way into it and noticed
// only after. A read that fails leaves go-git a file it calls modified, which
// the budget's refusal then stands in for (worktreeStatus).
type budgetedFile struct {
	billy.File
	budget *statusBudget
}

func (f budgetedFile) Read(p []byte) (int, error) {
	if f.budget.past() {
		return 0, f.budget.refuse("read", f.Name())
	}
	return f.File.Read(p)
}

// keepEverything is a pattern that keeps every path, the one go-git's status
// is handed after the `*` it reads (statusFiles): go-git consults its last
// pattern first, and stops at the first that decides.
type keepEverything struct{}

func (keepEverything) Match([]string, bool) gitignore.MatchResult { return gitignore.Include }

// ignoresRead is what one status has read of its ignore files, and how it
// matches a path against them: the bytes and the patterns it has left, each
// file it has decided on, by its path in the working tree or as the answer
// names it, the files it reads ahead of any .gitignore (excludeSource),
// whether it matches without regard to case (ignoreCase), the working tree
// it reads each .gitignore from, each directory it has decided on, and the
// call's budget, which the reading and the matching are held to.
type ignoresRead struct {
	bytes    int64
	patterns int
	files    map[string]ignoreFile
	root     []excludeSource
	fold     bool
	tree     billy.Filesystem
	dirs     map[string]*ignoreDir
	budget   *statusBudget
}

// ignoreFile is one ignore file as a status decided on it: the patterns it
// applies, or why it does not apply it, and the directory its patterns
// reach, with its trailing slash, "" for the whole working tree.
type ignoreFile struct {
	patterns []ignorePattern
	why      string
	reach    string
}

// ignoreDir is a directory of the working tree as git's rules decide it:
// excluded, so that everything under it is, or else the ignore files whose
// patterns reach a path in it, its own .gitignore last, which git consults
// last first.
type ignoreDir struct {
	excluded bool
	chain    []ignoreFile
}

func newIgnoresRead(root []excludeSource, fold bool, tree billy.Filesystem, budget *statusBudget) *ignoresRead {
	return &ignoresRead{
		bytes: maxIgnoreBytes, patterns: maxIgnorePatterns, files: map[string]ignoreFile{}, root: root,
		fold: fold, tree: tree, dirs: map[string]*ignoreDir{}, budget: budget,
	}
}

// excludeSource is a file of patterns git applies to the whole working tree
// before any .gitignore: the one core.excludesFile names, and the
// repository's own info/exclude. shown is how the answer names it, and why,
// where it is set, is why it is not read at all.
//
// **go-git's status applied neither.** It reads no core.excludesFile at all,
// not even git's default ~/.config/git/ignore, so a file ignored there, a .env
// or a .DS_Store on every machine its owner uses, was listed as untracked and
// git.diff showed the .env whole. And the .git/info/exclude it looks for is
// refused by its own working tree filesystem, which opens no path through a
// .git, so a repository's info/exclude was never applied either. Both are
// read here, the excludes file first, and consulted after every .gitignore,
// the excludes file last, as git consults them.
type excludeSource struct {
	shown string
	fs    billy.Filesystem
	name  string
	why   string
}

// decideRoot decides each root source, one that is not there as one that
// holds nothing.
//
// **Before any .gitignore.** git reads the root sources first, and they are
// held to the bounds first: deciding them after the working tree's own
// .gitignore let one of 10000 patterns take the whole bound and leave the
// operator's own excludes file not applied.
func (r *ignoresRead) decideRoot() []ignoreFile {
	out := make([]ignoreFile, 0, len(r.root))
	for _, s := range r.root {
		file := ignoreFile{why: s.why}
		if s.why == "" {
			var err error
			if file, err = r.decide(s.fs, s.name, "", true); err != nil {
				file = ignoreFile{}
			}
		}
		r.files[s.shown] = file
		out = append(out, file)
	}
	return out
}

// gitignore is the .gitignore of dir, a directory of the working tree with
// its trailing slash, "" for the root, as the status decides it; ok is false
// where there is none.
func (r *ignoresRead) gitignore(dir string) (file ignoreFile, ok bool) {
	name := dir + ".gitignore"
	if file, decided := r.files[name]; decided {
		return file, true
	}
	if r.budget.past() {
		return ignoreFile{}, false
	}
	r.budget.files++
	file, err := r.decide(r.tree, name, dir, false)
	if err != nil {
		return ignoreFile{}, false
	}
	file.reach = dir
	r.files[name] = file
	return file, true
}

// decide reads the ignore file at name, when what is left of the bounds holds
// it, and counts its patterns against them; a file they do not hold is not
// read. base is the directory its patterns are taken from, and follow says
// whether a symbolic link is read through, as git reads the root sources and
// never a .gitignore. An error is for a file that is not there, which git
// passes over.
//
// One that is there and cannot be read is not applied, and named, as git
// names one it cannot read: a .gitignore that is a directory, one the user
// may not read, and one that is a symbolic link, which git does not follow
// and warns of.
func (r *ignoresRead) decide(fs billy.Filesystem, name, base string, follow bool) (ignoreFile, error) {
	var info os.FileInfo
	var err error
	if follow {
		info, err = fs.Stat(name)
	} else {
		info, err = fs.Lstat(name)
	}
	switch {
	case errors.Is(err, iofs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return ignoreFile{}, err
	case err != nil:
		return ignoreFile{why: unreadable(err)}, nil
	case info.Mode()&os.ModeSymlink != 0:
		return ignoreFile{why: "a symbolic link, which git does not follow"}, nil
	case !info.Mode().IsRegular():
		return ignoreFile{why: "not a regular file"}, nil
	case info.Size() > r.bytes:
		return ignoreFile{why: r.pastBytes(info.Size())}, nil
	}
	f, err := fs.Open(name)
	if err != nil {
		return ignoreFile{why: unreadable(err)}, nil
	}
	defer func() { _ = f.Close() }()
	content, err := io.ReadAll(io.LimitReader(f, r.bytes+1))
	if err != nil {
		return ignoreFile{why: unreadable(err)}, nil
	}
	if int64(len(content)) > r.bytes {
		return ignoreFile{why: r.pastBytes(int64(len(content)))}, nil
	}
	patterns := parseIgnore(content)
	if n := len(patterns); n > r.patterns {
		why := fmt.Sprintf("%s, past the %s one status applies", format.CountOf(n, "pattern"),
			format.CountOf(maxIgnorePatterns, "pattern"))
		if n <= maxIgnorePatterns {
			why = fmt.Sprintf("%s, which with those read before it are past the %s one status applies",
				format.CountOf(n, "pattern"), format.CountOf(maxIgnorePatterns, "pattern"))
		}
		return ignoreFile{why: why}, nil
	}
	r.bytes -= int64(len(content))
	r.patterns -= len(patterns)
	return ignoreFile{patterns: patterns, reach: base}, nil
}

// dir is dir, a directory of the working tree with its trailing slash, ""
// for the root, as git's rules decide it, each decided once for the status.
//
// **As git's prep_exclude decides it.** A directory is matched, as a
// directory, against the ignore files that reach it, and where one excludes
// it, everything under it is excluded: its own .gitignore is never read, and
// no pattern brings back a file in it, `!keep` included, as git's own
// documentation says of a file under a directory it excludes. Otherwise its
// .gitignore is read, and reaches what is in it.
func (r *ignoresRead) dir(dir string) *ignoreDir {
	if d, ok := r.dirs[dir]; ok {
		return d
	}
	d := &ignoreDir{}
	if dir == "" {
		// In the order git reads them, and consulted last first: the
		// .gitignore files, then info/exclude, then the excludes file.
		d.chain = r.decideRoot()
	} else {
		path := strings.TrimSuffix(dir, "/")
		parentDir, name := pathpkg.Split(path)
		parent := r.dir(parentDir)
		switch {
		case parent.excluded, excludes(lastMatch(parent.chain, path, name, true, r.fold, r.budget)):
			d.excluded = true
		default:
			d.chain = parent.chain[:len(parent.chain):len(parent.chain)]
		}
	}
	if !d.excluded {
		if file, ok := r.gitignore(dir); ok && file.why == "" {
			d.chain = append(d.chain, file)
		}
	}
	r.dirs[dir] = d
	return d
}

// ignored reports whether git ignores path, an untracked file of the working
// tree with forward slashes: one under a directory git excludes, or one the
// last pattern to match it in the files that reach it excludes.
func (r *ignoresRead) ignored(path string) bool {
	dir, name := pathpkg.Split(path)
	d := r.dir(dir)
	return d.excluded || excludes(lastMatch(d.chain, path, name, false, r.fold, r.budget))
}

// dropIgnored takes out of status each untracked path git ignores, in path
// order, so that the files read against the bounds are the same on every
// call. It stops where the call's budget runs out, which the status is then
// refused for.
func (r *ignoresRead) dropIgnored(status git.Status) {
	var untracked []string
	for path, fs := range status {
		if fs.Worktree == git.Untracked {
			untracked = append(untracked, path)
		}
	}
	sort.Strings(untracked)
	r.budget.untracked = len(untracked)
	for _, path := range untracked {
		ignored := r.ignored(path)
		if r.budget.past() {
			return
		}
		r.budget.matched++
		if ignored {
			delete(status, path)
		}
	}
}

// excludes reports whether a pattern that matched, nil for none, excludes.
func excludes(p *ignorePattern) bool { return p != nil && !p.negative }

// lastMatch is the pattern that decides path, whose last part is name, in
// chain, as git's last_matching_pattern_from_lists finds it: the files from
// the last to the first, each file's patterns from its last to its first,
// and the first to match; nil where none does, or where the call's budget
// runs out first.
//
// **The cost is the paths times the patterns, and git's is too.** Ten
// thousand patterns of twenty stars each, well within the bounds, over ten
// thousand untracked files cost git 22 s and this 30 s of matching; each
// pattern tried is counted against the budget, as each step of matching one
// is (wildmatch), and the matching stops where it runs out.
func lastMatch(chain []ignoreFile, path, name string, isDir, fold bool, b *statusBudget) *ignorePattern {
	for i := len(chain) - 1; i >= 0; i-- {
		file := chain[i]
		for j := len(file.patterns) - 1; j >= 0; j-- {
			if b.tick() {
				return nil
			}
			p := &file.patterns[j]
			if p.dirOnly && !isDir {
				continue
			}
			if p.basename && p.matchesName(name, fold, b) || !p.basename && p.matchesPath(path, file.reach, fold, b) {
				return p
			}
		}
	}
	return nil
}

// ignorePattern is one line of an ignore file, as git's parse_path_pattern
// reads it: its ! and its trailing / taken off and said (negative, dirOnly),
// whether it holds no slash, and so matches a name alone (basename), how
// many bytes of it come before its first wildcard (literal), and whether it
// is a * and then no wildcard (endsWith), which git matches by the tail.
type ignorePattern struct {
	text                                  string
	literal                               int
	negative, dirOnly, basename, endsWith bool
}

// parseIgnore is the patterns of content, an ignore file, as git's
// add_patterns_from_buffer reads it: a UTF-8 byte order mark before it
// skipped, a line empty or starting with # skipped, one CR before each line
// feed taken off, each line ended at a NUL in it, as git reads it as a C
// string, and the spaces after its last character taken off where no
// backslash escapes the first. A line that leaves nothing to match matches
// no path, and is not kept.
//
// **go-git read the bytes as they were, and git does not.** git skips the
// byte order mark some editors write, where go-git kept it as part of the
// first pattern, and ends a line at a NUL, where go-git kept the rest
// as part of the pattern, so neither matched: `.env` after either was a file
// git ignores that git.status listed and git.diff showed whole. And go-git's
// reader stopped at a line longer than 64 KiB and dropped every pattern after
// it, where git reads on: this reads on too.
func parseIgnore(content []byte) []ignorePattern {
	content = bytes.TrimPrefix(content, utf8BOM)
	var out []ignorePattern
	for rest := content; len(rest) > 0; {
		line, after, _ := bytes.Cut(rest, []byte{'\n'})
		rest = after
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if nul := bytes.IndexByte(line, 0); nul >= 0 {
			line = line[:nul]
		}
		if p := parsePattern(trimTrailingSpaces(string(line))); p.text != "" {
			out = append(out, p)
		}
	}
	return out
}

// utf8BOM is the byte order mark an editor may write before UTF-8 text.
var utf8BOM = []byte(string(rune(0xFEFF)))

// trimTrailingSpaces is line without the spaces that end it, as git's
// trim_trailing_spaces takes them off: spaces alone, not tabs, and from the
// first of them that no backslash escapes, the escaped one kept.
func trimTrailingSpaces(line string) string {
	last := -1
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			if last < 0 {
				last = i
			}
		case '\\':
			if i++; i == len(line) {
				return line
			}
			last = -1
		default:
			last = -1
		}
	}
	if last >= 0 {
		return line[:last]
	}
	return line
}

// parsePattern is line as git's parse_path_pattern reads a pattern.
func parsePattern(line string) ignorePattern {
	var p ignorePattern
	if p.negative = strings.HasPrefix(line, "!"); p.negative {
		line = line[1:]
	}
	literal := simpleLength(line)
	p.endsWith = strings.HasPrefix(line, "*") && simpleLength(line[1:]) == len(line)-1
	if p.dirOnly = strings.HasSuffix(line, "/"); p.dirOnly {
		line = line[:len(line)-1]
	}
	p.text, p.literal, p.basename = line, min(literal, len(line)), !strings.Contains(line, "/")
	return p
}

// simpleLength is how many bytes of s come before its first wildcard, git's
// simple_length.
func simpleLength(s string) int {
	for i := 0; i < len(s); i++ {
		if isGlobSpecial(s[i]) {
			return i
		}
	}
	return len(s)
}

// matchesName is git's match_basename: whether a pattern that holds no slash
// matches name, the last part of a path.
func (p *ignorePattern) matchesName(name string, fold bool, b *statusBudget) bool {
	switch {
	case p.literal == len(p.text):
		return samePath(p.text, name, fold)
	case p.endsWith:
		tail := p.text[1:]
		return len(tail) <= len(name) && samePath(tail, name[len(name)-len(tail):], fold)
	}
	return wildmatch(p.text, name, foldFlag(fold), b)
}

// matchesPath is git's match_pathname: whether a pattern that holds a slash
// matches path, taken from base, the directory of the file it is in with
// its trailing slash, as a pattern holding a slash is.
func (p *ignorePattern) matchesPath(path, base string, fold bool, b *statusBudget) bool {
	pattern, literal := p.text, p.literal
	if strings.HasPrefix(pattern, "/") {
		pattern, literal = pattern[1:], max(literal-1, 0)
	}
	if !hasPathPrefix(path, base, fold) {
		return false
	}
	name := path[len(base):]
	if literal > 0 {
		if literal > len(name) || !samePath(pattern[:literal], name[:literal], fold) {
			return false
		}
		pattern, name = pattern[literal:], name[literal:]
		if pattern == "" && name == "" {
			return true
		}
	}
	return wildmatch(pattern, name, wmPathname|foldFlag(fold), b)
}

// hasPathPrefix reports whether path lies under base, a directory with its
// trailing slash, "" for the root, which every path does.
func hasPathPrefix(path, base string, fold bool) bool {
	return len(path) > len(base) && samePath(path[:len(base)], base, fold)
}

// samePath is git's fspathcmp: two paths equal, or, where fold is set, equal
// but for the case of an ASCII letter, as strcasecmp compares them.
func samePath(a, b string, fold bool) bool {
	if !fold {
		return a == b
	}
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

// foldFlag is wmCasefold where fold is set, as git matches with
// core.ignorecase.
func foldFlag(fold bool) wmFlags {
	if fold {
		return wmCasefold
	}
	return 0
}

// pastBytes is why an ignore file of size bytes is not read.
func (r *ignoresRead) pastBytes(size int64) string {
	if size > maxIgnoreBytes {
		return fmt.Sprintf("%s, past the %s of ignore files one status reads", format.Bytes(size), format.Bytes(maxIgnoreBytes))
	}
	return fmt.Sprintf("%s, which with those read before it is past the %s of ignore files one status reads",
		format.Bytes(size), format.Bytes(maxIgnoreBytes))
}

// readIgnoreFile is an ignore file as go-git reads it, the content this hands
// it (statusFiles).
type readIgnoreFile struct {
	*bytes.Reader
	name string
}

func (f *readIgnoreFile) Name() string              { return f.name }
func (f *readIgnoreFile) Close() error              { return nil }
func (f *readIgnoreFile) Lock() error               { return nil }
func (f *readIgnoreFile) Unlock() error             { return nil }
func (f *readIgnoreFile) Write([]byte) (int, error) { return 0, errReadOnly }
func (f *readIgnoreFile) Truncate(int64) error      { return errReadOnly }

// restoreRootIgnore marks unmodified the working tree's own .gitignore where
// the status marked it modified and what is on disk is what the index
// records: the status's comparison was handed `*` in its place
// (statusFiles), not shown a change. fs is the working tree, read as the
// comparison reads it, and the file is hashed only where its size is the
// index's, and only for as long as budget lasts, as the comparison's own
// reads are (budgetedFile): a tracked .gitignore of 4 GB, touched, held a
// status 2 s past its time, hashing it here.
func restoreRootIgnore(fs billy.Filesystem, idx *index.Index, status git.Status, budget *statusBudget) {
	fst, listed := status[rootIgnore]
	if !listed || fst.Worktree != git.Modified {
		return
	}
	if entry := indexLookup(idx)(rootIgnore); entry != nil && sameAsIndexed(fs, rootIgnore, entry, budget) {
		fst.Worktree = git.Unmodified
		if fst.Staging == git.Unmodified {
			delete(status, rootIgnore)
		}
	}
}

// sameAsIndexed reports whether the file at name is the content and the mode
// entry records, read for as long as budget lasts.
func sameAsIndexed(fs billy.Filesystem, name string, entry *index.Entry, budget *statusBudget) bool {
	info, err := fs.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(entry.Size) {
		return false
	}
	if mode, err := filemode.NewFromOSFileMode(info.Mode()); err != nil || mode != entry.Mode {
		return false
	}
	f, err := fs.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	h := plumbing.NewHasher(plumbing.BlobObject, info.Size())
	if _, err := io.Copy(h, io.LimitReader(budgetedFile{File: f, budget: budget}, info.Size())); err != nil {
		return false
	}
	return h.Sum() == entry.Hash
}

// notApplied is an ignore file a status did not apply, why, and the
// directory its patterns reach (ignoreFile).
type notApplied struct{ path, why, reach string }

// unapplied is the ignore files one status did not apply, in path order.
type unapplied []notApplied

// unapplied is what the status did not apply of the ignore files it read.
func (r *ignoresRead) unapplied() unapplied {
	var out unapplied
	for name, file := range r.files {
		if file.why != "" {
			out = append(out, notApplied{name, file.why, file.reach})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// sentence names the files, the first five with why, and counts the rest;
// so says what the answer carries for them.
func (u unapplied) sentence(so string) string {
	named := make([]string, 0, min(len(u), 5))
	for _, n := range u[:min(len(u), 5)] {
		named = append(named, n.path+" ("+n.why+")")
	}
	more := ""
	if len(u) > len(named) {
		more = fmt.Sprintf(" and %d more", len(u)-len(named))
	}
	return fmt.Sprintf("%s not applied, so %s: %s%s", format.CountOf(len(u), "ignore file"), so,
		strings.Join(named, ", "), more)
}

// reach is the directories the files' patterns reach, each with its
// trailing slash, and the working tree's root as "".
func (u unapplied) reach() map[string]bool {
	dirs := make(map[string]bool, len(u))
	for _, n := range u {
		dirs[n.reach] = true
	}
	return dirs
}

// mayIgnore reports whether path, in the working tree, lies where reach says
// an ignore file that was not applied reaches: a path it may have ignored.
func mayIgnore(reach map[string]bool, path string) bool {
	if reach[""] {
		return true
	}
	for i := range len(path) {
		if path[i] == '/' && reach[path[:i+1]] {
			return true
		}
	}
	return false
}

// rootExcludeSources is the files git applies to the whole of repo's working
// tree, at root, before any .gitignore, in the order it reads them
// (excludeSource). configs is the config git reads for repo (gitConfigs), or
// cerr why it could not be read, and confine the host's path gate.
//
// **A core.excludesFile the repository's own config names is put to the gate
// first.** The config is a file a caller can write inside the root, and it
// can name any file on the machine: its lines would be read as patterns, and
// which files the status then lists would say whether one of them matched.
// So it is judged as a path a caller sent, and read where the gate judged
// it, over MCP from the directory of the roots it lies under (files): read by
// name, the directory holding it swapped for a link out of the roots after
// the judgement had the status apply the patterns of the file of the same
// name at the link's far end. So is one a file the repository's config
// includes names, which is read in the repository's scope. One the
// operator's own config names, or git's default, is the operator's choice
// and read wherever it is, as git.hooks reads their core.hooksPath; nothing
// of it is shown but its name.
func rootExcludeSources(repo *git.Repository, configs []scopedConfig, cerr error, root string,
	files *repoFiles,
) []excludeSource {
	var out []excludeSource
	noValue := valuelessIn(configs, "core", "excludesFile")
	switch p, writes, why := excludesFile(configs, root); {
	case cerr != nil:
		out = append(out, excludeSource{shown: "core.excludesFile", why: "reading the config that sets it: " + cerr.Error()})
	case noValue != "":
		// git reads each setting of core.excludesFile as it comes, and one
		// with no value at all stops it before it runs ("missing value"),
		// whatever a later file sets. go-git reads it as set to nothing, no
		// file, which ignored nothing it named: what it ignores is listed,
		// and named, as a file past the bounds is.
		out = append(out, excludeSource{shown: "core.excludesFile", why: noValue +
			" sets it with no value, which git refuses to run with"})
	case why != "":
		out = append(out, excludeSource{shown: "core.excludesFile",
			why: "the file it names is not one this can tell, since " + why})
	case filepath.Clean(p) == os.DevNull:
		// `excludesFile = /dev/null` is how git is told to read no excludes
		// file, and git reads the null device as a file with nothing in it.
		// Told apart by its name, before the gate judges it: nothing is
		// looked at for a path the gate has not judged, and this one holds no
		// pattern to read.
	case p != "":
		s := excludeSource{shown: p}
		read, where, refusal := placeOf(files.req, p)
		switch {
		case where == refused, where == notRead, refusal != nil && writes:
			s.why = refusedBy(refusal)
		case where == readByName:
			fs, name := byName(p)
			s.fs, s.name = regularFiles{Filesystem: fs}, name
		default:
			s.fs, s.name, s.why = excludesBeneath(files, read)
		}
		if s.why != "" || s.fs != nil {
			out = append(out, s)
		}
	}
	if store, ok := repo.Storer.(*filesystem.Storage); ok {
		out = append(out, excludeSource{shown: ".git/info/exclude", fs: store.Filesystem(), name: filepath.Join("info", "exclude")})
	}
	return out
}

// byName is the file p as git reads it, by name wherever its links lead: a
// filesystem over the root of the machine, and p's name in it.
//
// **Not over the directory holding it.** go-billy's osfs follows a link only
// as far as the directory it was opened on, and refuses one leading out of
// it: ~/.config/git/ignore linked to the copy a dotfiles repository keeps,
// which git reads through the link, was named as unreadable and never
// applied.
func byName(p string) (billy.Filesystem, string) {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return osfs.New("/"), strings.TrimPrefix(p, "/")
}

// excludesBeneath is judged, an excludes file inside the roots, as a status
// reads it: from the directory of the roots it lies under (files), or why it
// is not; and nothing at all where it is not there, which git passes over
// (decide). A name that leads out of the roots is refused as one outside
// them is, whether or not its far end is there (openBeneath), and opened here
// to tell, since the status reads it later only to count and apply it.
func excludesBeneath(files *repoFiles, judged string) (_ billy.Filesystem, name, why string) {
	f, ledOut, err := openBeneath(files.req, judged)
	if f != nil {
		_ = f.Close()
	}
	switch verr := refusedByTheGate(err); {
	case ledOut:
		return nil, "", refusedBy(&view.Error{Code: outsideTheRoots})
	case verr != nil:
		return nil, "", refusedBy(verr)
	case errors.Is(err, iofs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return nil, "", ""
	}
	dir, err := files.at(filepath.Dir(judged))
	switch verr := refusedByTheGate(err); {
	case verr != nil:
		return nil, "", refusedBy(verr)
	case errors.Is(err, iofs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return nil, "", ""
	case err != nil:
		return nil, "", unreadable(err)
	}
	return regularFiles{Filesystem: dir}, filepath.Base(judged), ""
}

// excludesFile is the file core.excludesFile names, as git resolves it, and
// whether a caller over MCP can write the file of config that sets it
// (scopedConfig.callerWrites): the value in the last of the files git reads that
// sets it (gitConfigs), ~ and ~user expanded and a relative one taken from
// the working tree's root; where none does, git's default,
// $XDG_CONFIG_HOME/git/ignore, or ~/.config/git/ignore with that unset. ""
// where it is set to nothing, which git reads as no file. why is what keeps
// this from telling which file the value names (configPathname).
func excludesFile(configs []scopedConfig, root string) (path string, writes bool, why string) {
	set := false
	for _, f := range configs {
		if core := f.config.Raw.Section("core"); core.HasOption("excludesFile") {
			path, writes, set = core.Option("excludesFile"), f.callerWrites(), true
		}
	}
	switch {
	case set && path == "":
		return "", writes, ""
	case set:
		expanded, why := configPathname(path)
		if why != "" {
			return "", writes, why
		}
		return against(root, expanded), writes, ""
	case os.Getenv("XDG_CONFIG_HOME") != "":
		return filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "git", "ignore"), false, ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, ""
	}
	return filepath.Join(home, ".config", "git", "ignore"), false, ""
}

// ignoreCase reports whether git matches ignore patterns without regard to
// case: core.ignorecase, as the last of configs that sets it has it.
//
// **go-git never did.** `git init` sets it in every repository it makes on a
// filesystem that does not tell case apart, macOS's by default, and there
// `.ENV` in a .gitignore ignores a .env: git.status listed the .env and
// git.diff showed it whole. Where it is set, a name is matched as git's
// matcher matches it with WM_CASEFOLD (wildmatch): a letter of the name in
// either case, and a letter inside a bracket expression or after a backslash
// as it is written, which in upper case matches no name at all.
func ignoreCase(configs []scopedConfig) bool {
	on := false
	for _, f := range configs {
		if core := f.config.Raw.Section("core"); core.HasOption("ignorecase") {
			on = gitBool(core.Option("ignorecase"))
		}
	}
	return on
}
