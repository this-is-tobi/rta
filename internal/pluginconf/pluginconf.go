// Package pluginconf answers one question: which of the operator's stated
// values does this plugin get?
//
// The answer is not "the ones under its namespace", and that is the whole
// reason this package exists. A plugin's namespace comes from its own
// declaration, decoded off the wire — internal/pluginhost/discover.go says so
// in as many words: "Name is what comes after the prefix. It is NOT the
// plugin's namespace: the namespace comes from Describe, over the wire."
// Registration is first-come and $PATH decides the order, so a binary in any
// directory ahead of the real one can declare Name: "pg" and win. It can
// already impersonate pg.query and receive whatever somebody types. What it
// must not also get is the operator's stated values, unprompted, on the
// dashboard's five-second timer, forever.
//
// So a section is keyed on the artifact, using the pin grammar an operator
// already knows from `rta plugin trust`:
//
//	plugins:
//	  sys:                  # built-in: no pin, and a pin is refused
//	    ...
//	  pg@1a2b3c4d5e6f:      # on $PATH: a pin, prefix-matched against the digest
//	    host: db.internal
//
// The three branches are Options.destructiveAllowed's, deliberately: an
// unknown namespace is refused rather than assumed harmless, a built-in
// refuses a pin because it names an artifact with no separate identity, and
// an external plugin without a matching pin gets nothing.
package pluginconf

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// minPinLen matches internal/profile's, internal/plugintrust's and
// internal/mcp's own digest-prefix floor: short enough to type, long enough
// that grinding a second artifact to collide with it is not realistic.
const minPinLen = 8

// Resolver holds the sections that survived the pin check, by namespace.
type Resolver struct {
	sections map[string]map[string]any
}

// Problem is one stated thing rta could not honour, and what to do about it.
//
// Reported rather than fatal. A config file that names a plugin which is not
// installed today is an ordinary state — the operator uninstalled it, or has
// not installed it yet, or is sharing one file across machines — and refusing
// to start over it would make config a liability. `rta doctor` prints these.
type Problem struct {
	Section string
	Reason  string
	Hint    string
}

func (p Problem) String() string {
	if p.Hint == "" {
		return fmt.Sprintf("plugins.%s: %s", p.Section, p.Reason)
	}
	return fmt.Sprintf("plugins.%s: %s (%s)", p.Section, p.Reason, p.Hint)
}

// Origin is what Resolve needs to know about a namespace: where it came from,
// and whether rta has heard of it at all. registry.Registry.Origin has this
// shape, and so does mcp.Options.Origin — one accessor, three readers.
type Origin func(namespace string) (registry.Origin, bool)

// Resolve matches every stated section to the artifact it names.
func Resolve(cfg config.Config, origin Origin) (*Resolver, []Problem) {
	r := &Resolver{sections: map[string]map[string]any{}}
	var problems []Problem

	// Sorted, so `rta doctor` prints the same order twice running and a
	// diff of two machines' output is about the machines.
	names := make([]string, 0, len(cfg.Plugins))
	for name := range cfg.Plugins {
		names = append(names, name)
	}
	sort.Strings(names)

	// First, and structurally before origin() is consulted for any section: a
	// file rta has decided not to honour must not be able to ask questions of
	// the installation. Every arm below answers one — whether a namespace is
	// registered at all, and in three of them the digest of the artifact that
	// is installed, handed over as the fix to paste. `rta doctor` prints
	// those and app.ConfigNotApplied puts them on a failing call's Hint.
	// internal/profile's Lookup already refuses to let a refusal become "an
	// oracle for the operator's connection inventory"; a plugins: section is
	// that same question asked about artifacts.
	//
	// Reported and not fatal, one Problem per section, which is Problem's own
	// severity and internal/profile.Check's own wording for the identical
	// state. The section's values simply never reach a capability, which is
	// what a stale pin already produces; refusing to start would let a file in
	// whatever directory somebody is standing in stop rta from running.
	//
	// An early return rather than a first switch arm, because `o, known :=
	// origin(ns)` is evaluated above the switch: an arm would still query the
	// registry on behalf of a file rta has refused. It also gives one true
	// reason per section instead of a wrong one — an untrusted unpinned `pg:`
	// would otherwise be told to write `pg@1a2b3c4d5e6f`, an instruction that
	// fixes nothing.
	if !cfg.Trusted() {
		for _, section := range names {
			problems = append(problems, Problem{Section: section,
				Reason: "read from a working-directory config file, so it is not honoured",
				Hint:   "set $RTA_CONFIG to name this file deliberately"})
		}
		return r, problems
	}

	for _, section := range names {
		ns, pin, pinned := strings.Cut(section, "@")
		o, known := origin(ns)
		switch {
		case !known:
			problems = append(problems, Problem{Section: section,
				Reason: fmt.Sprintf("no plugin named %q is registered", ns),
				Hint:   "`rta plugin list` shows what is installed"})
		case !o.External():
			// Built-in. A pin would name an artifact that has no separate
			// identity, so accepting one would imply a check that is not
			// happening.
			if pinned {
				problems = append(problems, Problem{Section: section,
					Reason: fmt.Sprintf("%q is built in and has no artifact to pin", ns),
					Hint:   "write it as `" + ns + ":`"})
				continue
			}
			r.sections[ns] = cfg.Plugins[section]
		case !pinned:
			problems = append(problems, Problem{Section: section,
				Reason: fmt.Sprintf("%q is an installed plugin, so its config must name the artifact it is for", ns),
				Hint:   "write it as `" + ns + "@" + o.Short() + ":`"})
		case len(pin) < minPinLen:
			// Below the floor internal/profile, internal/plugintrust and
			// internal/mcp's own digest-prefix matches all share: short
			// enough to be cheap to grind, which turns "survives a rebuild
			// without silently re-trusting a different artifact" — pinning's
			// whole point — back into trusting whatever currently answers to
			// the name. An empty pin is the extreme case of this, not a
			// separate one.
			problems = append(problems, Problem{Section: section,
				Reason: fmt.Sprintf("this pin for %q is too short to trust", ns),
				Hint: "the installed one is `" + ns + "@" + o.Short() + "` — at least " +
					strconv.Itoa(minPinLen) + " hex characters"})
		case !strings.HasPrefix(o.Digest, pin):
			// A stale pin is the ordinary case after an upgrade, and saying
			// which digest is installed is the whole point.
			problems = append(problems, Problem{Section: section,
				Reason: fmt.Sprintf("this pin does not match the installed %q", ns),
				Hint:   "the installed one is `" + ns + "@" + o.Short() + "`"})
		default:
			r.sections[ns] = cfg.Plugins[section]
		}
	}
	return r, problems
}

