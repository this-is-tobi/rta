package app

import (
	"strings"
	"testing"
)

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
