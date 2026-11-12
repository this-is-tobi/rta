package plugin

import "strings"

// CredentialSources names where this call reads its credential input from,
// for a refusal that has to say where one goes: the variable the host reads,
// the setting, and the store a profile's secrets: maps from. capability is the
// calling capability's ID, whose namespace the variable is under.
//
// **The variable is the profile's own while a profile is in play.** The host
// reads RTA_PROFILE_<PROFILE>_<INPUT> for one (ProfileEnvVar) and
// RTA_<NS>_<INPUT> not at all, since Resolve switches the namespace-wide layer
// off whenever a profile is active, so a refusal naming the latter sent its
// reader to export a variable the call never reads. A labelled instance,
// profile/label, has no variable: the host derives none for one, since a
// spelling like that could be forged by naming another profile carefully, and
// its credential comes from secrets: alone.
//
// Lifted from the one plugin that had this right. Six others named a variable
// the call does not read.
func CredentialSources(req Request, capability, input string) string {
	sf := req.Surface()
	profile := req.Profile()
	switch {
	case profile == "":
		return "$" + LocalEnvVar(capability, input) + " or " + sf.SettingName(input) +
			", or mapped from the store in a profile's secrets:"
	case strings.Contains(profile, "/"):
		return sf.SettingName(input) + ", or mapped from the store in profile " + profile + "'s secrets:"
	}
	return "$" + ProfileEnvVar(profile, input) + " or " + sf.SettingName(input) +
		", or mapped from the store in profile " + profile + "'s secrets:"
}
