package grant

import (
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// ReserveNaming answers exactly as Reserve does and names the grants that
// covered the call, whether or not they are metered.
func TestReserveNamesTheGrantsThatCovered(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	c := declare("kv.get", plugin.Write, "key", true)
	if verr := Issue(Grant{Target: "kv.get", Scope: "db", Role: "dev", Issued: time.Now(), Expires: time.Now().Add(time.Hour)}, true); verr != nil {
		t.Fatal(verr)
	}
	release, covering, verr := ReserveNaming(c, map[string]any{"key": "db"}, Caller{})
	if verr != nil {
		t.Fatal(verr)
	}
	release()
	if len(covering) != 1 || covering[0].Role != "dev" {
		t.Fatalf("covering = %+v, want the dev grant", covering)
	}
	// A metered grant, through the locked path.
	if verr := Issue(Grant{Target: "kv.get", Scope: "api", Role: "ops", MaxUses: 2, Issued: time.Now(), Expires: time.Now().Add(time.Hour)}, true); verr != nil {
		t.Fatal(verr)
	}
	release, covering, verr = ReserveNaming(c, map[string]any{"key": "api"}, Caller{})
	if verr != nil {
		t.Fatal(verr)
	}
	release()
	if len(covering) != 1 || covering[0].Role != "ops" {
		t.Fatalf("metered covering = %+v, want the ops grant", covering)
	}
	if _, covering, verr := ReserveNaming(c, map[string]any{"key": "nothing"}, Caller{}); verr == nil || covering != nil {
		t.Fatalf("an uncovered call named %+v, %v", covering, verr)
	}
}
