package lock

import (
	"context"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The lock on every agent goes over the operator channel as the name `*`, which
// is what any server that knows the row already stores, and a window in days goes
// as the hours a server that has not learned days yet reads.
func TestTheLockOnEveryAgentCanBePlacedListedAndLiftedOnARemoteServer(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	lockServer(t)
	ctx := context.Background()

	v, err := capByID(t, "lock.add").Run(ctx, remoteReq(map[string]any{
		"all": true, "ttl": "1d", "server": "lab", "passphrase": "correct horse",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(t, v, "locked"); got != "every agent on lab" {
		t.Errorf("locked = %q", got)
	}
	locks, verr := lockdown.Load()
	if verr != nil || len(locks) != 1 || locks[0].Name != lockdown.Everyone || locks[0].Kind != lockdown.KindAgent {
		t.Fatalf("the server's store: %+v, %v", locks, verr)
	}
	if left := time.Until(locks[0].Expires); left < 23*time.Hour || left > 25*time.Hour {
		t.Errorf("1d reached the server as a window of %v", left)
	}

	v, err = capByID(t, "lock.list").Run(ctx, remoteReq(map[string]any{"server": "lab", "passphrase": "correct horse"}))
	if err != nil {
		t.Fatal(err)
	}
	if table := v.(view.Table); len(table.Rows) != 1 || len(table.Warnings) != 1 || table.Warnings[0].Code != "lock.everyone" {
		t.Errorf("remote list = %+v", v)
	}

	v, err = capByID(t, "lock.rm").Run(ctx, remoteReq(map[string]any{
		"all": true, "server": "lab", "passphrase": "correct horse",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(t, v, "unlocked"); got != "every agent on lab" {
		t.Errorf("unlocked = %q", got)
	}
	if locks, _ := lockdown.Load(); len(locks) != 0 {
		t.Errorf("the lock survived a remote rm --all: %+v", locks)
	}
}
