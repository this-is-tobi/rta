package profile

import (
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// AdHocName is what a connection typed on one command line is called, in
// every sentence that names the environment a call reached and in the profile
// argument a paste of its hint would carry.
//
// **Not a profile reference, on purpose.** A word would be: `ad-hoc` is a name
// a person can give a profile tomorrow, and a hint pasted then would reach it
// without a word. A space is refused as a reference (config.ValidRef), so the
// paste of a call that cannot be reproduced from its name fails where it is
// pasted, naming what is wrong, instead of reaching somewhere else.
const AdHocName = "ad hoc"

// AdHoc holds a connection typed on one command line — --kube, --secret,
// --secrets-from — to what the same connection stated in a profile is held to,
// less the parts that are about a name a file keeps.
//
// **What is the same.** The coordinate has to parse, a credential is a
// reference to one and never the credential, a `kube:` reference has to say
// which cluster holds the Secret, the forward and a `secrets-from:` are two
// answers to one question and not both, and the plugin has to declare an input
// a forward can fill — otherwise the forward is opened, fills nothing, and the
// call reaches the plugin's own default under a banner naming the cluster. The
// checks are Lookup's, called from the one place that has them, so a profile
// and a command line cannot disagree about what is a valid connection.
//
// **What is not.** The pin: a profile names a plugin by a key a $PATH impostor
// could answer to, and the pin is what keeps the stated connection from
// reaching it. Here the plugin is the one whose capability is being run, which
// registration already resolved and trust already approved by digest, so there
// is no name left to be taken for another.
//
// **Nothing here grants anything, and nothing here reaches an agent.** The
// flags are a person's at a terminal, who needs no grant for their own
// machine, and they exist on no tool an MCP client can call: over MCP a
// connection is a profile, named by what a person consented to.
func AdHoc(c plugin.Capability, conn config.Connection, inst Installed) *view.Error {
	ns := plugin.Namespace(c.ID)
	if !plugin.Profilable(c) {
		return view.Errorf("core.adhoc.unusable", "%s has no input a connection could fill", c.ID).
			WithHint("the connection flags overlay inputs a plugin offered to configuration; this capability offers none")
	}
	if bad := conn.BadSecretRefs(); len(bad) > 0 {
		return view.Errorf("core.adhoc.secret.scheme", "%s names no source", bad[0]).
			WithHint("write it as `kv:<entry>`, or `kube:<secret>/<key>` for a credential in the cluster — " +
				"a bare name would be ambiguous the day a second source exists")
	}
	if verr := checkTunnel(conn); verr != nil {
		return view.Errorf("core.adhoc.tunnel", "%s", verr.Message).WithHint(verr.Hint)
	}
	if fillable, declRead := tunnellable(ns, inst); conn.Tunnelled() && declRead && !fillable {
		return view.Errorf("core.adhoc.untunnellable",
			"%s declares no input a tunnel can fill, so the forward would be opened and ignored", ns).
			WithHint("the call would reach the plugin's own destination, not the forward: " +
				"a plugin that predates endpoint roles needs rebuilding")
	}
	if problems := checkSecretRefs(AdHocName, ns, conn, ns, inst); len(problems) > 0 {
		return view.Errorf("core.adhoc.secrets", "%s", problems[0].Reason).WithHint(problems[0].Hint)
	}
	return nil
}