// For returns the operator's values for one namespace, or nil.
//
// nil is the answer for every plugin that stated nothing, every plugin whose
// pin did not match, and every plugin rta has not heard of — the three cases
// are deliberately indistinguishable here, because at the point of a call
// they mean the same thing and the difference is a diagnostic, not a branch.
func (r *Resolver) For(namespace string) map[string]any {
	if r == nil {
		return nil
	}
	return r.sections[namespace]
}

// RawSection returns whatever is written for namespace, under whichever
// heading names it, regardless of whether that heading's pin matches what is
// installed now.
//
// Never call this to decide what a capability runs with — For is the only
// path that enforces the pin, and rightly so (Resolve's whole argument is
// above). This exists for the one caller that is itself the mechanism by
// which an operator re-examines and re-authorises those values: an
// interactive config editor, which needs to show a stale section's values
// in order to fix the stale pin, not the declared defaults For would hand
// back for the same namespace.
//
// It does not enforce the file's provenance either, and that is the same
// decision one axis over. Resolve refuses every section of a config file
// nobody named; this one still answers, because the caller it exists for is
// the editor an operator uses to move those values somewhere that *is*
// honoured, and an editor that cannot show what is written cannot help
// anybody do that. It applies nothing. `rta doctor` is the other caller, and
// it must keep telling the truth about a file it is diagnosing.
//
// A namespace named under two headings — an old pin never cleaned up after
// an upgrade, alongside a new one — is resolved by taking the
// lexicographically last, for the same reason Resolve sorts before it
// iterates: whichever heading is picked, it is the same one twice running.
// `rta doctor` already reports every such section as its own problem, so the
// operator has visibility into the duplicate this function silently prefers
// one side of.
func RawSection(cfg config.Config, namespace string) (heading string, values map[string]any, found bool) {
	var headings []string
	for section := range cfg.Plugins {
		if ns, _, _ := strings.Cut(section, "@"); ns == namespace {
			headings = append(headings, section)
		}
	}
	if len(headings) == 0 {
		return "", nil, false
	}
	sort.Strings(headings)
	last := headings[len(headings)-1]
	return last, cfg.Plugins[last], true
}

