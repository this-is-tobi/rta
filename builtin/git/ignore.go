package git

import (
	"bufio"
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
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// maxIgnoreBytes and maxIgnorePatterns are what one status reads of the
// working tree's ignore files, and the patterns it applies from them, in all.
//
// **go-git reads every ignore file whole and keeps every pattern, and nothing
// bounded either.** Its status reads the .gitignore of each directory it
// does not already ignore, then matches each directory it walks, and each
// change it found, against every pattern read so far: the cost is the tree
// times the patterns, from files a caller can write or a commit carry. A planted .gitignore of a hundred thousand patterns that
// match nothing cost a git.status of a thousand untracked directories 15 s
// of CPU, where git takes 2.6 s, and one of a million 154 s and 200 MB; git's
// own limit, 100 MB, is room for ten million.
//
// Measured against large real repositories rather than guessed: Chromium's
// 285 ignore files hold 2354 patterns in 54 KiB, the largest 9.7 KiB, and
// Linux's 405 hold about 1800 in 38 KiB. One status reads 1 MiB and applies
// 10000 patterns, about twenty and four times those, in the order go-git
// reads the files. An ignore file that would take it past either is not
// applied at all rather than in part, as git applies none of a pattern file
// past 100 MB and warns, and the answer says so (unapplied): what the file
// ignores is then listed as untracked, which git would not list. Variables so
// a test can lower them.
var (
	maxIgnoreBytes    int64 = 1 << 20
	maxIgnorePatterns       = 10000
)

// ignoreFiles is a working tree as its status reads it, whose ignore files
// are held to what one status reads and applies (maxIgnoreBytes,
// maxIgnorePatterns). Each is read here once, and what go-git reads of it is
// what this read and counted.
//
// **One open serves go-git's patterns and its comparison.** The status reads
// a tracked file again, to hash it, when its timestamp no longer matches the
// index's, through the same filesystem: a file refused here is refused to
// both, the comparison hashes nothing, and calls it modified. ignoresRead's
// restore puts that right from the disk.
type ignoreFiles struct {
	billy.Filesystem
	read *ignoresRead
}

// ignoresRead is what one status has read of its ignore files: the bytes and
// the patterns it has left, each file it has decided on, by its path in the
// working tree or as the answer names it, the files it reads ahead of the
// working tree's own .gitignore (excludeSource), and whether it served that
// .gitignore with anything ahead of it.
type ignoresRead struct {
	bytes    int64
	patterns int
	files    map[string]ignoreFile
	root     []excludeSource
	prefixed bool
}

// ignoreFile is one ignore file as a status decided on it: the content it
// applies, or why it does not apply it; and the directory its patterns reach,
// with its trailing slash, "" for the whole working tree.
type ignoreFile struct {
	content []byte
	why     string
	reach   string
}

func newIgnoresRead(root []excludeSource) *ignoresRead {
	return &ignoresRead{bytes: maxIgnoreBytes, patterns: maxIgnorePatterns, files: map[string]ignoreFile{}, root: root}
}

// rootIgnore is the working tree's own .gitignore, the first ignore file
// go-git reads, whose patterns reach the whole working tree, and the one it
// is handed the root sources ahead of.
const rootIgnore = ".gitignore"

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
// read here, the excludes file first, and handed to go-git ahead of the
// working tree's own .gitignore, the first ignore file it reads, where they
// reach every path and everything read after them wins over them, as in git.
type excludeSource struct {
	shown string
	fs    billy.Filesystem
	name  string
	why   string
}

// decideRoot decides each root source this has not, one that is not there
// as one that holds nothing.
//
// **Before any ignore file, whichever go-git opens first.** git reads the
// root sources first, and they are held to the bounds first: go-git opens
// the working tree's .gitignore before it reads them, and deciding them only
// then let a .gitignore of 10000 patterns take the whole bound and leave the
// operator's own excludes file not applied.
func (r *ignoresRead) decideRoot() {
	for _, s := range r.root {
		if _, decided := r.files[s.shown]; decided {
			continue
		}
		file := ignoreFile{why: s.why}
		if s.why == "" {
			var err error
			if file, err = r.decide(s.fs, s.name); err != nil {
				file = ignoreFile{}
			}
		}
		r.files[s.shown] = file
	}
}

// rootExcludes is what the root sources this applies hold, one after the
// other.
func (r *ignoresRead) rootExcludes() []byte {
	r.decideRoot()
	var content []byte
	for _, s := range r.root {
		if file := r.files[s.shown]; file.why == "" && len(file.content) > 0 {
			content = append(append(content, file.content...), '\n')
		}
	}
	return content
}

// ignoreFileName reports whether go-git's status reads name, a path in the
// working tree with forward slashes, for patterns: a .gitignore anywhere. It
// looks for a .git/info/exclude in each directory too, and its own working
// tree filesystem refuses the path, which goes through a .git.
func ignoreFileName(name string) bool {
	return pathpkg.Base(name) == ".gitignore"
}

func (f ignoreFiles) Open(name string) (billy.File, error) {
	return f.OpenFile(name, os.O_RDONLY, 0)
}

func (f ignoreFiles) OpenFile(name string, flag int, perm os.FileMode) (billy.File, error) {
	key := filepath.ToSlash(filepath.Clean(name))
	if flag != os.O_RDONLY || !ignoreFileName(key) {
		return f.Filesystem.OpenFile(name, flag, perm)
	}
	f.read.decideRoot()
	file, decided := f.read.files[key]
	var missing error
	if !decided {
		file, missing = f.read.decide(f.Filesystem, name)
		if missing == nil {
			file.reach = strings.TrimSuffix(key, ".gitignore")
			f.read.files[key] = file
		}
	}
	var ahead []byte
	if key == rootIgnore {
		ahead = f.read.rootExcludes()
		f.read.prefixed = f.read.prefixed || len(ahead) > 0
	}
	switch {
	case len(ahead) == 0 && missing != nil:
		return nil, missing
	case len(ahead) == 0 && file.why != "":
		return nil, &iofs.PathError{Op: "open", Path: name, Err: errors.New(file.why)}
	case file.why == "":
		ahead = append(ahead, file.content...)
	}
	return &readIgnoreFile{Reader: bytes.NewReader(ahead), name: name}, nil
}

// decide reads the ignore file at name, when what is left of the bounds holds
// it, and counts its patterns against them; a file they do not hold is not
// read. An error is for a file that is not there, which go-git passes over.
//
// One that is there and cannot be read is not applied, and named, as git
// names one it cannot read; go-git passed over it without a word, and so did
// this: a .gitignore that is a directory, or that the user may not read.
func (r *ignoresRead) decide(fs billy.Filesystem, name string) (ignoreFile, error) {
	info, err := fs.Stat(name)
	switch {
	case errors.Is(err, iofs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return ignoreFile{}, err
	case err != nil:
		return ignoreFile{why: unreadable(err)}, nil
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
	n := patternCount(content)
	if n > r.patterns {
		why := fmt.Sprintf("%s, past the %s one status applies", format.CountOf(n, "pattern"),
			format.CountOf(maxIgnorePatterns, "pattern"))
		if n <= maxIgnorePatterns {
			why = fmt.Sprintf("%s, which with those read before it are past the %s one status applies",
				format.CountOf(n, "pattern"), format.CountOf(maxIgnorePatterns, "pattern"))
		}
		return ignoreFile{why: why}, nil
	}
	r.bytes -= int64(len(content))
	r.patterns -= n
	return ignoreFile{content: content}, nil
}

// pastBytes is why an ignore file of size bytes is not read.
func (r *ignoresRead) pastBytes(size int64) string {
	if size > maxIgnoreBytes {
		return fmt.Sprintf("%s, past the %s of ignore files one status reads", format.Bytes(size), format.Bytes(maxIgnoreBytes))
	}
	return fmt.Sprintf("%s, which with those read before it is past the %s of ignore files one status reads",
		format.Bytes(size), format.Bytes(maxIgnoreBytes))
}

// patternCount is how many patterns go-git keeps of content: a line that is
// neither blank nor a # comment, read as its reader reads lines, which stops
// at one longer than it takes.
func patternCount(content []byte) int {
	n := 0
	lines := bufio.NewScanner(bytes.NewReader(content))
	for lines.Scan() {
		if line := lines.Text(); !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// readIgnoreFile is an ignore file as go-git reads it, the content this read.
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

// restore marks unmodified each tracked ignore file this served otherwise
// than it is that the status marked modified, where what is on disk is what
// the index records: the status's comparison was refused the file's content,
// or handed the root sources ahead of it (ignoreFiles), not shown a change.
// fs is the working tree, read as the comparison reads it, and the file is
// hashed only where its size is the index's.
func (r *ignoresRead) restore(fs billy.Filesystem, idx *index.Index, status git.Status) {
	entryOf := indexLookup(idx)
	for name, file := range r.files {
		fst, listed := status[name]
		served := file.why != "" || name == rootIgnore && r.prefixed
		if !served || !listed || fst.Worktree != git.Modified {
			continue
		}
		if entry := entryOf(name); entry != nil && sameAsIndexed(fs, name, entry) {
			fst.Worktree = git.Unmodified
			if fst.Staging == git.Unmodified {
				delete(status, name)
			}
		}
	}
}

// sameAsIndexed reports whether the file at name is the content and the mode
// entry records.
func sameAsIndexed(fs billy.Filesystem, name string, entry *index.Entry) bool {
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
	if _, err := io.Copy(h, io.LimitReader(f, info.Size())); err != nil {
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
// (excludeSource). confine is the host's path gate.
//
// **A core.excludesFile the repository's own config names is put to the gate
// first.** The config is a file a caller can write inside the root, and it
// can name any file on the machine: its lines would be read as patterns, and
// which files the status then lists would say whether one of them matched.
// So it is judged as a path a caller sent, and read where the gate judged
// it. One the operator's own config names, or git's default, is the
// operator's choice and read wherever it is, as git.hooks reads their
// core.hooksPath; nothing of it is shown but its name.
func rootExcludeSources(repo *git.Repository, root string, confine func(string) (string, *view.Error)) []excludeSource {
	var out []excludeSource
	switch p, scope, err := excludesFile(repo, root); {
	case err != nil:
		out = append(out, excludeSource{shown: "core.excludesFile", why: "reading the config that sets it: " + err.Error()})
	case p != "":
		s := excludeSource{shown: p}
		if scope == "local" || scope == "worktree" {
			judged, verr := confine(p)
			if verr != nil {
				s.why = refusedBy(verr)
			}
			p = judged
		}
		if s.why == "" {
			s.fs, s.name = regularFiles{Filesystem: osfs.New(filepath.Dir(p))}, filepath.Base(p)
		}
		out = append(out, s)
	}
	if store, ok := repo.Storer.(*filesystem.Storage); ok {
		out = append(out, excludeSource{shown: ".git/info/exclude", fs: store.Filesystem(), name: filepath.Join("info", "exclude")})
	}
	return out
}

// excludesFile is the file core.excludesFile names, as git resolves it, and
// the scope it is set in: the value in the last of the files git reads that
// sets it (gitConfigs), ~ expanded and a relative one taken from the working
// tree's root; where none does, git's default, $XDG_CONFIG_HOME/git/ignore,
// or ~/.config/git/ignore with that unset. "" where it is set to nothing,
// which git reads as no file. A file one of the configs includes is not
// followed, as git.config follows none, and could set it.
func excludesFile(repo *git.Repository, root string) (path, scope string, err error) {
	files, err := gitConfigs(repo)
	if err != nil {
		return "", "", err
	}
	for _, f := range files {
		if core := f.config.Raw.Section("core"); core.HasOption("excludesFile") {
			path, scope = core.Option("excludesFile"), f.scope
		}
	}
	switch {
	case scope != "" && path == "":
		return "", scope, nil
	case scope != "":
		return against(root, plugin.ExpandHome(path)), scope, nil
	case os.Getenv("XDG_CONFIG_HOME") != "":
		return filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "git", "ignore"), "default", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", nil //nolint:nilerr // no home directory is no default file, as git finds none
	}
	return filepath.Join(home, ".config", "git", "ignore"), "default", nil
}
