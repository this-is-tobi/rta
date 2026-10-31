package grant

import (
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Reaches reports whether this grant covers calls of c: c is the capability it
// names, or inside the plugin it names; it is one an agent can call at all, and
// one that needs this grant (Required); and a grant that names a profile
// covers only a capability a call can name one on (plugin.Profilable), since
// the grant matches the connection exactly and no call of the others has any.
//
// What a grant is worth, not whether one call may run: covers decides a call,
// and nothing here is read by the gate. Counted by it are the receipt and the
// roster's advisory, which say how far a plugin-wide grant reaches, and the
// refusal that offers one (WithPluginWide), so the three cannot count the same
// grant differently.
func (g Grant) Reaches(c plugin.Capability) bool {
	if c.HumanOnly || !Required(c, g.Profile) {
		return false
	}
	if g.Profile != "" && !plugin.Profilable(c) {
		return false
	}
	return c.ID == g.Target || (!strings.Contains(g.Target, ".") && Namespace(c.ID) == g.Target)
}

// ReachOf is what a grant covers in catalog, by the three counts the receipt
// gives of a plugin-wide one and the roster repeats: every capability it
// reaches, the destructive among them, and the ones that reveal a stored value.
// Each is sorted, so a sentence built from them reads the same every time.
func ReachOf(catalog []plugin.Capability, g Grant) (gated, destructive, reveals []string) {
	for _, c := range catalog {
		if !g.Reaches(c) {
			continue
		}
		gated = append(gated, c.ID)
		if c.Safety == plugin.Destructive {
			destructive = append(destructive, c.ID)
		}
		if c.Reveals {
			reveals = append(reveals, c.ID)
		}
	}
	sort.Strings(gated)
	sort.Strings(destructive)
	sort.Strings(reveals)
	return gated, destructive, reveals
}

// DestructiveSummary counts the destructive capabilities and names the first
// few. Never "and 1 other": the fourth name is shorter than saying there is one.
func DestructiveSummary(ids []string) string {
	if len(ids) == 0 {
		return "none destructive"
	}
	shown := ids[:min(3, len(ids))]
	if len(ids) == 4 {
		shown = ids
	}
	which := strconv.Itoa(len(ids)) + " destructive (" + strings.Join(shown, ", ")
	if rest := len(ids) - len(shown); rest > 0 {
		which += " and " + format.Count(rest, "other", "others")
	}
	return which + ")"
}

// WithPluginWide is a refusal for a call over a profile with one more command
// after the one it hands on: the grant on the whole plugin over that profile,
// followed by what it covers, in the receipt's words.
//
// **The refusal names the capability, and this is the only place the plugin
// is offered.** The gate's hint used to name the plugin whenever a profile was
// in play, on the reasoning that someone granting a connection rarely means
// "and only the status call". That made the command an agent relays for a read
// one that lets it delete too: prom_overview on a profile was refused with
// `rta grant allow prom --profile scratch`, and the operator who pasted it had
// let prom.series.rm through as well. The receipt said so, once the grant
// stood. So the command handed on first is the call's own, and the wider one
// comes second with how many capabilities it covers and which of them destroy
// something, read before it is issued rather than after.
//
// Added here and not in the gate, because counting needs the catalogue and
// the gate has only the capability it was asked about; and added to the
// refusal rather than worked into the decision, so the catalogue can change
// what the operator is told and never what the call is allowed.
//
// Offered only one step from the call. A call that names a record is refused
// with a grant on that record, and a grant on every record of every capability
// is a much wider question (the receipt's revealNote asks it); and a plugin
// whose grant over the profile reaches nothing but the call's own capability
// has nothing wider to offer.
//
// What it adds depends on the call, the catalogue and the names the call
// carries, never on whether the profile exists: a call naming an unknown
// profile has to be refused in the words an ungranted one gets, so that an
// agent cannot list the operator's connections one call at a time.
func WithPluginWide(verr *view.Error, c plugin.Capability, values map[string]any, catalog []plugin.Capability, by Caller) *view.Error {
	if verr == nil || verr.Code != "core.grant.required" || by.Profile == "" {
		return verr
	}
	if named := Scopes(c, values); len(named) != 1 || named[0] != "" {
		return verr
	}
	ns := Namespace(c.ID)
	gated, destructive, _ := ReachOf(catalog, Grant{Target: ns, Profile: by.Profile})
	if len(gated) < 2 {
		return verr
	}
	out := *verr
	out.Hint += "; or, for every " + ns + " capability on that profile, " +
		plugin.AskOperator(allowCommand(ns, "", by.Profile, by.Agent)) + ", which covers " +
		format.Count(len(gated), "capability", "capabilities") + " an agent needs a grant for, " +
		DestructiveSummary(destructive)
	return &out
}
