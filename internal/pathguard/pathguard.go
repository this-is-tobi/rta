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
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/pkg/plugin"
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
	// public is the directories under denied that hold public content rta
	// can fetch again, refused by name and left out of what the state is
	// known by by identity (ownState): the plugin index clones.
	public []string
	// seen is what denied held when the guard was made, by identity, for
	// the file of it a caller moves out before a call looks (ownState).
	seen *idSet
}

// New builds a guard rooted at each of roots.
//
// Roots are resolved through symlinks at construction, once: a root given as
// /var/x on macOS is /private/var/x underneath, and comparing a resolved
// candidate against an unresolved root would refuse everything.
//
// paths.Data() is refused even when it sits inside a root. It holds the age
// identity that unlocks the store, and no argument a caller sends should
// ever name it — a `read` capability that hashes it or reports its size is
// answering a question about the key to every secret on the machine. It is
// not covered by the root rule because an operator who starts the server
// from their home directory has, without meaning to, put it back in scope.
//
// rta's configuration is refused with it (configDenied). Plugin confinement
// has denied both to plugins from its first commit (internal/pluginhost's
// tier1) and this gate denied only the first: config.yaml names every
// environment an operator has and the `secrets:` references that point at
// them, remotes.yaml beside it names every server they operate, and the
// refusal that keeps an agent from learning a profile name by being refused
// (internal/mcp's ungranted) was one `fs_tree ~/.config` away from being
// undone.
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
	for _, own := range append([]string{paths.Data()}, configDenied()...) {
		if d, err := resolve(own); err == nil {
			g.denied = append(g.denied, d)
		}
	}
	if d, err := resolve(paths.Indexes()); err == nil {
		g.public = append(g.public, d)
	}
	g.seen = readState(g.denied, g.public)
	return g, nil
}

// configDenied is what of rta's configuration a caller may never name, and
// the shape follows where the file is.
//
// rta's own directory under the user's config directory is denied whole:
// everything in it is rta's. A file named by RTA_CONFIG can sit anywhere —
// inside a project that is also the root, in the container recipe's data
// directory — so denying its directory could deny the root itself and refuse
// every path; the file and the remotes.yaml rta reads beside it are denied
// instead. The ./.rta.yaml fallback gets the same file-level treatment, for
// the same reason: its directory is the working directory.
func configDenied() []string {
	file := paths.ConfigFile()
	if own := paths.OwnConfigDir(); own != "" && within(own, file) {
		return []string{own}
	}
	return []string{file, filepath.Join(filepath.Dir(file), "remotes.yaml")}
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
	return g.check(field, raw, false)
}

// Derived is Check for a path a handler reached from the ones it was given
// rather than received — the repository a walk upward from a directory
// found, the git directory a .git file points at, the directory a
// core.hooksPath names — which is what plugin.Request.Confine asks. The
// bounds are Check's; the refusal is worded for a caller who never sent the
// path. Check's told an agent to "use a path inside" about a git directory it
// had never named, and no argument it could send would have moved that
// directory anywhere: the repository it pointed at was inside the root, and
// what lay outside was where that repository kept itself.
func (g *Guard) Derived(field, path string) (string, *view.Error) {
	return g.check(field, path, true)
}

func (g *Guard) check(field, raw string, derived bool) (string, *view.Error) {
	if g == nil || len(g.roots) == 0 || strings.TrimSpace(raw) == "" {
		return raw, nil
	}
	named := fmt.Sprintf("%q", raw)
	if derived {
		named += ", which this call reached from the path it was given,"
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
		// A loop comes with where the chain had got to, and one that lies
		// outside the roots is refused as anything there is, below: refused
		// as a loop, a link to a loop outside said what a link to a missing
		// name there did not, which is that something is there.
		reached := abs == ""
		for _, r := range g.roots {
			reached = reached || inside(r, abs)
		}
		if reached {
			return "",
				// Unresolvable is refused rather than allowed. The realistic cause is
				// a path so malformed that no handler could use it either, and the
				// alternative is a value that failed the check sailing past it.
				view.Errorf("core.mcp.path.unresolvable",
					"%s: cannot resolve %q", field, raw)
		}
	}
	for _, d := range g.denied {
		if inside(d, abs) {
			return "", protected(field, named)
		}
	}
	for _, r := range g.roots {
		if inside(r, abs) {
			return abs, nil
		}
	}
	instead := "use a path inside, or "
	if derived {
		instead = "no argument names this path, so none can bring it inside; "
	}
	return "", view.Errorf("core.mcp.path.outside",
		"%s: %s is outside what this server may read (%s)",
		field, named, strings.Join(g.roots, ", ")).
		WithHint("an MCP server reads only under its roots, because there is no person here to " +
			"judge the request — " + instead + plugin.AskOperator("mcp serve --root <dir>") +
			" to serve another root")
}

