package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The etcd backup, and the reason it ships without the other half.
//
// **Every other datastore plugin here carries a dump and the restore that
// reads it back. This one carries the dump alone, and that is a fact about
// etcd rather than a gap in rta.** The v3 gRPC protocol is the complete list
// of what a client may ask a cluster to do, and its Maintenance service is
// Alarm, Status, Defragment, Hash, HashKV, Snapshot, MoveLeader and Downgrade.
// Snapshot streams the backend out. Nothing streams one back in — there is no
// restore RPC, on any service, anywhere in the protocol. Checked against the
// contract rather than recalled: `service Maintenance` in etcd's own rpc.proto
// is those eight calls and no ninth.
//
// Restoring is `etcdutl snapshot restore`, and what it produces is a **data
// directory on a disk**, not a change to a running cluster. The sequence is
// stop etcd, put that directory where the member's data directory was, start
// etcd — on every member, from this one file, because the restore mints a new
// cluster ID and members restored from different snapshots will not form a
// cluster with each other. Every step of that happens on a host rta may not be
// running on, to a process rta cannot stop, against a directory rta does not
// own. It is a runbook, and a capability that wrapped it would be lying about
// which of those it had.
//
// So `etcd.restore` does not exist, and inventing one would be worse than
// leaving it out: it would take the name of the thing an operator needs and
// then not do it, which is the failure mode this whole family exists to avoid
// — a backup you cannot restore is not a backup, it is a belief about one.
// What is owed instead is the file, verified, and the exact sequence printed
// on the receipt at the moment somebody is looking at the backup they just
// took. That is what the three restore lines on the receipt are for, and why
// one of them explains the absence rather than leaving it to be discovered.
//
// The posture is the family's, copied from pg.dump and vault.snapshot:
// humanOnly first so an agent's call never spends the operator's credential on
// a question that was always going to be answered no, Write rather than Read
// because the classification is about disclosure, and NeedsGrant deliberately
// unset — keys.backup's rule, that a grant which can never be exercised over
// the one surface grants exist to gate is an entry in `grant list` meaning
// nothing.
//
// It matters more here than in most of the family. A Kubernetes cluster keeps
// every object it has in etcd, and its Secrets are stored base64-encoded
// rather than encrypted unless somebody turned encryption at rest on — so this
// file is very often that cluster's secrets, all of them, in a form `strings`
// will read.

// snapshotTrailer is the length of the SHA256 etcd appends to the stream after
// the last byte of the database.
const snapshotTrailer = sha256.Size

// snapshotPageSize is the alignment etcdutl uses to decide whether a snapshot
// file has that trailer: a file whose length is snapshotTrailer more than a
// multiple of this has one, and a file whose length is not does not. 512
// because it is the smallest disk sector size and divides every page size the
// backend uses, so a database's own length can never land on the remainder by
// accident.
const snapshotPageSize = 512

func snapshotCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "etcd.snapshot",
		Summary: "Write a point-in-time snapshot of the whole keyspace, for a person at a terminal",
		// **Write for what it discloses, not for what it changes.** Nothing
		// here mutates the cluster; the file is every key and every value it
		// holds. That is the same reading etcd.kv.get gets next door, at the
		// magnitude that makes a grant meaningless rather than narrow.
		Safety: plugin.Write,
		// Running it twice at the same --out refuses rather than overwriting.
		Idempotent: false,
		Description: "etcd's own backup: the whole keyspace at one revision, written as the " +
			"file `etcdutl` restores.\n\n" +
			"**Refuses MCP outright** rather than asking for a grant, the line pg.dump and " +
			"keys.backup draw — a snapshot of everything has no blast radius a grant could " +
			"name. That matters more here than most places: a Kubernetes cluster keeps every " +
			"object it has in etcd, and its Secrets are stored base64-encoded rather than " +
			"encrypted unless somebody turned encryption at rest on, so this file is very " +
			"often that cluster's secrets. An agent that needs one key asks for etcd.kv.get " +
			"with a grant naming it.\n\n" +
			"**There is no `rta etcd restore`, and that is etcd rather than rta.** The v3 API " +
			"streams a snapshot out and takes nothing back in — no service in the protocol " +
			"carries a restore RPC. Restoring is `etcdutl snapshot restore`, which builds a " +
			"data directory on disk: stop etcd, put that directory where the member's was, " +
			"start it, on every member from this one file. The receipt prints that sequence " +
			"rather than leaving it to be looked up on the day it is needed.\n\n" +
			"The snapshot is the connected member's own view at its own revision — a member " +
			"behind the leader writes a file that is behind too — so the receipt names which " +
			"member answered and where its revision was.\n\n" +
			"etcd appends a SHA256 of the database to the end of the stream, and rta hashes " +
			"the bytes as they land and compares, so a transfer that stopped short is caught " +
			"here rather than at restore time. Written with O_EXCL at mode 0600, never over an " +
			"existing file, and a run that fails takes its partial file with it.",
		Run: runSnapshot,
	},
		plugin.Field{Name: "out", Type: plugin.Path, Local: true,
			Help: "file to write the snapshot to; refused if it already exists"})
}

