package profile

import (
	"regexp"
	"strings"

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

// AdHocWords says in a few words where a connection typed for one call sends
// it: what the line above a CLI result names, and what the TUI's environment
// picker calls the connection it holds. Taken as the three things a call can
// state, and not as a Connection, because the CLI reads them from flags as
// typed — before anything has checked them — so a connection about to be
// refused still says where it was going.
func AdHocWords(kube, secretsFrom string, secrets int) string {
	switch {
	case kube != "":
		return "through " + kube
	case secretsFrom != "":
		return "credentials from " + secretsFrom
	case secrets > 0:
		return "credentials by reference"
	}
	return ""
}

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
		return view.Errorf("core.adhoc.tunnel", "%s", inFlags(verr.Message)).WithHint(inFlags(verr.Hint))
	}
	if fillable, declRead := tunnellable(ns, inst); conn.Tunnelled() && declRead && !fillable {
		return view.Errorf("core.adhoc.untunnellable",
			"%s declares no input a tunnel can fill, so the forward would be opened and ignored", ns).
			WithHint("the call would reach the plugin's own destination, not the forward: " +
				"a plugin that predates endpoint roles needs rebuilding")
	}
	if problems := checkSecretRefs(AdHocName, ns, conn, ns, inst); len(problems) > 0 {
		return view.Errorf("core.adhoc.secrets", "%s", inFlags(problems[0].Reason)).WithHint(inFlags(problems[0].Hint))
	}
	return nil
}

// secretKeyWord matches how a profile's file names a credential's mapping in a
// sentence — `secrets.password`, "`secrets: host`" — and captures the input.
var secretKeyWord = regexp.MustCompile(`secrets[.:] ?(\w+)`)

// flagSpelling is the rest of what the shared checks say in a file's words:
// the keys a profile states a connection with, and the sentences that tell a
// reader which line to remove. Longest first, because a Replacer takes the
// first of its pairs that matches where it stands.
var flagSpelling = strings.NewReplacer(
	"`secrets-from: <context>/<namespace>`", "`--secrets-from <context>/<namespace>`",
	"`kube: <context>/<namespace>/svc/<name>:<port>`", "`--kube <context>/<namespace>/svc/<name>:<port>`",
	"`secrets-from:`", "`--secrets-from`",
	"`kube:`", "`--kube`",
	"states both ", "the call states both ",
	"remove the mapping, or remove", "drop that --secret, or drop",
)

// inFlags says in the command line's words a refusal the checks shared with a
// profile wrote in the file's. A person who typed --kube and --secret has no
// `kube:` line to drop and no `secrets:` block to edit, and a hint that says
// "drop `secrets-from:`" to them is one they cannot follow without first
// working out which flag it means. The checks stay one function, so a profile
// and a command line still cannot disagree about what is valid; only the
// words differ, and a test drives every refusal a command line can reach and
// fails on a spelling that is the file's.
//
// Here and not in the checks because the checks serve the file, the doctor and
// the profile editor, where these are the right words. The TUI form reaches
// few of these refusals — it has no secrets-from box, and its own check
// answers the cluster-reference case in the form's words before this runs.
func inFlags(s string) string {
	return flagSpelling.Replace(secretKeyWord.ReplaceAllString(s, "--secret $1"))
}
