package app

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A connection typed on one command line.
//
// A profile is how a connection is *kept*: named, stated in a file, the unit a
// grant names, switched on for a morning. Most of what a person does against a
// database in a cluster is not that. It is "look at the replication of the one
// in `databases`, once, now", and the choice used to be a profile written for
// the purpose or kubectl and psql and copying a password by hand — which is how
// credentials reach shell histories.
//
//	rta pg replication --kube prod/databases/svc/postgres:5432 \
//	    --secret password=kube:postgres-app/password --user app
//
// is the same connection a profile states, as flags: the forward is raised for
// the one call and torn down after it, the credential is read from the Secret
// in the namespace the coordinate names and never printed, and the rest of the
// connection is the capability's own flags. Nothing is written anywhere, so
// there is nothing to remember to clean up, and `rta profile set` is the way to
// keep one that turns out to be worth keeping.
//
// **A person's act, on a person's machine, and for nobody else.** The flags
// exist on the command line and on no MCP tool, so an agent cannot name a
// cluster, a Service or a Secret: over MCP a connection is a profile, named by
// something a person consented to, and `rta pg ... --kube` in a shell is that
// person, who already holds the kubeconfig the forward uses. The TUI's form
// states the same connection for the same reason. Nothing here is a grant, and
// none of it is recorded as an agent's call.

// addAdHocFlags gives a connection-bearing capability the flags that state a
// connection on its own command line.
//
// Per capability and only where a forward has something to fill — an input
// that declares an endpoint role — for the reason --profile is: a flag that
// exists everywhere and does something nowhere teaches that flags are
// decoration. `rta audit kube eol` reaches its cluster through the kubeconfig
// and has no address a coordinate could replace, so it has none of these.
// Lookup first, because pflag panics on a redefined flag and an input that
// declared one of these names would otherwise take the whole command tree with
// it.
func addAdHocFlags(cmd *cobra.Command, c plugin.Capability) {
	if !plugin.Profilable(c) || !plugin.Tunnellable([]plugin.Capability{c}) {
		return
	}
	for _, name := range []string{"kube", "secret", "secrets-from"} {
		if cmd.Flags().Lookup(name) != nil {
			return
		}
	}
	cmd.Flags().String("kube", "", "reach it through a port-forward raised for this call: "+
		"`context/namespace/kind/name:port` (kind is svc or pod)")
	// StringArray, as profile set's is: StringSlice would split a reference on
	// its commas.
	cmd.Flags().StringArray("secret", nil, "an `input=reference` for this call: kube:<secret>/<key> reads "+
		"it from the cluster, kv:<entry> from the local store — never the credential itself")
	cmd.Flags().String("secrets-from", "", "which cluster `context/namespace` holds the Secrets --secret reads, "+
		"when the service is reached directly and not through --kube")
}

// adHocConnection is the connection the command line states, and whether it
// states one at all. A command line that names none returns false and nothing
// else, which is every call there has ever been.
//
// Refused beside --profile, and not ranked: a profile and a coordinate are two
// answers to where the call goes, and a reader of the command could not tell
// which won without looking it up.
func adHocConnection(cmd *cobra.Command, c plugin.Capability, explicit string) (config.Connection, bool, *view.Error) {
	if cmd.Flags().Lookup("kube") == nil || cmd.Flags().Lookup("secret") == nil {
		return config.Connection{}, false, nil
	}
	conn := config.Connection{
		Kube:        strings.TrimSpace(mustString(cmd, "kube")),
		SecretsFrom: strings.TrimSpace(mustString(cmd, "secrets-from")),
	}
	pairs, _ := cmd.Flags().GetStringArray("secret")
	if len(pairs) > 0 {
		secrets, verr := parseSecretFlags(pairs)
		if verr != nil {
			return config.Connection{}, false, verr
		}
		conn.Secrets = secrets
	}
	if conn.Kube == "" && conn.SecretsFrom == "" && len(conn.Secrets) == 0 {
		return config.Connection{}, false, nil
	}
	if explicit != "" {
		return config.Connection{}, false, view.Errorf("core.adhoc.both",
			"--profile and the connection flags are two answers to where this call goes").
			WithHint("name a profile, or state the connection with --kube and --secret — " +
				"`rta profile set " + explicit + " --plugin " + plugin.Namespace(c.ID) + " --kube ...` keeps one")
	}
	if verr := profile.AdHoc(c, conn, installed); verr != nil {
		return config.Connection{}, false, verr
	}
	return conn, true, nil
}

// adHocWords says in a few words where a command line that states a connection
// sends the call, and whether it states one: what the badge above its result
// names. Read from the flags as typed, before anything has checked them, so a
// connection that is about to be refused still says where it was going.
func adHocWords(cmd *cobra.Command) (string, bool) {
	if cmd == nil || cmd.Flags().Lookup("kube") == nil || cmd.Flags().Lookup("secret") == nil {
		return "", false
	}
	secrets, _ := cmd.Flags().GetStringArray("secret")
	words := profile.AdHocWords(strings.TrimSpace(mustString(cmd, "kube")),
		strings.TrimSpace(mustString(cmd, "secrets-from")), len(secrets))
	return words, words != ""
}