// protected is the refusal of a path in rta's own state or configuration,
// named as the refusal around it names it.
func protected(field, named string) *view.Error {
	return protectedAs(fmt.Sprintf("%s: %s is rta's own state or configuration", field, named))
}

// protectedAs is that refusal, saying what message says.
func protectedAs(message string) *view.Error {
	return view.Errorf("core.mcp.path.protected", "%s", message).
		WithHint("the data directory holds the key to the secret store and the configuration " +
			"names every environment and server; nothing reachable from an agent may name " +
			"either, whatever the capability would have done with it")
}

// Bounds is this guard's reach as a request carries it to a handler
// (plugin.Bounds), and a nil guard's is the zero Bounds, which opens by name.
//
// Root judges a path as Derived does — every path a handler opens through it
// was either judged already, at the boundary, or reached from one that was —
// and hands back the root it lies under with the resolved path relative to
// it. Judged again, not trusted from the boundary, because the path a handler
// holds is a name, and the point is that a name can change between two looks
// at it: this look is the one the open follows, from the root, through
// nothing that has changed since.
func (g *Guard) Bounds() plugin.Bounds {
	if g == nil || len(g.roots) == 0 {
		return plugin.Bounds{}
	}
	return plugin.Bounds{Root: g.openRoot, Refuse: g.refuser()}
}

func (g *Guard) openRoot(path string) (*os.Root, string, error) {
	abs, verr := g.Derived("path", path)
	if verr != nil {
		return nil, "", verr
	}
	for _, r := range g.roots {
		rel, ok := under(r, abs)
		if !ok {
			continue
		}
		root, err := os.OpenRoot(r)
		if err != nil {
			return nil, "", err
		}
		return root, rel, nil
	}
	// Derived allowed it, so it is under a root; a root that has moved since
	// is the one way to get here, and the answer is the refusal.
	return nil, "", view.Errorf("core.mcp.path.outside", "path: %q is outside what this server may read", path)
}

