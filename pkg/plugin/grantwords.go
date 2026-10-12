package plugin

import "strconv"

// UntilAProfile is the clause that makes "needs no grant" true of a read that
// takes a profile.
//
// A read is free of a grant on the operator's base connection and costs one
// the moment a call names a profile (internal/grant.Required): the connection
// is what consent is about, so every capability an agent can call on one needs
// a grant, whatever its safety class. "None needs a grant" is a sentence about
// the first and says nothing of the second, which for a plugin of nothing but
// reads that talks to a service is the only way it is ever used — an install
// prompt, a search result and a detail page each said it, and a person reading
// them took the plugin for one that could never be refused an agent.
const UntilAProfile = "until a profile names one"

// GrantNeedLine is how many capabilities need a grant, worded the one way every
// line that counts them words it: "none needs a grant until a profile names
// one", "1 needs a grant", "3 need a grant".
//
// takesProfile says that something counted over can name a profile
// (Profilable), which is what makes "none" conditional. Only "none" is: a
// count above zero is true as it stands — those need a grant on any
// connection, and the reads beside them are the profile's — and the clause is
// the short thing to say, where the long one would be "and the reads beside
// them, until...".
func GrantNeedLine(n int, takesProfile bool) string {
	switch {
	case n == 0 && takesProfile:
		return "none needs a grant " + UntilAProfile
	case n == 0:
		return "none needs a grant"
	case n == 1:
		return "1 needs a grant"
	}
	return strconv.Itoa(n) + " need a grant"
}
