package profile

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
	reach := reachOfKeys(ns, inst)
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
			problems = append(problems, unreadKey(name, key, k, ns, "only", readers, reach))
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
		if _, out := outsideItsOwnList(conn, readers, k, stated); out {
			outside = append(outside, k)
		}
	}
	sort.Strings(outside)
	return outside
}

// SetFenceReason says why stated is not a value the entry's own `only:` list
// allows for key, as the clause that follows the key — "must be one of app,
// app-jobs" — or "" when it is allowed, or is not something this can judge.
//
// For the form that edits `set:` and not `only:`, which asks it of a box as the
// person leaves it, so the refusal lands on the box that holds the value like
// every other rule a box is held to, and not on the whole form after it has been
// submitted. The values are the person's own configuration, read by the person
// who wrote them, and they come first because the footer that carries the
// sentence is cut at the edge of a terminal: what cannot be cut is the answer.
// The same judgement SetOutsideFence makes, from the same function.
func SetFenceReason(conn config.Connection, ns string, inst Installed, key string, stated any) string {
	allowed, out := outsideItsOwnList(conn, fillableReaders(ns, inst), key, stated)
	if !out {
		return ""
	}
	return "must be one of " + fenceList(allowed)
}

// outsideItsOwnList is the one question SetOutsideFence and SetFenceReason ask:
// whether the entry fences key and states a value for it that the list does not
// hold, and what the list holds.
func outsideItsOwnList(conn config.Connection, readers map[string][]plugin.Field, key string, stated any,
) (allowed []any, outside bool) {
	list, fenced := conn.Only[key]
	if !fenced || len(readers[key]) == 0 {
		return nil, false
	}
	f := pluginconf.SharedField(readers[key])
	if problem, _ := plugin.FenceProblem(f, list); problem != "" {
		return nil, false
	}
	if problem, _ := plugin.StatedTypeProblem(f, stated); problem != "" {
		return nil, false
	}
	allowed, _ = plugin.FenceList(list)
	return allowed, !plugin.FenceAllows(f, allowed, stated)
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

// FenceOf is the allowlist a connection states, in the form the host holds a
// call to (plugin.Inputs.Fence). nil for a connection that fences nothing.
//
// Total, and failing closed: an entry that is not a list — which Lookup refuses
// before anything reaches here, so this is the second wall and not the first —
// becomes a fence that allows nothing, never one that is not there. A reader
// that dropped what it could not parse would turn a line the operator wrote to
// restrict a connection into silence about it.
func FenceOf(conn config.Connection) plugin.Fence {
	if len(conn.Only) == 0 {
		return nil
	}
	fence := make(plugin.Fence, len(conn.Only))
	for key, written := range conn.Only {
		list, isList := plugin.FenceList(written)
		if !isList {
			list = []any{}
		}
		fence[key] = list
	}
	return fence
}

// Fenced holds a call to the allowlist its connection states, before anything
// is fetched or opened for it, and returns the refusal when the call goes
// outside — nil when it does not, and always nil for a connection that fences
// nothing.
//
// **One check for the three places that hold the resolved values** — the CLI,
// the TUI and the MCP bridge — because the rule is only as good as the weakest
// surface that forgot it, and this repo has recorded that failure five times.
// in carries what the call itself knows: the caller's values, and the
// operator's own config and the heading it sits under. The profile's layer is
// bound here from conn, by Bind, which is also what makes the environment
// variable a profile can read for an input one of the values the check sees: a
// fence that held `set:` and not `RTA_PROFILE_<P>_<INPUT>` would be a fence the
// next export walked through.
//
// Bind and never Fill: a credential a `secrets:` block fetches fills an input
// the fence cannot see, which is why such a mapping onto a fenced input is a
// configuration error (checkOnly) and not something this has to cover.
//
// The refusal is written for a person at a terminal or a form: it names the
// input, what the connection reaches and the ways across. A caller that must
// not say so — over MCP an agent cannot enumerate what an operator's profile
// reaches, and `ungranted` is the precedent for saying nothing about it —
// uses the code and says its own sentence.
func Fenced(name string, conn config.Connection, c plugin.Capability, in plugin.Inputs,
	look func(string) (string, bool), sf plugin.Surface,
) *view.Error {
	breach := FenceBreached(name, conn, c, in, look)
	if breach == nil {
		return nil
	}
	return FenceRefusal(name, conn, c, breach, sf)
}

// FenceBreached is Fenced's check without its words: how the call goes outside
// the connection's fence, or nil. For the caller that has to say it in its own
// — the MCP bridge, whose reader must not be told what the fence holds.
func FenceBreached(name string, conn config.Connection, c plugin.Capability, in plugin.Inputs,
	look func(string) (string, bool),
) *plugin.FenceBreach {
	fence := FenceOf(conn)
	if len(fence) == 0 {
		return nil
	}
	in.Fence = fence
	in.Profile = Bind(name, conn, c, look)
	in.ProfileName = name
	return plugin.CheckFence(c, in)
}

// FenceInput names a fenced input the way a reader on sf gives it: the flag, the
// box, the argument — and for an input only the operator sets, the operator's
// setting, which an agent cannot be told to pass.
func FenceInput(f plugin.Field, sf plugin.Surface) string {
	if f.Local {
		return sf.SettingName(f.Name)
	}
	return sf.InputName(f.Name)
}

// FenceRefusal words a breach for the person who can act on it. ref is the
// profile as the call named it, instance included, and conn the connection it
// resolved to.
//
// **A blank the connection appears to answer is said as what happened.** An
// input a call names as its scope is never filled from a profile — the grant is
// checked against the record the call names, before anything is resolved — so a
// connection that states `set: namespace: apps` and fences it to `[apps]` still
// refuses a call that leaves the namespace out, and "is blank, and profile
// reaches only: apps" read as a contradiction of the line beside it. Saying that
// the connection states the value, and that a call has to name it all the same,
// is the whole of the fix; the line is not honoured (noteInertSet says so where
// it is written).
func FenceRefusal(ref string, conn config.Connection, c plugin.Capability, b *plugin.FenceBreach,
	sf plugin.Surface,
) *view.Error {
	profileName, instance := config.SplitRef(ref)
	entry := plugin.Namespace(c.ID)
	if instance != "" {
		entry += "/" + instance
	}
	// The names a fence is written under, which is not the names a call uses:
	// the key is the Config key and a person at a terminal types the flag.
	named := func(f plugin.Field) string { return FenceInput(f, sf) }
	reaches := fenceList(b.Allowed)
	cross := "name another profile that reaches it, or change what " + ref + " reaches: `rta profile set " +
		profileName + " --plugin " + entry + " --only " + b.Input.Config + "=…`"
	switch {
	case b.Widened():
		return view.Errorf("core.profile.fenced.switch",
			"%s reads past %s, which profile %q fences to: %s",
			named(b.Switch), named(b.Input), ref, reaches).
			WithHint("leave " + named(b.Switch) + " out, or " + cross).WithInput(b.Switch.Name)
	case b.Blank:
		if stated, ok := conn.Set[b.Input.Config]; ok && plugin.IsScope(c, b.Input) {
			return view.Errorf("core.profile.fenced",
				"%s is not given: profile %q states %s: %s, but a call has to name it",
				named(b.Input), ref, b.Input.Config, textclean.Terminal(fmt.Sprint(stated))).
				WithHint(c.ID + " takes it as its scope, which a profile never fills — give it one of " +
					reaches + ", or " + cross).WithInput(b.Input.Name)
		}
		return view.Errorf("core.profile.fenced",
			"%s is blank, and profile %q reaches only: %s", named(b.Input), ref, reaches).
			WithHint("give it one of those, or " + cross).WithInput(b.Input.Name)
	}
	return view.Errorf("core.profile.fenced",
		"%s is outside what profile %q reaches: %s", named(b.Input), ref, reaches).
		WithHint("give it one of those, or " + cross).WithInput(b.Input.Name)
}

// maxFenceShown is how many allowed values a refusal lists before it counts the
// rest: enough to see what a connection reaches, few enough that a fence of two
// hundred namespaces is not two hundred words on a terminal.
const maxFenceShown = 10

// fenceList is an allowlist as a sentence quotes it, cleaned for a terminal —
// the entries are the operator's, but the file is also written by scripts and
// a terminal acts on an escape in it whoever wrote it.
func fenceList(allowed []any) string {
	shown := make([]string, 0, min(len(allowed), maxFenceShown))
	for i, e := range allowed {
		if i == maxFenceShown {
			break
		}
		shown = append(shown, textclean.Terminal(fmt.Sprint(e)))
	}
	text := strings.Join(shown, ", ")
	if rest := len(allowed) - len(shown); rest > 0 {
		text += fmt.Sprintf(" … and %d more", rest)
	}
	if text == "" {
		text = "nothing"
	}
	return text
}
