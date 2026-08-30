package audit

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/helper/iofs"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/gitclone"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, view.Errorf("audit.deps.nopath", "no such path: %s", target).
				WithHint("pass the directory holding the lockfile or SBOM, the file itself, " +
					"or a repository URL")
		}
		return nil, view.Errorf("audit.deps.path", "reading %s: %v", target, err)
	}
	if !info.IsDir() {
		// A single file: read its directory and look at exactly one name, so
		// the fs.FS the parsers see is the same shape either way.
		dir, base := filepath.Dir(target), filepath.Base(target)
		return &project{
			fsys:  os.DirFS(dir),
			shown: func(string) string { return target },
			only:  base,
		}, nil
	}
	return &project{
		fsys: os.DirFS(target),
		// Rebuilt from the path that was typed, so a relative --path stays
		// relative in the output and an absolute one stays absolute — what
		// the reader sees is what they could paste back.
		shown: func(p string) string { return filepath.Join(target, filepath.FromSlash(p)) },
	}, nil
}

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
func (p *project) manifests(recursive bool) (names []string, shown []string, truncated bool, err error) {
	if p.only != "" {
		return []string{p.only}, []string{p.shown(p.only)}, false, nil
	}
	names, truncated, err = findManifests(p.fsys, recursive)
	shown = make([]string, len(names))
	for i, n := range names {
		shown[i] = p.shown(n)
	}
	return names, shown, truncated, err
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