// humanOnly is this plugin's copy of the gate builtin/keys opens with, and
// pg.dump and vault.snapshot repeat. It comes first in the handler, before a
// client is built, so an agent's call never spends the operator's credential
// on a question that was always going to be answered no.
func humanOnly(req plugin.Request, id, hint string) *view.Error {
	if req.Surface() != plugin.SurfaceMCP {
		return nil
	}
	return view.Refusef("etcd.human", "%s can only be run by a person at a terminal", id).
		WithHint(hint)
}

func runSnapshot(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := humanOnly(req, "etcd.snapshot",
		"a snapshot of the whole keyspace has no blast radius a grant could name — its one "+
			"authorized use is everything, and on a Kubernetes cluster that is every Secret "+
			"the cluster holds. Ask for the key you need with etcd.kv.get, which takes a "+
			"grant naming that key"); verr != nil {
		return nil, verr
	}

	out := strings.TrimSpace(req.String("out"))
	if out == "" {
		return nil, view.Errorf("etcd.snapshot.nooutput", "say where the snapshot should be written").
			WithHint("--out ./etcd.snap — a whole keyspace is a file, not something to read in " +
				"a terminal")
	}
	path, err := expandHome(out)
	if err != nil {
		return nil, view.Errorf("etcd.snapshot.path", "resolving --out: %v", err)
	}
	// A friendly early refusal before anything opens a connection. It is not
	// the guarantee — O_EXCL below is, and it still catches the race this
	// cannot.
	if _, err := os.Stat(path); err == nil {
		return nil, snapshotExists(path)
	}

	if req.DryRun {
		return view.Text{Body: "would write a snapshot of " +
			req.String("endpoint") + " to " + path}, nil
	}

	return withClient(ctx, req, func(ctx context.Context, c *clientv3.Client) (view.View, error) {
		// Ask the member what it is before taking anything from it, the way
		// pg.dump asks whether it is talking to a primary: the answer changes
		// what the file means, and finding out here means a cluster that
		// cannot be reached is a classified error rather than a child of a
		// half-written file.
		src, verr := describeSource(ctx, c, req)
		if verr != nil {
			return nil, verr
		}
		return writeSnapshot(ctx, c, req, path, src)
	})
}

// source is what the member said about itself immediately before the snapshot.
//
// **Which member answered is load-bearing here, unlike in most of this
// plugin.** The Snapshot RPC is served out of the connected member's own
// backend rather than through the leader, so a follower that has fallen behind
// hands back a snapshot that has fallen behind with it — and nothing about the
// resulting file says so. The receipt does.
type source struct {
	endpoint string
	member   string
	revision int64
	version  string
	leader   bool
	alarms   []string
}

func describeSource(ctx context.Context, c *clientv3.Client, req plugin.Request) (source, *view.Error) {
	endpoint := req.String("endpoint")
	st, err := c.Status(ctx, endpoint)
	if err != nil {
		return source{}, classifySnapshot(err, req)
	}
	return source{
		endpoint: endpoint,
		member:   hexID(st.Header.MemberId),
		revision: st.Header.Revision,
		version:  st.Version,
		leader:   st.Leader != 0,
		alarms:   st.Errors,
	}, nil
}

func (s source) describe() string {
	where := fmt.Sprintf("%s — member %s at revision %d, etcd %s",
		s.endpoint, s.member, s.revision, s.version)
	if !s.leader {
		// The same fact leaderText spells out for etcd.overview, said here
		// because it changes what the backup is worth: a member with no leader
		// is mid-election or outside quorum, and its revision is whatever it
		// last managed to apply.
		where += ". This member reports NO LEADER, so it is mid-election or has lost quorum " +
			"and this revision may be behind the rest of the cluster"
	}
	return where
}

// restoreVersion names the oldest etcdutl that can read this file.
//
// **The number on the stream is the storage version, not the server's own,
// and etcd's proto says otherwise.** `SnapshotResponse.version` is documented
// as "local version of server that created the snapshot"; a 3.6.6 server
// answering this call sends 3.6.0, which is its `storageVersion` and not its
// version — checked against a real server rather than read off the comment,
// because the two agree on almost every cluster anybody runs and disagree
// exactly here.
//
// Which is the better of the two to print anyway: what a restore needs is a
// tool that understands the format the file is in, and the storage version is
// the name of that format. Reporting 3.6.6 would refuse a working 3.6.2
// etcdutl for no reason.
//
// Servers older than 3.6 send nothing at all, and there the member's own
// version is the only answer available — coarser, and still true, since a
// 3.5 server writes a 3.5 file.
func (s source) restoreVersion(streamed string) string {
	if streamed != "" {
		return streamed
	}
	return s.version
}

