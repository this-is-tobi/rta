package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The kv tile is the store's overview, and what it does on a timer is the half
// of that a tile has to get right: it counts the store from what the
// environment holds, asks nobody for anything, and leaves no store session
// behind that its own refreshes would keep warm.

const tilePassphrase = "correct horse battery staple"

// kvTile runs one refresh of the dashboard's kv tile, the way a tick does.
func kvTile(t *testing.T, env string) view.KeyValue {
	t.Helper()
	reg := realRegistry(t)
	c, ok := reg.Capability("kv.overview")
	if !ok {
		t.Fatal("kv.overview is not registered")
	}
	t.Setenv("RTA_KV_PASSPHRASE", env)
	msg := tileCmd(0, tile{cap: c}, statedConfig{}, "", nil, config.Connection{})().(tileMsg)
	if msg.err != nil {
		t.Fatalf("the kv tile errored: %v", msg.err)
	}
	kvs, ok := msg.v.(view.KeyValue)
	if !ok {
		t.Fatalf("the kv tile is a %s, want a KeyValue", view.TypeOf(msg.v))
	}
	return kvs
}

func tilePair(kvs view.KeyValue, key string) string {
	for _, p := range kvs.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

func TestTheKvTileCountsTheStoreFromTheEnvironmentAndKeepsNoSession(t *testing.T) {
	kv.ForgetSession()
	t.Cleanup(kv.ForgetSession)
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_KV_IDENTITY", "")

	reg := realRegistry(t)
	set, _ := reg.Capability("kv.set")
	for key, value := range map[string]string{"prod/db-password": "hunter2", "api-token": "tok-1"} {
		create := plugin.NewRequest(map[string]any{"key": key, "value": value, "passphrase": tilePassphrase}, false, false)
		if _, err := set.Run(context.Background(), create); err != nil {
			t.Fatal(err)
		}
	}

	// Two refreshes, as a dashboard left open makes: the second would move a
	// session's idle deadline out if the first had started one.
	for range 2 {
		tileView := kvTile(t, tilePassphrase)
		if got := tilePair(tileView, "keys"); !strings.HasPrefix(got, "2 keys, 1 folder") {
			t.Errorf("the tile says keys = %q, want the store counted", got)
		}
		if _, held := kv.SessionPassphrase(); held {
			t.Fatal("a tile refresh started a store session: the header would say unlocked and it would never relock")
		}
	}
}

// With nothing that opens the store at hand the tile says where it is and that
// the counts need a key, in the words of the surface it is drawn on — the box
// the unlock form shows — and does not wait for an answer.
func TestTheKvTileOfAStoreItCannotOpenSaysWhatItNeeds(t *testing.T) {
	kv.ForgetSession()
	t.Cleanup(kv.ForgetSession)
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_KV_IDENTITY", "")

	set, _ := realRegistry(t).Capability("kv.set")
	create := plugin.NewRequest(map[string]any{"key": "a", "value": "b", "passphrase": tilePassphrase}, false, false)
	if _, err := set.Run(context.Background(), create); err != nil {
		t.Fatal(err)
	}

	tileView := kvTile(t, "")
	line := tilePair(tileView, "inventory")
	if !strings.HasPrefix(line, "needs a key at hand") || !strings.Contains(line, "the passphrase box") {
		t.Errorf("the tile says inventory = %q", line)
	}
	if got := tilePair(tileView, "keys"); got != "" {
		t.Errorf("a store nothing opened was counted: %q", got)
	}
	if tilePair(tileView, "store") == "" || tilePair(tileView, "locked with") == "" {
		t.Errorf("the tile lost the facts about the file: %v", tileView.Pairs)
	}
}
