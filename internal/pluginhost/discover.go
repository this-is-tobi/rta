package pluginhost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"strings"

	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/internal/registry"
)

// Prefix is how an SDK plugin announces itself on $PATH.
//
// The exec tier already owns `rta-<name>` (the escape hatch: any binary in
// any language that prints a view as JSON, no SDK involved), so the two tiers
// need names that cannot be confused. `rta-plugin-<name>` is the longer
// prefix, and the rule is decidable in one direction: everything matching
// this is an SDK plugin, everything else matching `rta-` is exec tier.
//
// Discovery is $PATH rather than a directory rta owns, for the reason every
// comparable tool lands on it — krew, gh, kubectl, git. It is what a package
// manager can already install into, what a user already knows how to inspect,
// and what `which` already answers questions about. A private directory means
// rta inventing an install step, and an install step nobody else's tooling
// understands is how a plugin ecosystem fails to start.
const Prefix = "rta-plugin-"

// BinaryName is what a plugin's artifact is called on *this* machine — the
// name to build, to install, and to look for. Not what an index calls it: a
// manifest describes six platforms and only one of them is this one.
func BinaryName(name string) string { return Prefix + name + ExeSuffix }

// Namespace is the plugin name a filename announces, and false when it
// announces none.
//
// The suffix half is inert on Unix (ExeSuffix is empty, so every name has it)
// and load-bearing on Windows, where the artifact is `rta-plugin-pg.exe`.
// Trimming only the prefix there yields the namespace "pg.exe", which then
// disagrees with everything the binary declares about itself — and LoadInto
// would refuse the plugin, correctly, for a reason that was really here.
func Namespace(filename string) (string, bool) {
	if !strings.HasPrefix(filename, Prefix) || !strings.HasSuffix(filename, ExeSuffix) {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(filename, Prefix), ExeSuffix), true
}

// Found is a discovered plugin binary, before anything has been launched.
type Found struct {
	// Name is what comes after the prefix, and it is what the namespace must
	// turn out to be. The namespace still arrives over the wire, from
	// Describe, because that is the only place it exists — but it is checked
	// against this rather than trusted, and a binary named rta-plugin-x
	// declaring "kv" is refused (LoadInto).
	//
	// The direction matters and it is the whole point: this name is the one
	// the *operator* gave the artifact by installing it at this path. The
	// declared one is what the artifact says about itself. Where those
	// disagree, the operator wins.
	Name string
	Path string
	// Shadowed lists the later $PATH entries with this same name, which
	// discovery skipped. Ordinary — a local build in front of a packaged
	// one is the case the ordering rule exists to resolve — but ordinary and
	// invisible is how "why is it running the old one" becomes an afternoon,
	// so `rta doctor` says which won and what it covered.
	Shadowed []string
}

// ManagedBin is the managed store's directory of current-version symlinks
// — <data>/plugins/bin. Defined here rather than in
// internal/plugindist, which owns the store, because discovery must scan it
// and plugindist already imports this package; plugindist.BinDir delegates,
// so the layout still has one home.
func ManagedBin() string { return filepath.Join(paths.Data(), "plugins", "bin") }

