package pluginhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/paths"
)

// The deny set is what a confined plugin may not touch. It is small on
// purpose, and the reason is worth stating before the list: a denylist cannot
// enumerate what matters. internal/pathguard makes the opposite argument for
// MCP path arguments and reaches the opposite conclusion — an allowlist,
// because it fails closed for everything nobody thought of.
//
// This is not that situation. There, the caller supplies one path and the
// question is whether that path is in bounds. Here, a plugin is an arbitrary
// program whose whole job is to do something useful with the machine, and the
// set of paths a legitimate plugin needs is "most of them" — a `pg` plugin
// reads ~/.pgpass, a `kube` plugin reads ~/.kube/config, and a docker plugin
// reads a socket. An allowlist over that is either useless or is a permission
// dialog per plugin per path, which is the model that has failed everywhere it
// has been tried.
//
// So the profile is `(allow default)` plus a deny set, and the deny set is
// scoped to two things: what rta owns, and the credentials that are not any
// plugin's business. ADR 0012 says plainly what that does not buy — the honest
// headline is that every attack found during the pre-M2 review succeeds
// identically on a fully confined macOS host. This raises the floor; it is not
// a boundary.

// tier1 is rta's own state: denied for both reading and writing.
//
// The second verb is the one that took an exploit to find. A first pass denied
// reads only, on the reasoning that these directories hold secrets — and left
// the one member whose dangerous operation is a *write* wide open. A confined
// plugin could not read grants.json and did not need to: the Grant struct is
// public, so an 82-byte blind overwrite handed it a standing grant over every
// kv capability. Sealing the grant file (ADR 0015) closed that specific route;
// denying the write closes the shape.
func tier1() []string {
	return dedupe([]string{
		paths.Data(),
		filepath.Dir(config.Path()),
	})
}

// tier2 is the standard credential locations: denied for reading only.
//
// Read-only because denying writes here would break honest tools and protect
// nothing rta owns — a plugin that legitimately runs `ssh-keygen` or `aws
// configure` on the user's behalf is doing what it was installed to do. The
// list is the well-known set and is deliberately not exhaustive; the `.env`
// beside somebody's docker-compose.yml is exactly what a denylist misses, and
// pretending otherwise is what makes people trust it further than it goes.
func tier2() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, rel := range []string{
		".ssh",
		".aws",
		".kube",
		".gnupg",
		".docker/config.json",
		".config/gcloud",
		".netrc",
		".npmrc",
		".pypirc",
		".git-credentials",
	} {
		out = append(out, filepath.Join(home, rel))
	}
	return dedupe(out)
}

// DenySet is the resolved policy for this machine, so that a caller — `rta
// doctor`, a test, the profile builder — reads one value rather than
// recomputing it three ways.
type DenySet struct {
	// NoAccess is denied for reading and writing; NoRead for reading only.
	// Each holds every path plus, where a path is a symlink, its target.
	NoAccess []string
	NoRead   []string
}

// Resolve builds the deny set for this machine.
//
// Every entry is expanded through symlinks with rules emitted for *both* the
// link and its target. A denied path that is itself a symlink is otherwise
// bypassed in two syscalls, and that is not exotic: `~/.aws → ~/dotfiles/aws`
// is what home-manager, chezmoi and stow all produce, so the machines most
// likely to be running this are the ones where the naive version does nothing.
//
// A path that does not exist is kept rather than dropped. It may be created
// while the plugin runs, and a rule naming a path that never appears costs
// nothing.
func Resolve() (DenySet, error) {
	var d DenySet
	for _, p := range tier1() {
		d.NoAccess = append(d.NoAccess, withTarget(p)...)
	}
	for _, p := range tier2() {
		d.NoRead = append(d.NoRead, withTarget(p)...)
	}
	d.NoAccess, d.NoRead = dedupe(d.NoAccess), dedupe(d.NoRead)
	if err := validate(append(append([]string{}, d.NoAccess...), d.NoRead...)); err != nil {
		return DenySet{}, err
	}
	return d, nil
}

// withTarget returns p and, if p is a symlink, what it resolves to.
func withTarget(p string) []string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return []string{p}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || resolved == abs {
		return []string{abs}
	}
	return []string{abs, resolved}
}

// validate refuses any path that could break out of the quoting in a
// generated policy.
//
// macOS needs a path validator that Linux does not, and the reason is that
// macOS generates policy as *text*. SBPL is last-match-wins, so an entry like
//
//	/tmp/x")) (allow file-read* (subpath "/
//
// closes the deny form and appends an allow-everything rule that overrides
// every tier above it — turning the confinement into strictly less than no
// confinement, since it would also be reported as enabled. The check runs on
// every platform even though only macOS renders text, because a path that
// cannot be safely quoted is a path worth refusing everywhere, and a check
// that only compiles on one GOOS is a check nobody runs.
//
// It fails the build of the command loudly rather than dropping the entry:
// silently omitting a rule is how a deny set shrinks to nothing without
// anybody noticing.
func validate(entries []string) error {
	const forbidden = "\"\\\n\r()"
	for _, e := range entries {
		if i := strings.IndexAny(e, forbidden); i >= 0 {
			return fmt.Errorf("refusing to build a sandbox profile: path %q contains %q, "+
				"which cannot be safely quoted in a policy file", e, e[i:i+1])
		}
	}
	return nil
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