// refuser is Bounds' Refuse: what a walk may not enter or open under a root,
// and what an opener may not have opened there, which is rta's own state and
// configuration, as Check refuses a path naming them.
//
// By name, and by identity as well (ownState): a walk reaches paths through
// real directories only, so the name it holds is where it is, but a
// case-insensitive volume answers to a name in any case, a hard link is a
// name anywhere for the same file, and a file moved onto a judged name is at
// that name. One call's bounds share one reading of the denied paths, taken
// when the call first asks, rather than one at every entry of a walk that
// may reach a million.
//
// Refused by identity, a path is said to be another name for the state
// rather than one reached from the path given, which it may not be — fs.hash
// asked of a hard link is refused of the very path the caller sent — and the
// words are the ones an operator needs: a file of a project refused as rta's
// state is a hard link to one, or one moved there.
func (g *Guard) refuser() func(string, fs.FileInfo) error {
	own := &ownState{denied: g.denied, public: g.public, seen: g.seen}
	return func(path string, info fs.FileInfo) error {
		if slices.ContainsFunc(g.denied, func(d string) bool { return within(d, path) }) {
			return protected("path", fmt.Sprintf("%q, which this call reached from the path it was given,", path))
		}
		if info != nil && own.holds(info) {
			return protectedAs(fmt.Sprintf("path: %q is another name for rta's own state or configuration", path))
		}
		return nil
	}
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
	//
	// A link is followed here, one hop at a time, rather than by
	// filepath.EvalSymlinks, because of the links EvalSymlinks cannot follow
	// to the end: one whose target is missing, or behind a directory this
	// process may not search. Those failed EvalSymlinks and were judged as
	// their own path, which is where the link sits — inside the root — while
	// a link whose target was there was judged by the target and refused. So
	// the answer said whether a file outside the roots exists, to a caller
	// who could make a link to any name it wanted to ask about. A hop's target
	// is spliced in as written, relative to the link's directory, and
	// resolved on from there like the rest of the path, so a link is judged
	// by where it points whether or not anything is there.
	//
	// What that leaves is a chain that leaves the roots and comes back: a
	// link inside to a link outside that leads back in is judged where it
	// ends, as the kernel opens it, so the answer turns on whether the outside
	// link is there. Refusing every hop outside would refuse the ordinary
	// case of that shape — a target spelled through /var or /tmp, links on
	// macOS to the resolved roots under /private — and the outside link it
	// would reveal is one pointing into the roots, which only somebody who
	// could already write outside them can make.
	vol := filepath.VolumeName(p)
	out := vol + string(filepath.Separator)
	rest := NameParts(runtime.GOOS, p[len(vol):])
	hops := 0
	for len(rest) > 0 {
		seg := rest[0]
		rest = rest[1:]
		switch seg {
		case "", ".":
			continue
		case "..":
			out = filepath.Dir(out)
			continue
		}
		next := filepath.Join(out, seg)
		target, isLink := readLink(next)
		if !isLink {
			out = next
			continue
		}
		// A loop never ends anywhere; the kernel refuses to open one past
		// its own limit, and the number is EvalSymlinks'. Where the chain had
		// got to comes back with the refusal, for the guard to judge as it
		// judges any path (check).
		if hops++; hops > 255 {
			return out, fmt.Errorf("%s: too many levels of symbolic links", raw)
		}
		switch tv := filepath.VolumeName(target); {
		case filepath.IsAbs(target):
			out, target = tv+string(filepath.Separator), target[len(tv):]
		case VolumeRooted(runtime.GOOS, target):
			// Rooted but not absolute, which only Windows has: the root of
			// the volume the link is on.
			out = filepath.VolumeName(out) + string(filepath.Separator)
		}
		// Taken apart at every separator the system reads: split at the
		// backslash alone, a Windows link written with forward slashes was
		// one part, whose .. filepath.Join took off before the link ahead of
		// it was asked where it led — judging a path the kernel never opens.
		rest = append(NameParts(runtime.GOOS, target), rest...)
	}
	return out, nil
}

// readLink reports whether path is a symbolic link, and what it holds. A
// path that cannot be looked at is not one: the rest of it is then judged
// lexically, as a path that does not exist is.
func readLink(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	return target, true
}

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
	_, ok := under(root, p)
	return ok
}

// under is inside, and where under root p lies: the path from root to it,
// "." for root itself. By identity, that is the path from the ancestor that
// is root, which on a case-insensitive volume may be spelled otherwise.
func under(root, p string) (string, bool) {
	if rel, ok := relWithin(root, p); ok {
		return rel, true
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		// Nothing on disk to compare against. The string test above is all
		// there is, and a root that does not exist protects nothing anyway.
		return "", false
	}
	for cur := p; ; {
		if info, err := os.Stat(cur); err == nil && os.SameFile(info, rootInfo) {
			rel, err := filepath.Rel(cur, p)
			return rel, err == nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

// within reports whether p is root or lives under it, by name alone.
//
// filepath.Rel rather than a string prefix, because "/home/user" is a prefix
// of "/home/username" and a prefix test would hand one user's files to
// another's root.
func within(root, p string) bool {
	_, ok := relWithin(root, p)
	return ok
}

func relWithin(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return "", false
	}
	return rel, rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

// ExpandTilde replaces a leading ~ with the user's home directory.
//
// Only a leading "~" or "~/", and "~\" on Windows, its own separator: "~user"
// is deliberately not supported, because resolving another account's home is
// not something any input here means, and a file literally named "~something"
// in the current directory should keep working.
//
// The rule itself lives in pkg/plugin, exported so a plugin can apply the
// same one to its own Local path inputs; this is the host's name for it.
func ExpandTilde(p string) string { return plugin.ExpandHome(p) }