// Discover lists SDK plugin binaries on $PATH, then in the managed store.
//
// First match wins, in $PATH order, which is what a shell does and therefore
// what a user predicts. A later duplicate does not change that and is not an
// error — two entries for the same name on a $PATH is ordinary, and it is
// exactly the case the ordering rule exists to resolve — but it is recorded
// on the winner rather than dropped, so `rta doctor` can answer "which one is
// this actually running" without anybody reaching for `which -a`.
//
// The managed store's bin/ comes after every $PATH entry, deliberately last:
// $PATH is the operator's own statement and the store is rta's, so a copy the
// operator put on $PATH shadows the managed one the ordinary, reported way —
// rather than rta's store silently outranking a deliberate local build. An
// operator who wants managed plugins visible to other tools adds the dir to
// their $PATH themselves, and the walked-dedup below keeps that from reading
// it twice.
func Discover() []Found {
	at := map[string]int{}
	// A directory named twice on $PATH is walked once. Shells commonly end
	// up with duplicates — a profile sourced twice, a tool prepending its own
	// bin on every shell — and a repeat cannot change which copy wins, since
	// the first occurrence already did. All it can do is report the same file
	// as a shadow of itself: `rta doctor` said "2 further copy on $PATH not
	// used" and then printed one path, twice.
	//
	// Cleaned before comparing, so `/usr/local/bin` and `/usr/local/bin/` are
	// the one entry they are. Not resolved through symlinks: that would make
	// two genuinely different $PATH entries collapse into one, which changes
	// which shadows exist rather than which are reported twice.
	walked := map[string]bool{}
	var out []Found
	dirs := append(filepath.SplitList(os.Getenv("PATH")), ManagedBin())
	for _, dir := range dirs {
		if clean := filepath.Clean(dir); walked[clean] {
			continue
		} else {
			walked[clean] = true
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		// os.ReadDir is already sorted, but the guarantee is worth not
		// relying on: two plugins in one directory must resolve the same way
		// on every machine.
		sort.Strings(names)
		for _, name := range names {
			namespace, isPlugin := Namespace(name)
			if !isPlugin {
				continue
			}
			full := filepath.Join(dir, name)
			info, err := os.Stat(full)
			if err != nil || info.IsDir() || !runnable(info) {
				continue
			}
			if i, dup := at[name]; dup {
				out[i].Shadowed = append(out[i].Shadowed, full)
				continue
			}
			at[name] = len(out)
			out = append(out, Found{Name: namespace, Path: full})
		}
	}
	return out
}

// LoadInto discovers every plugin on $PATH, launches it, and registers what
// it declares. It returns the problems it hit, one per plugin, having
// registered everything that worked.
//
// A broken plugin must not cost the user their built-ins. That is the whole
// shape of the error handling here: one plugin whose binary is corrupt, whose
// handshake times out, or whose declaration collides with an existing
// namespace is reported and skipped, and `rta` still starts with fifteen
// working ones. The alternative — a fatal error at startup — means any
// third-party plugin can brick the tool, which is a thing users learn once
// and then never install a plugin again.
//
// Every plugin is launched to be described, because a declaration is what the
// registry indexes and only the process knows it. That is a process spawn per
// installed plugin per rta invocation, which is fine at nought or one and is
// the obvious thing to cache on the digest once somebody has ten. The cache
// is deliberately not built yet: it is a correctness-preserving optimisation
// with a stale-entry failure mode, and it should be added against a measured
// startup time rather than an imagined one.
func (h *Host) LoadInto(ctx context.Context, reg *registry.Registry) []error {
	var problems []error
	// Which clients this loop actually registered. Open is a cache keyed on
	// (digest, confinement, argv) and deliberately carries no path — two
	// names for one artifact are one process — so a second discovery of the
	// same bytes hands back the *same* *Client, already registered.
	//
	// Tearing that one down on the second pass was the bug: reg.Register
	// refuses the namespace, and the client it then closed and forgot was the
	// incumbent. The plugin stayed registered and callable while vanishing
	// from h.running, which is a worse state than either outcome. Loaded()
	// went empty, so PluginOrigins() lost the namespace, so the MCP gate read
	// it as built-in and accepted an *unpinned* --allow-destructive for a
	// binary on $PATH: an authorization binds to an artifact, not to a name.
	// And CloseAll could no longer see the process, so the one Client.live
	// relaunched on the next call outlived rta.
	//
	// Copies, hardlinks and busybox-style multicall binaries all produce it.
	registered := map[*Client]bool{}

	// Read once for the whole sweep, and read before anything is launched.
	// This is the gate: a binary on $PATH has not been consented to by being
	// there, and the moment rta launches it to ask what it declares is the
	// moment a stranger's code runs. See internal/plugintrust.
	trusted := plugintrust.Load()
	// Asked once, up front. Open() checks this before every launch and
	// LoadInto used to reach it through Open; taking the hash out to check
	// trust first took the check with it, so on a macOS without
	// /usr/bin/sandbox-exec the operator got an opaque exec failure per plugin
	// instead of being told plugins cannot be confined here. Not a
	// confinement hole — wrap() still prepends the sandbox and the exec still
	// fails — but a diagnostic worth keeping.
	availErr := available()
	deny, denyErr := Resolve()

	for _, f := range Discover() {
		// Hashed here rather than inside Open, so the digest the operator
		// approved is the digest that gets launched — one read of the file,
		// no window between deciding and running.
		id, err := Identify(f.Path)
		if err != nil {
			problems = append(problems, fmt.Errorf("plugin %s: %w", f.Name, err))
			continue
		}
		if !trusted.Trusts(id.Digest) {
			// Recorded, not returned as a problem. LoadInto's problems are
			// printed before every command, and this is not a failure — it is
			// a decision nobody has made yet, which would then be announced on
			// every invocation forever, in the stderr of every script. It is
			// carried out through Untrusted() instead, where `rta plugin
			// list`, `rta doctor` and one line at startup can each say it in
			// the place and at the volume that suits them.
			// Whether anything already answers to this name, asked here
			// because this is the only place that knows both. A file called
			// `rta-plugin-kv` on $PATH records an Untrusted entry for a
			// namespace a built-in already owns and fully exposes — so every
			// surface reading this list would otherwise say that kv's
			// capabilities are unavailable, which is false, and send the
			// operator to `rta plugin trust kv`, which on the next start is
			// refused for declaring a namespace that is taken. A remedy that
			// cannot succeed is worse than none.
			_, taken := reg.Origin(f.Name)
			h.remember(Untrusted{Name: f.Name, Path: id.Path, Digest: id.Digest, Taken: taken})
			continue
		}
		if availErr != nil {
			problems = append(problems, fmt.Errorf("plugin %s: %w", f.Name, availErr))
			continue
		}
		if denyErr != nil {
			problems = append(problems, fmt.Errorf("plugin %s: %w", f.Name, denyErr))
			continue
		}
		// This artifact's own profile, when somebody has allowed it a
		// credential location the rest of the machine's plugins are denied.
		//
		// Keyed by digest, read from the same record trust is: the grant
		// applies to these bytes and to nothing else with this name. Resolved
		// per artifact rather than once for the sweep because the answer is
		// per artifact — and only when there is a grant, so the ordinary
		// plugin costs nothing.
		mine := deny
		if allowed := trusted.Allowed(id.Digest); len(allowed) > 0 {
			relaxed, err := ResolveAllowing(asNeeds(allowed))
			if err != nil {
				problems = append(problems, fmt.Errorf("plugin %s: %w", f.Name, err))
				continue
			}
			mine = relaxed
		}
		c, err := h.openIdentified(ctx, id, mine, nil)
		if err != nil {
			problems = append(problems, fmt.Errorf("plugin %s: %w", f.Name, err))
			continue
		}
		if registered[c] {
			// Same artifact under a second name. Not a collision between two
			// plugins, and nothing to close: the process is serving the
			// registration made a moment ago.
			problems = append(problems, fmt.Errorf(
				"plugin %s is the same binary as the one already loaded as %s (%s); ignoring the duplicate",
				f.Name, c.Declared.Name, c.Identity.Short()))
			continue
		}
		// The namespace must be the one the operator installed the artifact
		// under. Found.Name has said so since it was written — "a binary
		// named rta-plugin-x declaring namespace kv is a collision to refuse,
		// not a name to trust" — and nothing implemented it: RegisterFrom
		// refuses a namespace that is already *taken*, which protects the
		// built-ins because they register first, and protects nothing at all
		// between two plugins on $PATH. A file called rta-plugin-aaa
		// declaring "acmedb" registered as acmedb, appeared in `rta plugin
		// list` as acmedb, and served `rta acmedb …` with nothing anywhere
		// connecting the two names. Discovery is $PATH order then
		// alphabetical, so it also beat a correctly-named rta-plugin-acmedb
		// to the namespace.
		//
		// This is the same correction as the seal key, the grant lock and the
		// config section, one layer up: a name a thing chooses for itself is
		// not an identity. The filename is not a better name in the abstract
		// — it is a name the *operator* controls, by choosing what to install
		// where, and where the two disagree the operator wins.
		//
		// What it does not close is a file genuinely named rta-plugin-pg
		// earlier on $PATH than another. That is ordinary shadowing, it is
		// the risk model every command on $PATH already has, `which -a`
		// answers it, and Discover now records it for `rta doctor`.
		if c.Declared.Name != f.Name {
			c.Close()
			h.forget(c)
			// Two remedies, and they have to be different ones: the file can
			// move to match the declaration, or the declaration can move to
			// match the file. The first version of this interpolated the
			// declared name into both halves and read "rename it to
			// rta-plugin-weather, or install it as rta-plugin-weather" —
			// which tells an operator to do the thing they just did.
			problems = append(problems, fmt.Errorf(
				"plugin %s (%s) declares the namespace %q, which is not the name it is installed under; "+
					"either rename the file to %s%s, or change what it declares to %q",
				f.Path, c.Identity.Short(), c.Declared.Name,
				Prefix, c.Declared.Name, f.Name))
			continue
		}
		if err := reg.RegisterFrom(c.Declared, c.Origin()); err != nil {
			// Registered nowhere, so the process is of no further use. Left
			// running it would hold a subprocess for capabilities nothing can
			// reach.
			c.Close()
			h.forget(c)
			problems = append(problems, fmt.Errorf("plugin %s (%s): %w", f.Name, c.Declared.Name, err))
			continue
		}
		registered[c] = true
	}
	return problems
}

// Loaded returns the plugins currently registered from this host, so that
// `rta doctor` can report on them.
//
// Unknown declaration elements are reported there and not from LoadInto,
// which was the first shape and was wrong: LoadInto's return value is printed
// at startup, so a plugin built against a newer rta printed a warning before
// *every single command* the user ran. A forward-compatible plugin working
// correctly is not an event; it is a fact about the installation, and facts
// about the installation are what doctor is for.
func (h *Host) Loaded() []*Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Client, 0, len(h.running))
	for _, c := range h.running {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Declared.Name < out[j].Declared.Name })
	return out
}

// forget drops a client from the process cache without killing it again.
func (h *Host) forget(target *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, c := range h.running {
		if c == target {
			delete(h.running, key)
			return
		}
	}
}