func writeSnapshot(ctx context.Context, c *clientv3.Client, req plugin.Request,
	path string, src source) (view.View, error) {
	resp, err := c.SnapshotWithVersion(ctx)
	if err != nil {
		return nil, classifySnapshot(err, req)
	}
	defer func() { _ = resp.Snapshot.Close() }()

	// O_EXCL is the no-overwrite guarantee in one syscall, and 0600 is set at
	// creation rather than chmod'd after, so there is no instant where the file
	// is both complete and readable by everyone. This is every key in the
	// cluster.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, snapshotExists(path)
	}
	if err != nil {
		return nil, view.Errorf("etcd.snapshot.create", "creating %s: %v", path, err)
	}

	started := time.Now()
	// Streamed into the file rather than buffered, and hashed on the way past:
	// a keyspace has no size rta gets to assume, and re-reading the file to
	// verify it would double the I/O on the one capability most likely to be
	// pointed at something large.
	th := &trailerHasher{w: f, h: sha256.New()}
	_, copyErr := io.Copy(th, resp.Snapshot)
	closeErr := f.Close()
	switch {
	case copyErr != nil:
		// A partial snapshot left on disk is the failure that gets restored
		// six months later, so the half-written file goes rather than staying
		// to be mistaken for a good one.
		_ = os.Remove(path)
		if errors.Is(copyErr, context.Canceled) || errors.Is(copyErr, context.DeadlineExceeded) {
			return nil, view.Errorf("etcd.snapshot.interrupted",
				"the snapshot stopped partway through").
				WithHint("the partial file has been removed rather than left looking like a " +
					"backup. A large keyspace over a slow link is the usual cause — the " +
					"transfer is bounded by the call's own deadline, not by etcd")
		}
		return nil, classifySnapshot(copyErr, req)
	case closeErr != nil:
		_ = os.Remove(path)
		return nil, view.Errorf("etcd.snapshot.write", "finishing %s: %v", path, closeErr)
	}

	verified, verr := th.verify()
	if verr != nil {
		// Same removal, for the artifact that is worse than a failure: a file
		// of the right shape and the wrong contents, which restores into a
		// database nobody can tell is wrong.
		_ = os.Remove(path)
		return nil, verr
	}

	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	return snapshotReceipt(path, size, time.Since(started),
		src, resp.Version, verified), nil
}

// snapshotReceipt is the answer, split from the transfer so its wording is
// assertable without a cluster. It is the only place an operator is told how
// to restore this file, so the claims it makes are worth a test of their own.
func snapshotReceipt(path string, size int64, took time.Duration,
	src source, streamedVersion, verified string) view.KeyValue {
	pairs := []view.Pair{
		{Key: "wrote", Value: path},
		{Key: "size", Value: format.Bytes(uint64(size))},
		{Key: "took", Value: took.Round(time.Millisecond).String()},
		{Key: "source", Value: src.describe()},
		{Key: "contents", Value: "every key at that revision, with the users and roles beside " +
			"them — the whole backend, not just the keyspace"},
		{Key: "verified", Value: verified},
	}
	// An alarmed cluster still hands over a snapshot, and the snapshot is
	// still good — but NOSPACE means it has been refusing writes, so the
	// revision this file stops at may be older than whoever asked for it
	// expects. Said here for the same reason etcd.overview says it.
	for _, a := range src.alarms {
		pairs = append(pairs, view.Pair{Key: "ALARM", Value: a})
	}
	return view.KeyValue{Pairs: append(pairs,
		// Named on the answer rather than left in the docs, with this
		// plugin's own sharpest fact attached.
		view.Pair{Key: "at rest", Value: "unencrypted, mode 0600 — a Kubernetes cluster keeps " +
			"its Secrets in here base64-encoded rather than encrypted, so treat this file as " +
			"those secrets. `rta kv` or `age` if it is going anywhere"},
		view.Pair{Key: "does not carry", Value: "the cluster's own identity: membership, peer " +
			"URLs and TLS material. The restore mints a new cluster ID and takes the topology " +
			"from its own flags, so keep the member list beside this file"},
		view.Pair{Key: "restore with", Value: fmt.Sprintf(
			"etcdutl snapshot restore %s --data-dir <new data dir>, using etcdutl from etcd %s "+
				"or newer — the storage format this file is in",
			path, src.restoreVersion(streamedVersion))},
		view.Pair{Key: "restore is offline", Value: "stop etcd, put that directory where the " +
			"member's data directory was, start it. On every member, from this one file: the " +
			"restore mints a new cluster ID, so members restored from different snapshots " +
			"will not form a cluster with each other"},
		view.Pair{Key: "and has no capability", Value: "deliberately. etcd's API streams a " +
			"snapshot out and takes nothing back in — there is no restore RPC in the v3 " +
			"protocol — so `rta etcd restore` would be a name for something rta cannot do"},
	)}
}

