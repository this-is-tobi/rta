package consent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The digest a remote answer carries must pin the file at decision time —
// Honest alone passes a swap for a *different* honest request under the
// same id, which is exactly the gap between a display that crossed a
// network and a queue something else can write.
func TestDecideBoundRefusesAReplacedRequest(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	parked, err := Ask(Call{Cap: "kv.get", Safety: "read", Scopes: []string{"db-password"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()

	other := Call{Cap: "todo.rm", Safety: "destructive"}.Digest()
	err = DecideBound(parked.Request.ID, other, true, "test")
	if err == nil || !strings.Contains(err.Error(), "no longer describes") {
		t.Fatalf("err = %v, want the replaced-request refusal", err)
	}
	if err := DecideBound(parked.Request.ID, "", true, "test"); err == nil {
		t.Fatal("an empty digest was accepted as a binding")
	}

	if err := DecideBound(parked.Request.ID, parked.Request.Digest, true, "test"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if a := parked.Wait(ctx); !a.Answered || !a.Allowed {
		t.Fatalf("answer = %+v, want the bound decision honoured", a)
	}
}
