package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/helper/iofs"

	"github.com/this-is-tobi/rta/builtin/internal/gitclone"
	"github.com/this-is-tobi/rta/builtin/internal/pathin"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// What a dependency audit is pointed at, resolved once.
//
// `audit deps` and `audit why` both start from the same question — which
// files does this project declare, and what do I call them back — and both
// used to answer it with os.Stat on a path. That is one of three shapes
// somebody means: a directory, a single manifest, or a repository they have
// not checked out. Resolving all three here rather than in each capability
// is what keeps them from drifting into two different ideas of what --path
// accepts.

// project is a filesystem to read manifests from, plus how to name a path
// inside it when a finding has to point at one.
type project struct {
	fsys fs.FS
	// shown renders an fs path the way the reader should see it: the path
	// they typed for a directory on this machine, and the path inside the
	// repository for a clone, where an absolute path in a memory filesystem
	// would name a file nobody can open.
	shown func(string) string
	// only is set when the caller named one file rather than a directory,
	// and it is that file — no walk, no directory listing.
	only string
}

// openProject resolves what --path names.
func openProject(ctx context.Context, req plugin.Request, target string) (*project, *view.Error) {
	if gitclone.IsRemote(target) {
		return cloneProject(ctx, req, target)
	}
	info, err := pathin.Stat(req, target)
	var refused *view.Error
	switch {
	case errors.As(err, &refused):
		// The bounds' own refusal, which says what it is better than a
		// sentence about reading could.
		return nil, refused
	case err != nil:
		if errors.Is(err, fs.ErrNotExist) {
			return nil, view.Errorf("audit.deps.nopath", "no such path: %s", target).
				WithHint("pass the directory holding the lockfile or SBOM, the file itself, " +
					"or a repository URL")
		}
		return nil, view.Errorf("audit.deps.path", "reading %s: %v", target, err)
	case !info.IsDir():
		return namedProject(req, target)
	}
	return &project{
		fsys: pathin.FS(req, target),
		// Rebuilt from the path that was typed, so a relative --path stays
		// relative in the output and an absolute one stays absolute — what
		// the reader sees is what they could paste back.
		shown: func(p string) string { return filepath.Join(target, filepath.FromSlash(p)) },
	}, nil
}

// maxManifestBytes is more than any manifest this reads: a package-lock.json
// for a few thousand packages is a few megabytes, and an SBOM listing every
// file of a large image a few tens of them. A file past it is none of those —
// a dataset or an archive under a name a scan looks for — and reading it whole
// was the server's memory spent on the caller's say-so, as pathin says of the
// files it reads.
const maxManifestBytes = 64 << 20

// namedProject is the project of a file named on its own, which is read here,
// once, as pathin reads a file its caller named: off the CLI only a regular
// file, and on every surface no more than maxManifestBytes.
//
// Read here rather than by the scan, because two things need its bytes: the
// scan, and the question of whether a JSON file is an SBOM at all. Read twice,
// a pipe named at the terminal gave the first read its contents and left the
// second waiting on a writer that had gone. A refusal of what the path names
// is the call's answer, while a file that is there and could not be read — a
// permission, say — is left to the scan, which names it as a manifest it
// could not read, as it names one it found.
func namedProject(req plugin.Request, target string) (*project, *view.Error) {
	format := manifestFormat(target)
	if format == "" {
		return nil, notAManifest(target)
	}
	named := namedFile{name: filepath.Base(target)}
	f, info, err := pathin.Open(req, target)
	if err == nil {
		named.info = info
		named.data, err = pathin.ReadAll(f, target, maxManifestBytes)
		_ = f.Close()
	}
	var notAFile *pathin.NotAFileError
	var tooLarge *pathin.TooLargeError
	switch {
	case errors.As(err, &notAFile):
		return nil, view.Errorf("audit.deps.notafile", "%v", err).
			WithHint("name the lockfile, requirements file or SBOM itself, or the directory holding it")
	case errors.As(err, &tooLarge):
		return nil, view.Errorf("audit.deps.toolarge", "%v, more than any lockfile or SBOM holds", err).
			WithHint("name the manifest itself, or the directory holding it")
	case err != nil:
		named.err = err
	}
	// A JSON file is an SBOM by what is inside it, so that is what is looked
	// at; one that does not parse at all is left to the scan, which says why.
	if named.err == nil && strings.HasSuffix(format, ".json") && format != "package-lock.json" {
		var marks sbomMarks
		if json.Unmarshal(named.data, &marks) == nil && !marks.isSBOM() {
			return nil, notAManifest(target)
		}
	}
	return &project{fsys: named, shown: func(string) string { return target }, only: named.name}, nil
}

