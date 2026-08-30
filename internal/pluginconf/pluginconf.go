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
// already knows from --allow-destructive:
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
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

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
		case pin == "" || !strings.HasPrefix(o.Digest, pin):
			// An empty pin is not a prefix of everything: it is a missing
			// decision. A stale one is the ordinary case after an upgrade,
			// and saying which digest is installed is the whole point.
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
		declared := map[string]plugin.Field{}
		for _, c := range reg.Capabilities() {
			if !strings.HasPrefix(c.ID, ns+".") {
				continue
			}
			for _, f := range c.Inputs {
				if f.Config != "" {
					declared[f.Config] = f
				}
			}
		}
		for _, key := range flatten(r.sections[ns], "") {
			f, ok := declared[key]
			if !ok {
				problems = append(problems, Problem{Section: ns,
					Reason: fmt.Sprintf("nothing in %q reads %q", ns, key),
					Hint:   "`rta explain` lists the inputs a capability takes"})
				continue
			}
			v, _ := lookup(r.sections[ns], key)
			// Before the Options check, and reported here for the reason the
			// rest of this function exists: a value whose type the handler
			// cannot read is not ignored, it is read as the zero — so a
			// quoted `"true"` under a `tls` key leaves the connection
			// unencrypted while the file says otherwise. Louder than a key
			// nothing reads, and it was the one this did not look for.
			if problem, hint := plugin.StatedTypeProblem(f, v); problem != "" {
				problems = append(problems, Problem{Section: ns,
					Reason: key + " " + problem, Hint: hint})
				continue
			}
			if len(f.Options) == 0 {
				continue
			}
			got := fmt.Sprint(v)
			if !contains(f.Options, got) {
				problems = append(problems, Problem{Section: ns,
					Reason: fmt.Sprintf("%s = %q is not one of the values %q accepts", key, got, f.Name),
					Hint:   "one of: " + strings.Join(f.Options, ", ")})
			}
		}
	}
	return problems
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

func contains(options []string, v string) bool {
	for _, o := range options {
		if o == v {
			return true
		}
	}
	return false
}
