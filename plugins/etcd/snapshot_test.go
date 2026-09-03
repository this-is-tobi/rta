package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A snapshot of the whole keyspace has no blast radius a grant could name, so
// it leaves the agent surface rather than asking for one — and the refusal is
// marked as a refusal, so the ledger files it under policy rather than "the
// work broke".
func TestTheSnapshotRefusesMCP(t *testing.T) {
	r := req(t, "etcd.snapshot", map[string]any{
		"out": filepath.Join(t.TempDir(), "etcd.snap"),
	}).WithSurface(plugin.SurfaceMCP)
	_, err := runSnapshot(context.Background(), r)
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "etcd.human" || !verr.Refusal {
		t.Fatalf("err = %v, want etcd.human marked a refusal", err)
	}
	if !strings.Contains(verr.Hint, "etcd.kv.get") {
		t.Errorf("hint = %q, want it to name the bounded alternative", verr.Hint)
	}
}

// The refusal comes before anything reaches for the cluster, so an agent's
// call never spends a credential on a question already answered. Pinned by
// pointing the request at a port nothing is listening on: a handler that
// connected first would fail with that instead.
func TestTheRefusalComesBeforeTheConnection(t *testing.T) {
	r := req(t, "etcd.snapshot", map[string]any{
		"endpoint": "127.0.0.1:1",
		"out":      filepath.Join(t.TempDir(), "etcd.snap"),
	}).WithSurface(plugin.SurfaceMCP)
	_, err := runSnapshot(context.Background(), r)
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "etcd.human" {
		t.Fatalf("err = %v, want the refusal rather than a connection failure", err)
	}
}

