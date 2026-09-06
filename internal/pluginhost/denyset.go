package pluginhost

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/pkg/plugin"
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
// plugin's business. What that does not buy is said plainly — the honest
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
// kv capability. Sealing the grant file closed that specific route;
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
// tier2 is keyed by the need that asks for each location, because denying a
// location outright is only right until a plugin's whole purpose is to use it.
//
// `~/.kube` is the case that proved it: on the list from the first commit of
// this package, before plugins/kube existed, and the comment above names "a
// `kube` plugin reads ~/.kube/config" as the example of what a denylist must
// not get in the way of. It got in the way of exactly that — both kube and
// cnpg were unable to read a kubeconfig at all on macOS, which is to say
// unable to do anything.
//
// The key is what makes the entry grantable. A plugin declares the need, an
// operator grants it against the artifact's digest, and only then is the path
// left out of that plugin's profile. Everything ungranted is denied exactly
// as before, so the default is unchanged and the exception is something
// somebody said out loud.
func tier2() map[plugin.Need]string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	rel := map[plugin.Need]string{
		plugin.NeedSSH:        ".ssh",
		plugin.NeedAWS:        ".aws",
		plugin.NeedKubeconfig: ".kube",
		plugin.NeedGnuPG:      ".gnupg",
		plugin.NeedDocker:     ".docker/config.json",
		plugin.NeedGCloud:     ".config/gcloud",
		plugin.NeedNetrc:      ".netrc",
		plugin.NeedNPM:        ".npmrc",
		plugin.NeedPyPI:       ".pypirc",
		plugin.NeedGitCreds:   ".git-credentials",
	}
	out := make(map[plugin.Need]string, len(rel))
	for need, r := range rel {
		out[need] = filepath.Join(home, r)
	}
	return out
}

// asNeeds converts a stored grant to the closed set.
//
// An entry rta does not recognise is dropped rather than refused: the store
// outlives the build that wrote it, and a need removed from a later rta must
// not stop every plugin loading. Dropping it denies the location, which is
// the safe direction — the plugin fails at the operation that wanted it and
// the operator is told what to grant.
func asNeeds(stored []string) []plugin.Need {
	out := make([]plugin.Need, 0, len(stored))
	for _, s := range stored {
		if n := plugin.Need(s); plugin.KnownNeed(n) {
			out = append(out, n)
		}
	}
	return out
}

// Tier2Path is the location a need names, for a caller that has to say which
// file an operator is about to allow.
func Tier2Path(n plugin.Need) string { return tier2()[n] }

// DenySet is the resolved policy for this machine, so that a caller — `rta
// doctor`, a test, the profile builder — reads one value rather than
// recomputing it three ways.
type DenySet struct {
	// NoAccess is denied for reading and writing; NoRead for reading only.
	// Each holds every path plus, where a path is a symlink, its target.
	NoAccess []string
	NoRead   []string
	// NoMove is every directory whose own name has to stay where it is for the
	// two sets above to go on meaning anything. Denied for renaming and
	// removal, and for nothing else — the entry itself, never its contents.
	//
	// **A rule that names a path stops applying when the path stops having
	// that name.** Proven against /usr/bin/sandbox-exec, not reasoned about:
	// with `~/.ssh` read-denied and writes deliberately left open, `mv ~/.ssh
	// ~/x` succeeds and every key is then readable at a path no rule mentions.
	// Two syscalls, no cleverness, and it applies to all ten of tier2 — the
	// entire tier bought nothing.
	//
	// The ancestors are the same defect one level up and are why this is a
	// separate list rather than an extra verb on the others. `~/.docker/
	// config.json` is read-denied; `~/.docker` is not, so renaming *it* moves
	// the file out from under the rule just as effectively. Denying the whole
	// ancestor subpath would deny writing to `~/.config` and `~` — which is
	// most of what a plugin legitimately does — so what is denied is the
	// literal directory entry, leaving everything inside it alone.
	NoMove []string
	// Own is the launched artifact's own directory, readable again under the
	// denial that covers it. Empty for a plugin found anywhere but the store.
	//
	// **On macOS a process that cannot read its own directory cannot verify
	// a TLS certificate.** Proven with a plain Go binary under
	// /usr/bin/sandbox-exec rather than reasoned about: executed from a
	// read-denied directory, every HTTPS request fails with
	// `SecPolicyCreateSSL error: 0`, and the same binary a directory over
	// succeeds — the Security framework initialises from the main
	// executable's location, and it wants the directory, not the file
	// (allowing the executable alone changes nothing). Installed artifacts
	// live in <data>/plugins/store/<name>/<digest>, inside the tree tier1
	// denies both verbs, so every managed plugin was unable to reach any
	// https:// address — while the same plugin built locally and found on
	// $PATH worked, which is why no test and no development machine saw it.
	//
	// Reads only, of exactly that directory, and only for an artifact rta
	// placed there. The directory holds the binary and nothing else, and the
	// process already is that binary; the rest of the data directory stays
	// denied, the write half of the rule stays as it was, and a plugin
	// anywhere else gains nothing — the carve-out is for the layout rta
	// chose, not for wherever an executable happens to sit.
	Own []string
}

