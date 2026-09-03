//go:build liveetcd

// The questions a fake cannot answer, run against a real etcd.
//
// One of them is the only reason this capability is worth having: **does the
// file rta writes actually restore?** Everything else here checks bytes rta
// controls. That one checks whether etcd's own tool accepts what rta produced,
// and no amount of unit testing reaches it — the snapshot format is a bbolt
// database with a digest glued to the end, and rta neither writes nor parses
// it.
//
//	docker run -d --name rta-etcd-live -p 12379:2379 -p 12380:2380 \
//	  -e ETCD_NAME=rta0 -e ETCD_DATA_DIR=/etcd-data \
//	  -e ETCD_LISTEN_CLIENT_URLS=http://0.0.0.0:2379 \
//	  -e ETCD_ADVERTISE_CLIENT_URLS=http://127.0.0.1:12379 \
//	  -e ETCD_LISTEN_PEER_URLS=http://0.0.0.0:2380 \
//	  -e ETCD_INITIAL_ADVERTISE_PEER_URLS=http://127.0.0.1:12380 \
//	  -e ETCD_INITIAL_CLUSTER=rta0=http://127.0.0.1:12380 \
//	  -e ETCD_INITIAL_CLUSTER_STATE=new \
//	  quay.io/coreos/etcd:v3.6.6
//
//	RTA_ETCD_LIVE=127.0.0.1:12379 \
//	  go test ./plugins/etcd/ -tags liveetcd -count=1 -v
//
// The restore half runs in the same image, because etcdutl ships beside etcd
// and the whole point is that the file is restorable by the tool the receipt
// names. The snapshot is left at $RTA_ETCD_SNAPSHOT (default /tmp/live.snap)
// so these have something to run against:
//
//	docker cp /tmp/live.snap rta-etcd-live:/tmp/live.snap
//	docker exec rta-etcd-live etcdutl snapshot status /tmp/live.snap -w table
//	docker exec rta-etcd-live etcdutl snapshot restore /tmp/live.snap \
//	  --name rta0 --initial-cluster rta0=http://127.0.0.1:12380 \
//	  --initial-advertise-peer-urls http://127.0.0.1:12380 --data-dir /tmp/restored
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func liveEndpoint(t *testing.T) string {
	t.Helper()
	ep := os.Getenv("RTA_ETCD_LIVE")
	if ep == "" {
		t.Skip("set RTA_ETCD_LIVE=host:port — see this file's package comment for the container")
	}
	return ep
}

