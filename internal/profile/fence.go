package profile

import (
	"fmt"
	"slices"
	"sort"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// This file is the fence a profile can put around a connection: the `only:`
// block of a plugin entry, which says per config key what the connection
// reaches.
//
// `set:` supplies a default and the caller wins, which is the right rule for a
// host and the wrong one for "this environment is that namespace and no other".
// Everything here is about the second statement: reading the block, holding it
// to the plugin's declaration, and holding a call to it.

// checkOnly reports what is wrong with an entry's `only:` block, in the same
// terms checkSet uses for `set:` so the two blocks that are keyed by
// Field.Config are refused the same way for the same mistakes: a key nothing
// reads, an entry of the wrong type, a forward that would override it.
//
// Then the rules only a fence has. A `set:` value outside its own list is a
// value the entry states and the host would refuse on every call, which is a
// default nobody could use and so a mistake rather than a statement. A
// `secrets:` mapping onto a fenced input is refused because the credential is
// fetched after the fence is checked — the check is made before anything is
// opened for a call that may yet be refused — so the value it brings would be
// one the fence never saw. And a `set:` that turns on a switch the plugin says
// widens a fenced input is the same contradiction as a value outside the list,
// stated through the other door.
//
// Feeds Lookup as well as the reports, so a fence that does not hold is not
// half-applied on one surface and honoured on another: the rule this package
// states once at Check and has kept repeating.
func checkOnly(name, key string, conn config.Connection, ns string, inst Installed) []Problem {
	if len(conn.Only) == 0 {
		return nil
	}
	readers := fillableReaders(ns, inst)
	declared := declaredKeys(ns, inst)
	keys := make([]string, 0, len(conn.Only))
	for k := range conn.Only {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var problems []Problem
	add := func(reason, hint string) {
		problems = append(problems, Problem{Name: name, Plugin: key, Reason: reason, Hint: hint})
	}
	for _, k := range keys {
		if len(readers[k]) == 0 {
			problems = append(problems, unreadKey(name, key, k, ns, "only", readers, declared))
			continue
		}
		f := pluginconf.SharedField(readers[k])
		// A forward that fills the key makes its fence a statement about a
		// value no call resolves to, the way checkSet refuses the `set:` line
		// beside it: said for the same reason, and before the list's own shape,
		// which is not the more fundamental complaint.
		overridden := slices.ContainsFunc(readers[k], func(r plugin.Field) bool {
			return r.Endpoint != plugin.EndpointNone || r.TLSAdjacent
		})
		if conn.Tunnelled() && overridden {
			add(fmt.Sprintf("`only: %s` fences a value the forward `%s:` opens overrides", k, conn.TunnelKey()),
				"remove it, or remove `"+conn.TunnelKey()+":` to connect directly")
			continue
		}
		if problem, hint := plugin.FenceProblem(f, conn.Only[k]); problem != "" {
			add(fmt.Sprintf("`only: %s` %s", k, problem), hint)
			continue
		}
	}
	for _, k := range SetOutsideFence(conn, ns, inst) {
		add(fmt.Sprintf("`set: %s` is not one of the values `only: %s` allows", k, k),
			"add it to the list, or set one the list holds — a default the fence refuses on every call is not a default")
	}
	problems = append(problems, fencedSecretMappings(name, key, conn, readers)...)
	problems = append(problems, fencedWideners(name, key, conn, ns, inst)...)
	return problems
}

// SetOutsideFence lists the config keys whose `set:` value is not one of the
// values the same entry's `only:` allows, sorted.
//
// Exported for the one writer that edits `set:` and not `only:` — the TUI's
// connection form — which has to refuse the value it is about to write rather
// than save an entry every call then refuses, and cannot be sent to a flag it
// does not have. One function, so what the form refuses and what the loader
// refuses cannot be two rules.
//
// Only the keys it can judge: a list that is not a well-formed allowlist is
// FenceProblem's to report, and a value of the wrong shape is checkSet's, so
// neither earns a second complaint here.
func SetOutsideFence(conn config.Connection, ns string, inst Installed) []string {
	if len(conn.Only) == 0 || len(conn.Set) == 0 {
		return nil
	}
	readers := fillableReaders(ns, inst)
	var outside []string
	for k, stated := range conn.Set {
		list, fenced := conn.Only[k]
		if !fenced || len(readers[k]) == 0 {
			continue
		}
		f := pluginconf.SharedField(readers[k])
		if problem, _ := plugin.FenceProblem(f, list); problem != "" {
			continue
		}
		if problem, _ := plugin.StatedTypeProblem(f, stated); problem != "" {
			continue
		}
		allowed, _ := plugin.FenceList(list)
		if !plugin.FenceAllows(f, allowed, stated) {
			outside = append(outside, k)
		}
	}
	sort.Strings(outside)
	return outside
}

// fencedSecretMappings reports a `secrets:` reference aimed at an input the
// entry fences.
//
// Refused, not ordered: Fill runs after the fence is checked — a credential is
// not fetched and a forward is not opened for a call that is about to be
// refused — so what the reference brings is a value the fence never saw, and
// the call would run with it. An allowlist of credentials is not a thing
// either: the list would be the secret, written into the file the secret was
// kept out of.
func fencedSecretMappings(name, key string, conn config.Connection, readers map[string][]plugin.Field) []Problem {
	var problems []Problem
	for _, ref := range conn.SecretRefs() {
		for k, fields := range readers {
			if _, fenced := conn.Only[k]; !fenced {
				continue
			}
			if !slices.ContainsFunc(fields, func(f plugin.Field) bool { return f.Name == ref.Input }) {
				continue
			}
			problems = append(problems, Problem{Name: name, Plugin: key,
				Reason: fmt.Sprintf("`secrets: %s` fills an input `only: %s` fences, and a reference is fetched "+
					"after the fence is checked, so the fence would never see its value", ref.Input, k),
				Hint: "remove the mapping, or remove the fence — a fence is for a value written in the profile"})
		}
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Reason < problems[j].Reason })
	return problems
}

// fencedWideners reports a `set:` line that turns on a switch the plugin
// declares widens an input this entry fences (plugin.Field.Widens).
//
// Walked off the declarations, per capability, because Widens names a sibling
// of the same capability and the key it widens is that sibling's own: two
// capabilities of one plugin may each declare the switch beside a different
// input, and the profile's `set:` key is shared between them.
func fencedWideners(name, key string, conn config.Connection, ns string, inst Installed) []Problem {
	var problems []Problem
	seen := map[string]bool{}
	for _, c := range capabilitiesOf(inst) {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, w := range c.Inputs {
			if w.Widens == "" || w.Config == "" || !plugin.ProfileFillable(c, w) {
				continue
			}
			if on, _ := conn.Set[w.Config].(bool); !on {
				continue
			}
			i := slices.IndexFunc(c.Inputs, func(f plugin.Field) bool { return f.Name == w.Widens })
			if i < 0 || c.Inputs[i].Config == "" {
				continue
			}
			target := c.Inputs[i].Config
			if _, fenced := conn.Only[target]; !fenced || seen[w.Config+"|"+target] {
				continue
			}
			seen[w.Config+"|"+target] = true
			problems = append(problems, Problem{Name: name, Plugin: key,
				Reason: fmt.Sprintf("`set: %s` turns on a switch that widens %s, which `only: %s` fences",
					w.Config, target, target),
				Hint: "remove the line — a fenced input is read at the values the list names, not past them"})
		}
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Reason < problems[j].Reason })
	return problems
}

// declaredKeys is every config key the namespace declares on any input, a
// profile may fill it or not: what separates "nothing reads that" from "that is
// a key, and a profile may not have it".
func declaredKeys(ns string, inst Installed) map[string]bool {
	declared := map[string]bool{}
	for _, c := range capabilitiesOf(inst) {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Config != "" {
				declared[f.Config] = true
			}
		}
	}
	return declared
}