// Resolve builds the deny set for this machine.
//
// Every entry is expanded through symlinks with rules emitted for *both* the
// link and its target. A denied path that is itself a symlink is otherwise
// bypassed in two syscalls, and that is not exotic: `~/.aws → ~/dotfiles/aws`
// is what home-manager, chezmoi and stow all produce, so the machines most
// likely to be running this are the ones where the naive version does nothing.
//
// A path that does not exist is kept rather than dropped, and resolved as far
// as the filesystem allows right now — which is what withTarget's walk is for.
// It may be created while the plugin runs, and a rule naming a path that never
// appears costs nothing. What it must not do is name a path the kernel never
// produces, which is what a missing component used to cause.
func Resolve() (DenySet, error) { return ResolveAllowing(nil) }

// ResolveAllowing builds it for one artifact, leaving out the locations that
// artifact has been granted.
//
// A grant subtracts from the deny list and can do nothing else — it cannot add
// a path, cannot reach tier1, and cannot name anything outside the closed set
// tier2 is keyed by. That is the same shape team policy has: a mechanism that
// can only ever narrow is one whose worst case is bounded by what it started
// from.
func ResolveAllowing(granted []plugin.Need) (DenySet, error) {
	allowed := map[plugin.Need]bool{}
	for _, n := range granted {
		allowed[n] = true
	}
	var d DenySet
	for _, p := range tier1() {
		d.NoAccess = append(d.NoAccess, withTarget(p)...)
	}
	// **In plugin.Needs() order, never the map's.** Go randomises map
	// iteration, and this slice is hashed into the process cache key
	// (specHash): a set whose order moves between two calls hashes
	// differently, the cache misses, and every call spawns a fresh plugin
	// process — which is the exact thing that cache was written to prevent.
	// Caught by TestTheSameBinaryIsNotLaunchedTwice, which is why it is a
	// test and not a comment.
	paths := tier2()
	for _, need := range plugin.Needs() {
		if allowed[need] {
			continue
		}
		if p := paths[need]; p != "" {
			d.NoRead = append(d.NoRead, withTarget(p)...)
		}
	}
	d.NoAccess, d.NoRead = dedupe(d.NoAccess), dedupe(d.NoRead)
	d.NoMove = ancestors(append(append([]string{}, d.NoAccess...), d.NoRead...))
	if err := validate(append(append(append([]string{}, d.NoAccess...), d.NoRead...), d.NoMove...)); err != nil {
		return DenySet{}, err
	}
	return d, nil
}

// Launching is d for one launch: the artifact's own directory readable when
// it lies inside the managed store, and d unchanged otherwise. See Own.
//
// Validated like every other entry, because it is rendered into the same
// policy string — and refused rather than dropped for the same reason
// ResolveAllowing refuses: a rule silently left out is a policy nobody
// noticed shrinking, in either direction.
func (d DenySet) Launching(exe string) (DenySet, error) {
	dir := filepath.Dir(exe)
	for _, store := range withTarget(ManagedStore()) {
		if !inside(dir, store) {
			continue
		}
		own := dedupe(withTarget(dir))
		if err := validate(own); err != nil {
			return DenySet{}, err
		}
		d.Own = own
		return d, nil
	}
	return d, nil
}

