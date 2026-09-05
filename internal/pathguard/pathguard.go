// Package pathguard confines caller-supplied filesystem paths to a set of
// roots.
//
// It exists because rta's default MCP surface reads arbitrary paths, and that
// is not a bug in one capability. Measured against the shipping binary over a
// live `rta mcp serve`, with no flag and no grant: `fs_hash` returns the
// sha256 and size of any file on disk, `fs_tree` lists any directory,
// `net_resolver_list` parses any file and reports what it found in it, and
// `cert_inspect` distinguishes "exists and is readable" from "does not
// exist". Every one of those capabilities is `read` and correctly so — they
// mutate nothing — and every one of them is doing exactly its job. The
// question that matters is not whether the caller may run it but whether
// *this* caller may point it there, and until now nothing asked.
//
// So the control is not per-capability and not a safety class. It is a root,
// enforced once at the MCP boundary, in the same place and for the same
// reason grants are: a person at a terminal can already read their
// own files, and an agent with no human behind it cannot be given the same
// reach by default.
//
// An allowlist, not a denylist of secrets. A denylist has to name ~/.ssh,
// ~/.aws, ~/.kube, ~/.gnupg, the browser profiles, the cloud SDK caches and
// whatever the next tool invents — and it still misses the `.env` beside
// somebody's docker-compose.yml, which is where the credentials actually are.
// A root fails closed for everything nobody thought of, which is the only
// property worth having here.
package pathguard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Guard answers whether a caller-supplied path is in bounds.
//
// The zero Guard allows everything, which is what every non-MCP surface uses.
// Making "no guard" the zero value rather than a nil check at each call site
// keeps the CLI and TUI paths free of a concept that does not apply to them.
type Guard struct {
	roots  []string
	denied []string
}

// New builds a guard rooted at each of roots.
//
// Roots are resolved through symlinks at construction, once: a root given as
// /var/x on macOS is /private/var/x underneath, and comparing a resolved
// candidate against an unresolved root would refuse everything.
//
// paths.Data() is refused even when it sits inside a root, and that is the
// one denylist entry. It holds the age identity that unlocks the store, and
// no argument a caller sends should ever name it — a `read` capability that
// hashes it or reports its size is answering a question about the key to
// every secret on the machine. It is not covered by the root rule because an
// operator who starts the server from their home directory has, without
// meaning to, put it back in scope.
func New(roots ...string) (*Guard, error) {
	g := &Guard{}
	for _, r := range roots {
		abs, err := resolve(r)
		if err != nil {
			return nil, fmt.Errorf("root %q: %w", r, err)
		}
		g.roots = append(g.roots, abs)
	}
	if len(g.roots) == 0 {
		return nil, fmt.Errorf("a guard needs at least one root")
	}
	if d, err := resolve(paths.Data()); err == nil {
		g.denied = append(g.denied, d)
	}
	return g, nil
}

// Roots reports what this guard allows, for a message that has to say so.
func (g *Guard) Roots() []string {
	if g == nil {
		return nil
	}
	return append([]string(nil), g.roots...)
}

// Check reports whether raw is in bounds, as a *view.Error a surface can
// render.
//
// A nil or zero Guard allows everything: that is the CLI and the TUI, where
// there is a person who can already read their own files and for whom this
// would be an obstacle with no threat behind it.
//
// An empty value is allowed. "not given" is not a path, and refusing it would
// turn every optional path input into a required one.
func (g *Guard) Check(field, raw string) (string, *view.Error) {
	if g == nil || len(g.roots) == 0 || strings.TrimSpace(raw) == "" {
		return raw, nil
	}
	if remote(raw) {
		return "", view.Errorf("core.mcp.path.remote",
			"%s: %q names a remote endpoint, not a path", field, raw).
			WithHint("under a root, a path input is a local path and nothing else — a capability " +
				"that fetches what a caller names is an outbound request an agent chose the " +
				"destination of, which is the thing a root exists to bound")
	}
	abs, err := resolve(raw)
	if err != nil {
		return "",
			// Unresolvable is refused rather than allowed. The realistic cause is
			// a path so malformed that no handler could use it either, and the
			// alternative is a value that failed the check sailing past it.
			view.Errorf("core.mcp.path.unresolvable",
				"%s: cannot resolve %q", field, raw)
	}
	for _, d := range g.denied {
		if inside(d, abs) {
			return "", view.Errorf("core.mcp.path.protected",
				"%s: %q is inside rta's own data directory", field, raw).
				WithHint("that is where the key to the secret store lives; nothing reachable " +
					"from an agent may name it, whatever the capability would have done with it")
		}
	}
	for _, r := range g.roots {
		if inside(r, abs) {
			return abs, nil
		}
	}
	return "", view.Errorf("core.mcp.path.outside",
		"%s: %q is outside what this server may read (%s)",
		field, raw, strings.Join(g.roots, ", ")).
		WithHint("an MCP server reads only under its roots, because there is no person here to " +
			"judge the request — ask the operator to restart it with --root, or use a path inside")
}