// Check reports stated values the catalogue cannot use: a key no input
// declares, and a value outside an input's declared Options.
//
// Separate from Resolve because it needs the declarations and Resolve needs
// only the origins, and because these are reported once by `rta doctor`
// rather than on every call. A capability that ran and silently ignored a
// stated value is the failure this exists to prevent: the operator's file is
// right in their eyes, and nothing anywhere says otherwise.
func (r *Resolver) Check(reg *registry.Registry) []Problem {
	if r == nil || reg == nil {
		return nil
	}
	var problems []Problem
	namespaces := make([]string, 0, len(r.sections))
	for ns := range r.sections {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)

	for _, ns := range namespaces {
		readers := map[string][]plugin.Field{}
		for _, c := range reg.Capabilities() {
			if !strings.HasPrefix(c.ID, ns+".") {
				continue
			}
			for _, f := range c.Inputs {
				if f.Config != "" {
					readers[f.Config] = append(readers[f.Config], f)
				}
			}
		}
		for _, key := range flatten(r.sections[ns], "") {
			if len(readers[key]) == 0 {
				problems = append(problems, Problem{Section: ns,
					Reason: fmt.Sprintf("nothing in %q reads %q", ns, key),
					Hint:   "`rta explain` lists the inputs a capability takes"})
				continue
			}
			f := SharedField(readers[key])
			v, _ := lookup(r.sections[ns], key)
			// Before the Options check, and reported here for the reason the
			// rest of this function exists: a value whose type the handler
			// cannot read was not ignored, it was read as the zero — a
			// quoted `"true"` under a `tls` key left the connection
			// unencrypted while the file said otherwise. The host refuses
			// every call reading it now; this says so once, before any
			// call does.
			if problem, hint := plugin.StatedTypeProblem(f, v); problem != "" {
				problems = append(problems, Problem{Section: ns,
					Reason: key + " " + problem, Hint: hint})
				continue
			}
			// Every capability reading it holds a number to its own range, so
			// one outside all of them runs everywhere with a bound nobody
			// wrote — worth saying, since the file then does not mean what it
			// says anywhere. Not echoed, like the type problem above.
			if bounds := f.Bounds(); bounds != "" {
				if _, ok := f.Range(v); !ok {
					problems = append(problems, Problem{Section: ns,
						Reason: key + " is outside what every capability reading it takes, " +
							"so each runs with its own nearest bound instead",
						Hint: "write a value " + bounds})
					continue
				}
			}
			if len(f.Options) == 0 {
				continue
			}
			// Matched the way a run matches it, in any case: `BASE32` runs as
			// base32, and a report calling it invalid sent the operator to
			// fix a file every call already reads correctly.
			for _, got := range OptionValues(v) {
				if _, named := f.CanonicalOption(got); got != "" && !named {
					problems = append(problems, Problem{Section: ns,
						Reason: fmt.Sprintf("%s = %q is not one of the values %q accepts", key, got, f.Name),
						Hint:   "one of: " + strings.Join(f.Options, ", ")})
					break
				}
			}
		}
	}
	return problems
}

// OptionValues is a stated value as the option strings a run compares: one
// for a scalar, each element of a list. A shape the type check above already
// passed. Exported for internal/profile, which holds a profile's `set:` to
// the same rule this holds a plugins: section to.
func OptionValues(v any) []string {
	switch list := v.(type) {
	case []any:
		out := make([]string, 0, len(list))
		for _, e := range list {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case []string:
		return list
	}
	return []string{fmt.Sprint(v)}
}

// SharedField is what a config key accepts when several inputs read it: the
// widest range any of them allows, and every option any of them offers — or
// no Options at all when one of them takes free text. The rest is the first
// reader's, which for the ordinary case of one declaration copied across a
// namespace is every reader's.
//
// **One key is not one input.** A namespace's key serves every capability
// that declares it, and they need not agree about it: net's `timeout` is read
// by six capabilities with three different maxima, fs's `limit` by two, pg's
// by five with four. plugin.Resolve holds a number from the file inside each
// reader's own range, so a value some reader accepts is one every reader can
// run with, and the value to refuse — or, for doctor, to report — is one no
// reader accepts. Held to one field instead, as the three writers of a key
// each were — doctor to the last declared, `rta profile set` to the last by
// ID, the TUI's config editor to the first — a writer refused values most of
// the namespace takes and wrote ones the rest refused on every call, and the
// three disagreed with each other about the same key.
//
// The widest range rather than the union of them, because a Field holds one
// interval: two readers bounded 1..10 and 20..30 would let 15 through, which
// each then holds to its own nearest bound. No declaration does that.
func SharedField(readers []plugin.Field) plugin.Field {
	if len(readers) == 0 {
		return plugin.Field{}
	}
	f := readers[0]
	f.Options = nil
	free := false
	for _, r := range readers {
		if len(r.Options) == 0 {
			free = true
		}
		for _, o := range r.Options {
			if _, named := f.CanonicalOption(o); !named {
				f.Options = append(f.Options, o)
			}
		}
		f.Min = widest(f.Min, r.Min, false)
		f.Max = widest(f.Max, r.Max, true)
		f.Required = f.Required && r.Required
	}
	if free {
		f.Options = nil
	}
	return f
}

// widest is the looser of two bounds: nil, which is none, beats anything.
func widest(a, b any, upper bool) any {
	x, okA := number(a)
	y, okB := number(b)
	if !okA || !okB {
		return nil
	}
	if (upper && y > x) || (!upper && y < x) {
		return b
	}
	return a
}

// number reads a declared bound, which Validate has already held to being a
// number of one of these shapes.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// flatten lists the dotted keys that hold a value, deepest-first, so a nested
// block is reported by the leaves an input could actually name.
func flatten(m map[string]any, prefix string) []string {
	var out []string
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		full := k
		if prefix != "" {
			full = prefix + "." + k
		}
		switch v := m[k].(type) {
		case map[string]any:
			out = append(out, flatten(v, full)...)
		case map[any]any:
			conv := make(map[string]any, len(v))
			for kk, vv := range v {
				conv[fmt.Sprint(kk)] = vv
			}
			out = append(out, flatten(conv, full)...)
		default:
			out = append(out, full)
		}
	}
	return out
}

func lookup(m map[string]any, key string) (any, bool) {
	cur := any(m)
	for _, seg := range strings.Split(key, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mm[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