func TestSayingNothingAboutWhereItGoesIsRefusedWithAnExample(t *testing.T) {
	_, err := runSnapshot(context.Background(), req(t, "etcd.snapshot", map[string]any{}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "etcd.snapshot.nooutput" {
		t.Fatalf("err = %v, want etcd.snapshot.nooutput", err)
	}
	if !strings.Contains(verr.Hint, "--out") {
		t.Errorf("hint = %q, want an example naming --out", verr.Hint)
	}
}

// A backup is never written over an existing file. The stat is only the
// friendly half — O_EXCL is the guarantee — but it is the half that answers
// before a connection is opened, which is what makes it testable without a
// cluster and useful without one.
func TestASnapshotIsNeverWrittenOverAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etcd.snap")
	if err := os.WriteFile(path, []byte("an older backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runSnapshot(context.Background(), req(t, "etcd.snapshot", map[string]any{
		"endpoint": "127.0.0.1:1",
		"out":      path,
	}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "etcd.snapshot.exists" {
		t.Fatalf("err = %v, want etcd.snapshot.exists", err)
	}
	if body, _ := os.ReadFile(path); string(body) != "an older backup" {
		t.Error("the existing file was touched")
	}
}

// A dry run says what would happen and reaches nothing. The endpoint is a port
// nothing listens on, so a handler that connected would fail rather than
// describe.
func TestADryRunNamesTheDestinationAndOpensNoConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etcd.snap")
	r := plugin.NewRequest(plugin.Resolve(snapshotCapability(), plugin.Inputs{Caller: map[string]any{
		"endpoint": "127.0.0.1:1",
		"out":      path,
	}}), true, false)

	v, err := runSnapshot(context.Background(), r)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	body, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	if !strings.Contains(body.Body, path) {
		t.Errorf("the dry run does not name where the file would go: %q", body.Body)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a dry run created the file")
	}
}

// **The digest rule, and it has to be etcdutl's rule rather than a reasonable
// one.** A file whose length is snapshotTrailer more than a multiple of
// snapshotPageSize carries a digest; one whose length is not does not, and
// both tools have to agree about which — a check that fired where etcdutl
// skipped would reject working backups, and one that skipped where etcdutl
// checked would pass broken ones through.
func TestTheTrailerIsCheckedTheWayEtcdutlChecksIt(t *testing.T) {
	body := []byte(strings.Repeat("etcd", 256)) // 1024, a whole number of pages
	sum := sha256.Sum256(body)

	t.Run("a digest that matches is reported as verified", func(t *testing.T) {
		th := feed(t, 64, body, sum[:])
		got, verr := th.verify()
		if verr != nil {
			t.Fatalf("verify: %v", verr)
		}
		if !strings.HasPrefix(got, "sha256 ") {
			t.Errorf("verify says %q, want the digest it matched", got)
		}
	})

	t.Run("a digest that does not match takes the file with it", func(t *testing.T) {
		wrong := sha256.Sum256([]byte("some other database"))
		th := feed(t, 64, body, wrong[:])
		_, verr := th.verify()
		if verr == nil || verr.Code != "etcd.snapshot.corrupt" {
			t.Fatalf("verify = %v, want etcd.snapshot.corrupt", verr)
		}
	})

	t.Run("a length that carries no digest is said rather than claimed", func(t *testing.T) {
		// 1000 + 32 = 1032, which is 8 past a page boundary: etcdutl reads no
		// digest here and neither does this.
		th := feed(t, 64, body[:1000], sum[:])
		got, verr := th.verify()
		if verr != nil {
			t.Fatalf("verify: %v", verr)
		}
		if !strings.Contains(got, "no digest") {
			t.Errorf("verify says %q, want it to say the stream carried no digest", got)
		}
	})
}

// **The delay window is the part that can be subtly wrong.** The digest is the
// tail of the stream and is not covered by itself, so the hash runs 32 bytes
// behind the writer — and a window that flushed a byte early or late would
// still produce a self-consistent answer that only a real file disagrees with.
// Feeding the same bytes at chunk sizes either side of the window, and in one
// go, pins it against arithmetic rather than against luck.
func TestTheDelayWindowIsIndependentOfHowTheStreamArrives(t *testing.T) {
	body := []byte(strings.Repeat("k", 2048))
	sum := sha256.Sum256(body)
	for _, chunk := range []int{1, 7, 31, 32, 33, 512, 4096} {
		th := feed(t, chunk, body, sum[:])
		if _, verr := th.verify(); verr != nil {
			t.Errorf("chunk %d: %v", chunk, verr)
		}
	}
}

// feed writes body and then trailer through a trailerHasher in chunk-sized
// pieces, the way a stream arrives, and hands back the hasher to be asked.
func feed(t *testing.T, chunk int, body, trailer []byte) *trailerHasher {
	t.Helper()
	th := &trailerHasher{w: io.Discard, h: sha256.New()}
	all := append(append([]byte{}, body...), trailer...)
	for i := 0; i < len(all); i += chunk {
		end := min(i+chunk, len(all))
		if _, err := th.Write(all[i:end]); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return th
}

// **The receipt is the only place an operator is told how to restore this
// file**, because there is no capability to send them to. Every claim it makes
// is therefore load-bearing, and each of these lines exists for a reason a
// future edit could quietly drop.
func TestTheReceiptNamesTheOfflineRestoreAndWhyThereIsNoCapability(t *testing.T) {
	src := source{
		endpoint: "etcd-0.internal:2379",
		member:   "8e9e05c52164694d",
		revision: 4021,
		version:  "3.6.6",
		leader:   true,
	}
	kv := snapshotReceipt("/backups/etcd.snap", 438304, 120*time.Millisecond,
		src, "3.6.0", "sha256 abc, matching the digest etcd appended")

	pairs := map[string]string{}
	for _, p := range kv.Pairs {
		pairs[p.Key] = p.Value
	}
	for _, want := range []struct{ key, substr, why string }{
		{"restore with", "etcdutl snapshot restore /backups/etcd.snap",
			"the command, with this file's own path in it"},
		{"restore with", "3.6.0",
			"the storage version, which is the oldest etcdutl that reads this format"},
		{"restore is offline", "every member",
			"one file restores the whole cluster or none of it"},
		{"and has no capability", "no restore RPC",
			"why rta has no etcd restore, said where somebody would look for one"},
		{"at rest", "0600", "the mode, beside the fact that the file is unencrypted"},
		{"does not carry", "membership",
			"the half of a restore that is not in the file"},
		{"source", "8e9e05c52164694d",
			"which member answered, since the snapshot is that member's own view"},
	} {
		if !strings.Contains(pairs[want.key], want.substr) {
			t.Errorf("receipt row %q does not carry %q (%s): %q",
				want.key, want.substr, want.why, pairs[want.key])
		}
	}
}

// A member with no leader is mid-election or outside quorum, and its revision
// is whatever it last managed to apply. A backup taken from one is still worth
// having and is not worth mistaking for current.
func TestAMemberWithNoLeaderSaysSoOnTheReceipt(t *testing.T) {
	src := source{endpoint: "e:2379", member: "abc", revision: 7, version: "3.6.6"}
	if !strings.Contains(src.describe(), "NO LEADER") {
		t.Errorf("describe = %q, want it to say the member has no leader", src.describe())
	}
	src.leader = true
	if strings.Contains(src.describe(), "NO LEADER") {
		t.Errorf("describe = %q, want no warning when there is a leader", src.describe())
	}
}

// **The version on the stream is the storage version, not the server's own.**
// etcd's proto calls it "local version of server that created the snapshot"; a
// 3.6.6 server sends 3.6.0. That is the better of the two to print — a restore
// needs a tool that reads the format, and the storage version names the format
// — so the stream's answer wins wherever there is one, and a pre-3.6 server
// that sends nothing falls back to the member's own version.
func TestTheStreamsVersionWinsOverTheMembersOwn(t *testing.T) {
	src := source{version: "3.6.6"}
	if got := src.restoreVersion("3.6.0"); got != "3.6.0" {
		t.Errorf("restoreVersion = %q, want the storage version the stream reported", got)
	}
	if got := src.restoreVersion(""); got != "3.6.6" {
		t.Errorf("restoreVersion = %q, want the member's own version when the stream sends none", got)
	}
}

// etcd's maintenance calls are root-only, so the shared classifier's answer for
// PermissionDenied — widen the role's key range — is advice that cannot work.
// Verified against a real server: a user with read on every key gets this.
func TestASnapshotDenialNamesTheRootRoleRatherThanTheKeyRange(t *testing.T) {
	r := req(t, "etcd.snapshot", map[string]any{"endpoint": "etcd-0.internal:2379"})
	verr := classifySnapshot(status.Error(codes.PermissionDenied, "etcdserver: permission denied"), r)
	if verr.Code != "etcd.snapshot.denied" {
		t.Fatalf("code = %q, want etcd.snapshot.denied", verr.Code)
	}
	if !strings.Contains(verr.Hint, "root") {
		t.Errorf("hint = %q, want it to name the root role", verr.Hint)
	}
	// Everything else still goes to the shared classifier, so the one
	// specialization here does not cost the rest of them their answers.
	if got := classifySnapshot(status.Error(codes.Unauthenticated, "auth"), r); got.Code != "etcd.auth.failed" {
		t.Errorf("an unauthenticated call classified as %q, want etcd.auth.failed", got.Code)
	}
}

// **This plugin ships a backup and no restore, on purpose.**
//
// etcd's v3 protocol streams a snapshot out and takes nothing back in — there
// is no restore RPC on any service — so a restore happens offline, against a
// data directory, with etcd stopped. rta is not in a position to do any of
// that, and a capability named for it would take the name of the thing an
// operator needs and then not do it.
//
// If a future change finds a way, this test is where the justification goes:
// it fails on the day somebody adds the word, which is exactly the moment to
// re-read the paragraph above rather than six months later.
func TestNothingHereClaimsToRestore(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if strings.Contains(c.ID, "restore") {
			t.Errorf("%s: etcd has no restore over the wire — see the comment on this test", c.ID)
		}
	}
	var snap plugin.Capability
	for _, c := range Plugin().Capabilities {
		if c.ID == "etcd.snapshot" {
			snap = c
		}
	}
	if snap.ID == "" {
		t.Fatal("etcd.snapshot is gone, and this plugin has no backup at all")
	}
	// The absence has to be explained where somebody reads about the backup,
	// which is `rta explain etcd.snapshot` and nowhere else.
	for _, want := range []string{"etcdutl", "no `rta etcd restore`"} {
		if !strings.Contains(snap.Description, want) {
			t.Errorf("etcd.snapshot's description never mentions %q, so the missing half is "+
				"unexplained where it is looked for", want)
		}
	}
}