// scpLike matches git's other address form, `user@host:path`, which has no
// scheme to give it away. Anchored and deliberately narrow: a host part with
// no slash in it, then a colon. A Windows drive letter has no "@" and a real
// local file called "notes@work" has no colon after it.
var scpLike = regexp.MustCompile(`^[A-Za-z0-9._~-]+@[A-Za-z0-9._-]+:`)

// remote reports whether a caller's "path" is really an address somewhere
// else.
//
// **Under a root, a Path input is a local path and nothing else.** That
// invariant is worth more than the two lines it costs, because without it the
// guard silently does something surprising: `resolve` treats
// "https://host/repo.git" as a relative path, joins it to the working
// directory, and hands the handler "/cwd/https:/host/repo.git" — an address
// turned into a local read of a file that does not exist. builtin/git's path
// input accepts a URL by design (it clones one in memory), so the substitution
// converted a remote clone into "not a git repository" and nobody could tell
// why.
//
// Refusing is the right half of that fix rather than passing the URL through
// unchecked. A capability that fetches whatever a caller names is an outbound
// request whose destination an agent chose — the shape a root exists to
// bound — and it is a `read` capability with no grant in front of it. On the
// CLI and the TUI there is no guard, so the URL still works for the person who
// typed it.
func remote(raw string) bool {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i > 0 && !strings.ContainsAny(s[:i], `/\`) {
		return true
	}
	if unc(s) {
		return true
	}
	return scpLike.MatchString(s)
}

// unc reports whether a path names a Windows network share.
//
// **This is the one address form that makes the guard itself do the
// connecting.** The two cases above hand a mangled string to a handler and let
// it fail; a UNC path does its damage inside `resolve`, before any in-root
// decision is reached. `filepath.EvalSymlinks` on `\\host\share` asks the
// Windows SMB redirector to open it, which dials the host and authenticates
// with the rta process's own machine credentials — the forced-authentication
// primitive Responder and ntlmrelayx are built to catch. The eventual
// "outside what this server may read" is returned long after the NetNTLM
// exchange has happened, so refusing at the string is the only place it can be
// stopped.
//
// Two separators, judged differently, because they are not the same claim:
//
//   - A backslash pair is refused on every platform. No POSIX caller means a
//     file whose name begins `\\`, and the server's GOOS is not something the
//     value should depend on when the value is this unambiguous.
//   - A forward-slash pair is refused only on Windows, where FromSlash turns
//     `//host/share` into exactly the UNC volume above. On POSIX `//x/y` is an
//     ordinary absolute path that Clean collapses to `/x/y`, and refusing it
//     there would be a false positive on a path nobody chose for its network
//     meaning.
//
// Testing the first two bytes rather than matching a host name also covers
// `\\?\` and `\\.\` device paths, which a host-shaped pattern would let
// through.
//
// The cost, stated rather than discovered: an operator who serves with
// `--root \\fileserver\projects` can no longer have callers name absolute
// paths under it, because this refuses the root's own spelling. That
// deployment is exotic, the refusal is explicit and names itself, and
// fail-closed is the rule everywhere else in this package — a caller-chosen
// network destination is not something to allow because a root happened to be
// spelled the same way.
func unc(s string) bool {
	if len(s) < 2 {
		return false
	}
	sep := func(c byte) bool {
		return c == '\\' || (c == '/' && runtime.GOOS == "windows")
	}
	return sep(s[0]) && sep(s[1])
}

// resolve turns a caller's string into the absolute path a handler would
// actually open.
//
// Symlinks are followed on the deepest part that exists, and the rest is
// joined back on. Doing it lexically would leave the obvious bypass in place:
// a symlink inside a root pointing at /etc passes a string-prefix test and
// then opens /etc. Doing it with EvalSymlinks alone would fail for every path
// that does not exist yet, which is most of the write ones.
func resolve(raw string) (string, error) {
	p := filepath.FromSlash(ExpandTilde(strings.TrimSpace(raw)))
	if !filepath.IsAbs(p) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		p = cwd + string(filepath.Separator) + p
	}

	// Component by component, resolving each one before the next is applied.
	//
	// The previous version called filepath.Abs and filepath.Clean first, and
	// that was the bug: Clean cancels ".." against the component before it,
	// *lexically*, so a directory symlink was removed from the string before
	// anything asked the filesystem about it. With root/link -> ../secrets,
	// "root/link/id_rsa" was correctly refused and
	// "root/link/../secrets/id_rsa" was allowed — and read the same bytes,
	// because the kernel resolves link first and then applies "..", arriving
	// somewhere the guard never looked. Every real directory level under a
	// root bought one more "..", so the reach was arbitrary: /etc/passwd and
	// rta's own grants.key were both reachable from a root that contained a
	// symlink.
	//
	// Doing it this way makes ".." mean what it means to the kernel — the
	// parent of wherever we actually are — and keeps the property the old
	// code was written for, that a path which does not exist yet still
	// resolves: once a component is missing, the rest accumulates lexically,
	// which is correct because a path that does not exist cannot be a symlink.
	vol := filepath.VolumeName(p)
	out := vol + string(filepath.Separator)
	for _, seg := range strings.Split(p[len(vol):], string(filepath.Separator)) {
		switch seg {
		case "", ".":
			continue
		case "..":
			out = filepath.Dir(out)
			continue
		}
		next := filepath.Join(out, seg)
		if resolved, err := filepath.EvalSymlinks(next); err == nil {
			out = resolved
			continue
		}
		out = next
	}
	return out, nil
}

// within reports whether p is root or lives under it.
//
// filepath.Rel rather than a string prefix, because "/home/user" is a prefix
// of "/home/username" and a prefix test would hand one user's files to
// another's root.
// inside reports whether p is root or under it, by name and then by identity.
//
// The name test alone is wrong on any case-insensitive filesystem, which is
// the default on macOS and Windows. Reproduced on this machine: with the data
// directory denied, `…/rta/grants.key` is refused and `…/RTA/grants.key` is
// allowed — and reads the same bytes. That is the seal key for every grant
// (internal/grant/seal.go) named by an agent that changed one letter's case.
//
// Case-folding the strings would be the obvious fix and is the wrong one: a
// path is only case-insensitive if the filesystem holding it says so, and
// that is per-volume, not per-OS. macOS ships case-sensitive APFS volumes and
// Linux mounts case-insensitive ones. So the fallback asks the filesystem
// instead of guessing: walk up the candidate's existing ancestors and compare
// each against root by file identity, which is true whatever the volume does
// with case, and is the same test os.SameFile exists for.
//
// The cheap string test stays first because it answers almost every call
// without a syscall, and it is the only thing that works when neither path
// exists yet — `--out` naming a file to create is the ordinary case.
func inside(root, p string) bool {
	if within(root, p) {
		return true
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		// Nothing on disk to compare against. The string test above is all
		// there is, and a root that does not exist protects nothing anyway.
		return false
	}
	for cur := p; ; {
		if info, err := os.Stat(cur); err == nil && os.SameFile(info, rootInfo) {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

// ExpandTilde replaces a leading ~ with the user's home directory.
//
// Only a leading "~" or "~/": "~user" is deliberately not supported, because
// resolving another account's home is not something any input here means, and
// a file literally named "~something" in the current directory should keep
// working.
func ExpandTilde(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
}
