// Package gitclone reads a repository somebody named by URL, and draws the
// one line that reading a stranger's repository has to be drawn on.
//
// It exists because two plugins ask the same question. builtin/git accepts a
// remote URL wherever it accepts a path, and builtin/audit reads the manifests
// out of one to answer "what does this project depend on" without a checkout.
// Two implementations of "is this a URL", "how long may a clone take" and
// "who is allowed to ask" is how the two come to disagree — and the third of
// those is a security boundary, which is the one that must not drift.
package gitclone

import (
	"context"
	"fmt"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Timeout bounds one clone. Not an input anybody sets: the difference between
// "the URL is wrong" and "the network is slow" is not a distinction worth a
// person deciding per call, and a generous fixed budget covers both without
// asking.
const Timeout = 60 * time.Second

// maxObjectBytes bounds how much object data one InMemory clone may store —
// Timeout's reasoning applied to space instead of duration. A clone that
// never returns costs one call; a clone that returns having filled every
// byte of memory on the machine costs the process, and everything else
// running in it. Generous rather than tight, for the same reason Timeout
// is: this exists to catch a repository sized like a bomb, not to second-
// guess one sized like a large monorepo's actual history.
//
// Not an input anybody sets, and not scaled down for ShallowSingleBranch
// either — a single commit's tree is still the whole tree at that commit,
// which for a monorepo with large files in it is not automatically small.
//
// A var, not a const, so a test can shrink it rather than clone a fixture
// sized in gigabytes to prove the cap fires.
var maxObjectBytes int64 = 2 << 30 // 2 GiB

// cappedStorage wraps a storage.Storer and refuses once the total size of
// every object stored through it would exceed maxObjectBytes.
//
// SetEncodedObject is the interception point rather than anything at the
// transport or pack-parsing layer: whatever the protocol details, every
// commit, tree, blob and tag a clone brings back is written exactly once
// through this one method on its way into memory — so this is the one
// place a cap here covers the whole object graph regardless of how it
// arrived, without having to reason about pack framing or delta
// resolution at all.
//
// remaining is a pointer, not a plain field, so Module (submodule storage)
// can share it: RecurseSubmodules is off by default and neither of
// gitclone's two callers turns it on today, but a submodule's objects
// still flow through the identical SetEncodedObject path the moment one
// does, and a fresh, independent budget per submodule would turn one cap
// into as many as the remote cares to declare.
type cappedStorage struct {
	storage.Storer
	remaining *int64
}

func newCappedStorage() *cappedStorage {
	remaining := int64(maxObjectBytes)
	return &cappedStorage{Storer: memory.NewStorage(), remaining: &remaining}
}

func (s *cappedStorage) SetEncodedObject(o plumbing.EncodedObject) (plumbing.Hash, error) {
	if o.Size() > *s.remaining {
		return plumbing.ZeroHash, fmt.Errorf(
			"remote repository exceeds the %s object size limit", format.Bytes(uint64(maxObjectBytes)))
	}
	*s.remaining -= o.Size()
	return s.Storer.SetEncodedObject(o)
}

func (s *cappedStorage) Module(name string) (storage.Storer, error) {
	inner, err := s.Storer.Module(name)
	if err != nil {
		return nil, err
	}
	return &cappedStorage{Storer: inner, remaining: s.remaining}, nil
}

// IsRemote reports whether path names a repository somewhere else.
//
// go-git's own endpoint parser answers it, rather than a prefix check:
// transport.NewEndpoint classifies a bare local path as protocol "file" and
// anything else (https://, ssh://, an scp-like git@host:path) as remote,
// which is the classification the git binary itself would reach for the same
// string. So "does this look like a URL" is never answered twice, once here
// and once wrong.
func IsRemote(path string) bool {
	ep, err := transport.NewEndpoint(path)
	return err == nil && ep.Protocol != "file"
}

// Options narrows what a clone brings back.
type Options struct {
	// ShallowSingleBranch fetches one commit of one branch and no tags.
	//
	// Whether it is right depends entirely on the question. `git log` wants
	// the history and must not set it; reading the lockfiles at the tip wants
	// the tip and nothing else, and asking for a decade of history to answer
	// that is a cost paid in somebody's memory. It is not what keeps a clone
	// bounded — maxObjectBytes is the hard ceiling, sized for a whole tree
	// arriving at once whether this is set or not — but it is the largest
	// reduction the protocol offers for free before that ceiling is ever
	// reached.
	ShallowSingleBranch bool
}

// InMemory clones url with no working copy on disk.
//
// Nothing is written anywhere: a repository somebody named on a command line
// should not still be on the machine afterwards, needing cleaning up or
// waiting to be found by whoever looks in the temp directory next. The whole
// tree does arrive in memory, which is the honest cost and the reason
// ShallowSingleBranch exists — and the reason maxObjectBytes bounds it.
func InMemory(ctx context.Context, url string, opts Options) (*git.Repository, *view.Error) {
	cctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	co := &git.CloneOptions{URL: url}
	if opts.ShallowSingleBranch {
		co.Depth = 1
		co.SingleBranch = true
		co.Tags = git.NoTags
	}
	repo, err := git.CloneContext(cctx, newCappedStorage(), memfs.New(), co)
	if err != nil {
		return nil, view.Errorf("git.clone.failed", "cloning %s: %v", url, err).
			WithHint("only unauthenticated (public) URLs are supported")
	}
	return repo, nil
}

// RefuseOverMCP is the line between "this capability only reads" and "this
// capability is safe to hand an agent unauthenticated".
//
// The capabilities that reach for this are Read with no grant, which is the
// class that costs nothing to reach: read capabilities go onto every `rta mcp
// serve` with no --allow-write, no grant and read_only_hint: true. The
// catalogue already decided what may be in that class — http.get carries
// NeedsGrant with Scope "url" because a caller-chosen URL is an outbound
// channel on the way there and a model's context on the way back, and
// audit.web is gated for the same reason under a different name.
//
// A clone is that call with extra steps. It speaks first, to a path the
// caller composed, and it hands back the remote's own commit messages,
// branch names, config, file contents and dependency list — a stranger's
// text arriving in a model's context. "It only reads" was true and was never
// the question; net.probe and net.send split over the same distinction.
//
// Refused rather than granted because a grant has to be declared on the
// capability, and these capabilities are overwhelmingly local: gating
// `git status` behind consent to read the checkout you are standing in costs
// the thing agents actually use it for and buys nothing.
//
// The URL is deliberately not echoed back. A refusal is rta's own voice in a
// model's context, and the caller's text does not belong there under rta's
// name — the same reason a consent notification names the capability and
// never the agent's wording.
func RefuseOverMCP(req plugin.Request, what string) *view.Error {
	if req.Surface() != plugin.SurfaceMCP {
		return nil
	}
	return view.Refusef("git.remote.mcp", "reading a remote %s is not available over MCP", what).
		WithHint("clone it locally and pass the checkout, so the fetch is something " +
			"you did rather than something a call did")
}
