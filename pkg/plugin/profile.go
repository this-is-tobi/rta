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

// envToken renders one segment of a derived variable name.
func envToken(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
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
