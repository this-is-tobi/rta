package app

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// TestNoInputHelpRepeatsWhatTheHostAlreadyPrints walks the built-in
// catalogue and refuses a Help string that names its own environment
// variable.
//
// `rta explain` prints ", from $RTA_KV_PASSPHRASE" from EnvFallback
// (explain.go), and flagUsage appends the same variable to --help. Three
// declarations wrote it into Help as well, one of them by calling
// LocalEnvVar itself, so the explain card named the variable twice in one
// line. There is no way to notice that by reading a declaration — the
// duplicate is two hundred lines away in another package — which is what
// makes it a drift test rather than a review note.
func TestNoInputHelpRepeatsWhatTheHostAlreadyPrints(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range reg.Capabilities() {
		for _, f := range c.Inputs {
			if !f.EnvFallback {
				continue
			}
			if env := plugin.LocalEnvVar(c.ID, f.Name); strings.Contains(f.Help, env) {
				t.Errorf("%s input %q names %s in its Help; the host already prints it "+
					"from EnvFallback — in `rta explain` and in --help — so the card says it twice. "+
					"State what the value is and let the surface say where it comes from.",
					c.ID, f.Name, env)
			}
		}
	}
}

// The environment variable a credential may arrive in is the host's to name
// on --help, generated from the declaration the way `rta explain` already
// generates its ", from $RTA_KV_PASSPHRASE" clause.
//
// Before this, --help named the variable only where a declaration had written
// it into Help by hand: three built-ins did, in a spelling of their own, and
// no plugin credential ever had — so `rta s3 object get --help` never said
// how to supply the secret key without putting it on argv. kv.rm is the
// fixture because it is a built-in whose store reads a credential from the
// environment, so the test needs no plugin process, and the spelling asserted
// is the one explain uses, which no declaration writes.
func TestCLIHelpNamesTheEnvironmentVariableForACredential(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := NewRoot(reg, "test")
	cmd, _, err := root.Find([]string{"kv", "rm"})
	if err != nil {
		t.Fatal(err)
	}
	flag := cmd.Flags().Lookup("passphrase")
	if flag == nil {
		t.Fatal("kv rm has no --passphrase, so this test checks nothing")
	}
	if !strings.Contains(flag.Usage, "$RTA_KV_PASSPHRASE") {
		t.Errorf("--passphrase usage does not name $RTA_KV_PASSPHRASE: %q", flag.Usage)
	}
}