// inside reports whether p is root or lies below it, by path components —
// a prefix test on the string alone would put /data-old inside /data.
func inside(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// ancestors is every directory above each path, up to the root.
//
// The whole chain rather than the immediate parent: `~/.config/gcloud` is
// moved out from under its rule by renaming `~/.config`, and equally by
// renaming `~`. Naming all of them costs a handful of rules that match a
// directory entry nothing honest renames, and stops the question being "how
// far up did we think to look".
func ancestors(paths []string) []string {
	var out []string
	for _, p := range paths {
		for dir := filepath.Dir(p); ; {
			out = append(out, dir)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return dedupe(out)
}

// withTarget returns p and, if any part of p resolves elsewhere, that too.
//
// **Both spellings are load-bearing and neither is redundant.** SBPL matches
// against the kernel's canonical path, so the resolved form is the one that
// actually denies reads. The unresolved form is what pins the *link* — a
// `(literal "<link>")` under file-write-unlink is what stops `mv ~/.config x`,
// and it does not protect the directory behind it.
func withTarget(p string) []string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return []string{p}
	}
	if resolved := resolveDeepest(abs); resolved != abs {
		return []string{abs, resolved}
	}
	return []string{abs}
}

// resolveDeepest resolves the longest existing prefix of abs and re-attaches
// what is left.
//
// filepath.EvalSymlinks fails on the *whole* path when any single component is
// missing, and withTarget used to treat that failure exactly like "not a
// symlink" — returning the unresolved spelling alone. Missing components are
// the normal case here, not an edge: the deny set names `~/.config/gcloud` on
// machines with no gcloud, `~/.docker/config.json` before a docker login, and
// rta's own data directory before rta has ever written anything.
//
// When an *ancestor* is a symlink, that unresolved spelling is a path the
// kernel never produces, so the rule is inert rather than narrow — proven
// against /usr/bin/sandbox-exec: with `.config` a link and a profile naming
// only the link spelling, a sandboxed `cat` read the credential through both
// names; naming only the resolved spelling denied both. That layout is exactly
// the one Resolve's comment cites — home-manager, chezmoi and stow all produce
// it — so the function was blindest on the machines it was written for.
//
// internal/pathguard does the same walk for MCP arguments, and for the same
// reason it states there: once a component is missing the rest accumulates
// lexically, which is correct, because a path that does not exist cannot be a
// symlink. A component created later *as a symlink* still points somewhere
// this did not name; that is why the deny set is recomputed on every spawn and
// why the ancestors are pinned against creation and renaming.
func resolveDeepest(abs string) string {
	vol := filepath.VolumeName(abs)
	out := vol + string(filepath.Separator)
	for _, seg := range strings.Split(abs[len(vol):], string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		next := filepath.Join(out, seg)
		if resolved, err := filepath.EvalSymlinks(next); err == nil {
			out = resolved
			continue
		}
		out = next
	}
	return out
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
	forbidden := forbiddenIn(runtime.GOOS)
	for _, e := range entries {
		if i := strings.IndexAny(e, forbidden); i >= 0 {
			return fmt.Errorf("refusing to build a sandbox profile: path %q contains %q, "+
				"which cannot be safely quoted in a policy file", e, e[i:i+1])
		}
	}
	return nil
}

// forbiddenIn is what validate refuses, which is everything above except on
// the one platform where a backslash is not an anomaly but the separator.
//
// **Refusing `\` on Windows refuses every path there is.** `C:\Users\...`
// contains one by construction, so ResolveAllowing returned an error for any
// machine-derived deny set and Host.OpenAllowing handed that straight back to
// its caller: rta could not launch a single plugin on Windows, whatever else
// was fixed. The deny set is not even rendered there — profile() returns ""
// and wrap() is the identity on every platform but darwin (confine_other.go)
// — so the check was failing closed on a policy file that does not exist.
//
// The other five characters stay forbidden everywhere, and that is the half
// worth keeping: a quote, a newline or a parenthesis in a path is an anomaly
// on any platform, and refusing it on Linux is how the darwin renderer never
// meets one. Only the separator is platform knowledge.
//
// Branched on runtime.GOOS rather than a build tag, which the comment above
// asks for in as many words: a check that only compiles on one GOOS is a
// check nobody runs. This one compiles everywhere and both answers are
// reachable from any machine, so validate's Windows behaviour is testable on
// the laptop it was written on rather than only on the platform it is for.
func forbiddenIn(goos string) string {
	const (
		anywhere = "\"\n\r()"
		posix    = anywhere + `\`
	)
	if goos == "windows" {
		return anywhere
	}
	return posix
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