// notAManifest refuses a file named on its own that is no manifest this
// reads.
//
// A directory scan picks up only the names it looks for; a file named on its
// own can be anything, and one this does not read was listed as a manifest
// with no pinned dependencies in it — notes.txt as much as a JSON config —
// with the report blaming a requirements file's ranges. That is a finding
// about a file nobody read.
func notAManifest(target string) *view.Error {
	return view.Errorf("audit.deps.format", "%s is not a lockfile, a requirements file or an SBOM", target).
		WithHint("the formats read are " + strings.Join(ecosystems, "; "))
}

// whyUnread says why a file could not be read, without the path the sentence
// around it already names: what pathin found in place of a file, or past what
// size it stopped, and any other error as it reads.
func whyUnread(err error) string {
	var notAFile *pathin.NotAFileError
	var tooLarge *pathin.TooLargeError
	switch {
	case errors.As(err, &notAFile):
		return "it is " + pathin.Kind(notAFile.Mode) + ", not a file"
	case errors.As(err, &tooLarge):
		return "it is larger than " + format.Bytes(tooLarge.Max)
	}
	return err.Error()
}

// namedFile is the fs.FS of a file named on its own: the one name the scan
// asks for, and what namedProject read from it, or why it could not.
type namedFile struct {
	name string
	info fs.FileInfo
	data []byte
	err  error
}

func (n namedFile) Open(name string) (fs.File, error) {
	switch {
	case name != n.name:
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	case n.err != nil:
		return nil, n.err
	}
	return openNamed{Reader: bytes.NewReader(n.data), info: n.info}, nil
}

type openNamed struct {
	*bytes.Reader
	info fs.FileInfo
}

func (o openNamed) Stat() (fs.FileInfo, error) { return o.info, nil }
func (openNamed) Close() error                 { return nil }

// cloneProject reads a repository nobody checked out.
//
// Shallow and single-branch: the question is what the tip declares, and
// asking for a decade of history to answer it is memory spent on nothing.
// It is not a size bound — the whole tree still arrives at once, which is
// the honest cost of reading a repository without a working copy — and the
// clone timeout is the only other thing between this and a very large
// stranger.
func cloneProject(ctx context.Context, req plugin.Request, url string) (*project, *view.Error) {
	if verr := gitclone.RefuseOverMCP(req, "repository"); verr != nil {
		return nil, verr
	}
	repo, verr := gitclone.InMemory(ctx, url, gitclone.Options{ShallowSingleBranch: true})
	if verr != nil {
		return nil, verr
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, view.Errorf("audit.deps.clone", "reading the cloned repository: %v", err)
	}
	// The path inside the repository is the whole name a finding can carry:
	// "go.mod" from a clone is unambiguous next to the URL in the header, and
	// "/go.mod" would look like a file on this machine.
	return &project{fsys: iofs.New(wt.Filesystem), shown: func(p string) string { return p }}, nil
}

// manifests lists what this project declares, and how to name each one.
func (p *project) manifests(recursive bool) (names []string, shown []string, cov coverage, err error) {
	if p.only != "" {
		return []string{p.only}, []string{p.shown(p.only)}, coverage{}, nil
	}
	names, cov, err = findManifests(p.fsys, recursive)
	shown = make([]string, len(names))
	for i, n := range names {
		shown[i] = p.shown(n)
	}
	// Named the way the reader can act on: a path inside a clone means
	// nothing to them, the same reason the manifests themselves are shown
	// rather than passed through.
	for i, u := range cov.unreadable {
		cov.unreadable[i] = p.shown(u)
	}
	return names, shown, cov, err
}

// pathHelp is the --path help both capabilities share, so the two never
// describe the same input differently.
func pathHelp(what string) string {
	return what + " — or, from a terminal, a repository URL (https://, ssh://, " +
		"git@host:path) read in memory; not over MCP"
}

// remoteLabel is how a report names where it read from, for the header line.
// A URL is shown as given; a directory is shown as typed.
func remoteLabel(target string) string {
	if gitclone.IsRemote(target) {
		return strings.TrimSuffix(target, ".git")
	}
	return path.Clean(filepath.ToSlash(target))
}