func liveClient(t *testing.T, endpoint string) *clientv3.Client {
	t.Helper()
	c, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("connecting to %s: %v", endpoint, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// liveSnapshot fills the cluster with something worth backing up and takes one.
//
// The keys are what make the restore half mean anything: a snapshot of an
// empty keyspace restores just as cleanly as a good one and proves nothing
// about whether the transfer carried what it claimed. Every test here takes
// its own, so the order the suite runs them in — shuffled, like every other
// suite in this repository — cannot matter.
func liveSnapshot(t *testing.T, endpoint, path string) view.KeyValue {
	t.Helper()
	c := liveClient(t, endpoint)
	ctx := context.Background()

	if _, err := c.Put(ctx, "/rta-live/canary",
		fmt.Sprintf("written at %d", time.Now().UnixNano())); err != nil {
		t.Fatalf("putting the canary: %v", err)
	}
	for i := range 200 {
		if _, err := c.Put(ctx, fmt.Sprintf("/rta-live/bulk/%03d", i),
			strings.Repeat("x", 512)); err != nil {
			t.Fatalf("putting bulk key %d: %v", i, err)
		}
	}

	_ = os.Remove(path)
	v, err := runSnapshot(ctx, req(t, "etcd.snapshot", map[string]any{
		"endpoint": endpoint,
		"out":      path,
	}))
	if err != nil {
		t.Fatalf("etcd.snapshot: %v", err)
	}
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("want KeyValue, got %s", view.TypeOf(v))
	}
	return kv
}

func snapshotPath() string {
	if p := os.Getenv("RTA_ETCD_SNAPSHOT"); p != "" {
		return p
	}
	return "/tmp/live.snap"
}

// The whole capability, end to end, against a cluster with something in it.
func TestASnapshotOfARealClusterIsWrittenAndVerified(t *testing.T) {
	endpoint := liveEndpoint(t)
	path := snapshotPath()
	kv := liveSnapshot(t, endpoint, path)
	for _, p := range kv.Pairs {
		t.Logf("%-20s %s", p.Key, p.Value)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the snapshot is not where the receipt says it is: %v", err)
	}
	// A file holding every Secret in a Kubernetes cluster, at the mode the
	// declaration promises.
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
	if info.Size() < 16*1024 {
		t.Errorf("size = %d, smaller than an empty etcd's own backend — nothing was transferred",
			info.Size())
	}

	if verified := livePair(t, kv, "verified"); !strings.HasPrefix(verified, "sha256 ") {
		t.Errorf("the receipt does not claim a verified digest: %q", verified)
	}

	c := liveClient(t, endpoint)
	st, err := c.Status(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if src := livePair(t, kv, "source"); !strings.Contains(src, hexID(st.Header.MemberId)) {
		t.Errorf("the receipt names a different member than answered: %q", src)
	}

	// **The version on the restore line is the storage version, and etcd's own
	// proto says it is the server's.** `SnapshotResponse.version` is documented
	// as "local version of server that created the snapshot"; this 3.6.6 server
	// sends 3.6.0, its storageVersion. That is the more useful of the two —
	// what a restore needs is a tool that reads the format — but it is not what
	// the comment promises, so it is pinned here against the server's own two
	// answers rather than against a literal.
	wantVersion := st.StorageVersion
	if wantVersion == "" {
		wantVersion = st.Version
	}
	restore := livePair(t, kv, "restore with")
	if !strings.Contains(restore, "etcdutl snapshot restore") {
		t.Errorf("the restore line does not name etcdutl: %q", restore)
	}
	if !strings.Contains(restore, wantVersion) {
		t.Errorf("the restore line says %q; this server reports version %s, storageVersion %s",
			restore, st.Version, st.StorageVersion)
	}
}

// **The digest rule, checked against a real file rather than against rta's own
// arithmetic.** verify() computes the hash on the way past, so a mistake in the
// delay window would produce a self-consistent wrong answer that every unit
// test agrees with. This reads the finished file back and applies etcdutl's
// rule from the outside: the length says a digest is there, and the last 32
// bytes are the hash of everything before them.
func TestTheDigestRtaChecksIsTheOneInTheFile(t *testing.T) {
	endpoint := liveEndpoint(t)
	path := snapshotPath()
	liveSnapshot(t, endpoint, path)

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the snapshot back: %v", err)
	}
	if len(body)%snapshotPageSize != snapshotTrailer {
		t.Fatalf("length %d is not a page multiple plus a digest — this server appended none, "+
			"and the receipt should have said so rather than claiming a match", len(body))
	}
	sum := sha256.Sum256(body[:len(body)-snapshotTrailer])
	if !bytes.Equal(sum[:], body[len(body)-snapshotTrailer:]) {
		t.Error("the trailing digest does not cover the rest of the file — either etcd's rule " +
			"is not what this code believes, or the delay window is off by something")
	}
}

// A dry run against a reachable cluster still touches nothing: no file, and no
// snapshot stream opened on a cluster that would happily have served one.
func TestADryRunAgainstARealClusterWritesNothing(t *testing.T) {
	endpoint := liveEndpoint(t)
	path := "/tmp/live-dryrun.snap"
	_ = os.Remove(path)

	r := plugin.NewRequest(plugin.Resolve(snapshotCapability(), plugin.Inputs{Caller: map[string]any{
		"endpoint": endpoint,
		"out":      path,
	}}), true, false)
	if _, err := runSnapshot(context.Background(), r); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a dry run created the file")
		_ = os.Remove(path)
	}
}

func livePair(t *testing.T, kv view.KeyValue, key string) string {
	t.Helper()
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	t.Fatalf("the receipt has no %q row", key)
	return ""
}