func snapshotExists(path string) *view.Error {
	return view.Errorf("etcd.snapshot.exists", "%s already exists", path).
		WithHint("a snapshot is never written over an existing file — name a new one, or move " +
			"that one aside")
}

// trailerHasher passes every byte through to the file while hashing all but
// the last snapshotTrailer of them, which it keeps.
//
// **etcd's server appends a SHA256 of the database to the end of the snapshot
// stream** — one final blob after the last page of the backend — and the tool
// that restores it finds that digest by arithmetic rather than by a header: a
// file whose length is snapshotTrailer more than a multiple of snapshotPageSize
// has one, and a file whose length is not does not. That rule is mirrored here
// exactly, because a check that disagrees with the tool doing the restore is
// worse than no check at all.
//
// The delay window is what makes this one pass instead of two. The digest is
// the tail of the stream and is not part of what it covers, so the hash has to
// run snapshotTrailer bytes behind the write — at EOF the window holds exactly
// the digest and the hash holds exactly what the digest is of.
type trailerHasher struct {
	w    io.Writer
	h    hash.Hash
	tail []byte
	n    int64
}

func (t *trailerHasher) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		t.n += int64(n)
		// Only what the file actually took: a short write must not be hashed
		// as though it had landed, or a truncated file would verify.
		t.tail = append(t.tail, p[:n]...)
		if extra := len(t.tail) - snapshotTrailer; extra > 0 {
			_, _ = t.h.Write(t.tail[:extra])
			t.tail = t.tail[:copy(t.tail, t.tail[extra:])]
		}
	}
	return n, err
}

// verify compares the digest etcd sent against the bytes that landed, and
// returns the line the receipt prints about it.
func (t *trailerHasher) verify() (string, *view.Error) {
	if t.n%snapshotPageSize != snapshotTrailer {
		// Not a failure. A server that appended no digest still wrote a
		// snapshot etcdutl will restore, and etcdutl skips the same check by
		// the same arithmetic. Reported rather than swallowed, because
		// "verified" and "not verified" are different backups and only one of
		// them was checked.
		return "no digest on the stream — this server appended none, and etcdutl skips the " +
			"same check for the same reason", nil
	}
	got := t.h.Sum(nil)
	if !bytes.Equal(t.tail, got) {
		return "", view.Errorf("etcd.snapshot.corrupt",
			"the bytes that arrived do not match the digest etcd sent with them").
			WithHint("the file has been removed rather than left looking like a backup. This " +
				"is a transfer that was damaged rather than truncated — a proxy or a tunnel " +
				"in the path is the usual cause, since a dropped connection ends the stream " +
				"instead")
	}
	return "sha256 " + hex.EncodeToString(got) + ", matching the digest etcd appended", nil
}

// classifySnapshot names the failure particular to this call before falling
// back to the shared classifier.
//
// etcd's maintenance calls are root-only when auth is enabled, so a user that
// reads every key in the cluster still cannot take a snapshot — and the shared
// classifier's answer for PermissionDenied is about key ranges, which sends
// somebody to widen a role that was already as wide as roles go.
//
// Verified against a real server rather than read off the documentation: a
// user granted read on the empty prefix, which is every key there is, listed
// all 201 keys in the cluster and still got PermissionDenied on Snapshot,
// where root got the file.
func classifySnapshot(err error, req plugin.Request) *view.Error {
	var already *view.Error
	if errors.As(err, &already) {
		return already
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.PermissionDenied {
		return view.Errorf("etcd.snapshot.denied",
			"%s refused the snapshot: %s", req.String("endpoint"), st.Message()).
			WithHint("etcd's maintenance calls are root-only — a role with permissions on a " +
				"key range does not reach them, however wide that range is. Authenticate as a " +
				"member of the root role")
	}
	return classify(err, req)
}

// expandHome resolves a leading ~ the way every other consumer of a Path input
// in this codebase does. Mirrors builtin/kv, builtin/keys and plugins/pg's own
// copies rather than centralizing a shared helper: an external plugin cannot
// reach internal/pathguard, and the rule is ten lines.
func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return filepath.Abs(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}
