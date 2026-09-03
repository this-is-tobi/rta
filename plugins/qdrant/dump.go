package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The whole collection, for a person, as a file.
//
// **This is the capability that has no nameable blast radius, and it is
// therefore the one that refuses MCP outright rather than asking for a
// grant** — pg.dump's line, drawn here for a sharper reason than rows. A
// collection snapshot carries both halves this plugin's read tier exists to
// keep apart: every payload, and every raw vector, and a vector is a lossy
// but reversible-enough encoding of its source text. qdrant.points.scroll
// lets an agent ask for bounded points under a grant naming the collection;
// the snapshot's single authorized use is "all of it", which is not a thing
// a grant can meaningfully consent to. Leaving NeedsGrant unset is
// deliberate and copied from keys.backup: a grant that can never be
// exercised over the one surface grants exist to gate would be a standing
// entry in `grant list` that means nothing.
//
// **It rides Qdrant's own snapshot API rather than scrolling points out** —
// the same decision pg.dump makes by shelling out to pg_dump. A snapshot is
// the server's own restorable artifact: segments, index structures, payload
// schema, collection config, all of it, produced under the server's own
// consistency rules. A point-by-point export rebuilt through upserts would
// lose the index and the config and restore into something that answers
// differently — a backup you cannot faithfully restore is not a backup, it
// is a belief about one.
//
// The transfer is create, download, delete: the server writes the snapshot
// to its own disk, this capability streams it down, and then removes the
// server-side copy — snapshots live in the server's storage directory and
// accumulate silently, and a backup tool that fills the disk it is backing
// up has misunderstood its job. A delete that fails is reported on the
// receipt rather than failing the dump: the file is safely local either way.

// humanOnly is this plugin's copy of the gate builtin/keys opens with. It
// comes first in the handler, before anything reaches the network, so an
// agent's call never touches the instance on a question that was always
// going to be answered no. The hint is the caller's, because the dump and
// the restore refuse for mirrored reasons — everything leaving, everything
// arriving — and one blended hint would explain neither.
func humanOnly(req plugin.Request, id, hint string) *view.Error {
	if req.Surface() != plugin.SurfaceMCP {
		return nil
	}
	return view.Refusef("qdrant.human", "%s can only be run by a person at a terminal", id).
		WithHint(hint)
}

func dumpCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "qdrant.dump",
		Summary: "Back up one collection to a snapshot file, for a person at a terminal",
		Safety:  plugin.Write,
		// Not idempotent, and the reason is the guarantee: running it twice
		// at the same --out refuses rather than overwriting.
		Idempotent: false,
		Description: "One collection as a file you can restore. **Refuses MCP outright rather " +
			"than asking for a grant**, the same line pg.dump and keys.backup draw: a snapshot " +
			"is every payload and every raw vector in the collection, and a vector is a " +
			"reversible-enough encoding of its source text. An agent that needs points asks " +
			"for qdrant.points.scroll and a person names the collection in the grant.\n\n" +
			"Uses Qdrant's own snapshot API rather than scrolling points out: the snapshot is " +
			"the server's restorable artifact — segments, indexes, payload schema, collection " +
			"config — and a point-by-point export would restore into a collection that answers " +
			"differently. The server writes the snapshot, rta streams it down, and the " +
			"server-side copy is deleted so backups do not silently fill the server's own " +
			"disk.\n\n" +
			"Created with O_EXCL at 0600, so an existing file is never written over; a failed " +
			"run takes its half-written file with it. The receipt says the file is " +
			"unencrypted, names the restore command, and reports what the collection held " +
			"when the snapshot was taken.",
		Run: runDump,
	}, collectionField("collection to dump"),
		// Local for the usual reason — a destination is a destination — and
		// so a caller can never choose which file on the host is written.
		// Belt and braces beside the MCP refusal, since the two protect
		// against different mistakes: the refusal is this capability's, and
		// Local is the contract's.
		plugin.Field{Name: "out", Type: plugin.Path, Local: true,
			Help: "file to write; refused if it already exists"})
}

func runDump(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := humanOnly(req, "qdrant.dump",
		"a collection snapshot has no blast radius a grant could name — it is every "+
			"payload and every vector, and its one authorized use is all of them. Ask for "+
			"the points you need with qdrant.points.scroll, which takes a grant naming the "+
			"collection"); verr != nil {
		return nil, verr
	}
	collection := req.String("collection")

	out := strings.TrimSpace(req.String("out"))
	if out == "" {
		return nil, view.Errorf("qdrant.dump.nooutput", "say where the snapshot should be written").
			WithHint("--out ./" + collection + ".snapshot — a collection is a file, not " +
				"something to read in a terminal")
	}
	path, err := expandHome(out)
	if err != nil {
		return nil, view.Errorf("qdrant.dump.path", "resolving --out: %v", err)
	}
	// A friendly early refusal, before the server does any work to say the
	// same thing more slowly. It is not the guarantee — O_EXCL in
	// downloadSnapshot is, and that still catches the race this stat cannot.
	if _, err := os.Stat(path); err == nil {
		return nil, alreadyThere(path)
	}

	if req.DryRun {
		return view.Text{Body: fmt.Sprintf(
			"would ask %s to snapshot %q, stream it to %s, and delete the server-side copy",
			req.String("endpoint"), collection, path)}, nil
	}

	// **Ask the server what it holds before dumping it** — pg.dump's
	// describeSource discipline. The count is what the receipt reports as
	// the snapshot's contents, and an unreachable instance or a missing
	// collection fails here, classified, before any file is created.
	var info collectionInfo
	if verr := get(ctx, req, pathFor("/collections/%s", collection), &info); verr != nil {
		return nil, verr
	}
	version := serverVersion(ctx, req)

	started := time.Now()
	snap, verr := createSnapshot(ctx, req, collection)
	if verr != nil {
		return nil, verr
	}
	written, verr := downloadSnapshot(ctx, req, collection, snap.Name, path)
	if verr != nil {
		// The server-side copy goes even when the download failed — a broken
		// transfer that also leaves a snapshot eating the server's disk is
		// two failures for the price of one.
		_ = deleteSnapshot(ctx, req, collection, snap.Name)
		return nil, verr
	}

	pairs := []view.Pair{
		{Key: "wrote", Value: path},
		{Key: "size", Value: format.Bytes(uint64(written))},
		{Key: "took", Value: time.Since(started).Round(time.Millisecond).String()},
		{Key: "contents", Value: describeContents(info, version)},
		// Named on the answer rather than left in the docs, and with this
		// plugin's own sharpest fact attached: the file is every payload and
		// every vector, and a vector is close to its document.
		{Key: "at rest", Value: "unencrypted, mode 0600 — payloads and raw vectors both; " +
			"`rta kv` or `age` if it is going anywhere"},
		// **The half of the restore that is not in this file.** A snapshot is of
		// a collection, and an alias is a name pointing at one — kept beside
		// collections rather than inside them. Restore this and the collection
		// is back under its own name while whatever queried it by alias still
		// finds nothing.
		{Key: "does not carry", Value: "the aliases pointing at this collection — a restore " +
			"brings the collection back under its own name, and anything querying it by an " +
			"alias still finds nothing until the alias is recreated"},
		{Key: "restore with", Value: fmt.Sprintf("rta qdrant restore %s %s --endpoint=%s",
			collection, path, req.String("endpoint"))},
	}
	if verr := deleteSnapshot(ctx, req, collection, snap.Name); verr != nil {
		// Reported rather than failed: the dump is safely local, and the
		// leftover is the server's disk problem, named so somebody can act.
		pairs = append(pairs, view.Pair{Key: "leftover", Value: fmt.Sprintf(
			"the server-side copy %q could not be deleted (%s) — it sits in the server's "+
				"snapshot storage until removed", snap.Name, verr.Message)})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// snapshotInfo is what the server says about a snapshot it just made.
type snapshotInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// createSnapshot asks the server to write one, waiting for it to finish —
// through slowCall, because snapshotting walks every segment and takes as
// long as the collection is large.
func createSnapshot(ctx context.Context, req plugin.Request, collection string) (snapshotInfo, *view.Error) {
	var snap snapshotInfo
	if verr := slowCall(ctx, req, http.MethodPost,
		pathFor("/collections/%s/snapshots", collection)+"?wait=true", nil, &snap); verr != nil {
		return snapshotInfo{}, verr
	}
	if snap.Name == "" {
		return snapshotInfo{}, view.Errorf("qdrant.dump.nosnapshot",
			"the server reported no snapshot name").
			WithHint("this may be a Qdrant version whose response shape has moved")
	}
	return snap, nil
}

// downloadSnapshot streams the server-side snapshot into a fresh local file.
//
// **The exclusive create is the no-overwrite guarantee**, one syscall rather
// than a stat followed by a create, which is a race a backup should not
// have. 0600 at creation rather than chmod'd afterwards, so there is no
// instant where the snapshot is both complete and readable by everyone. A
// failed transfer takes its half-written file with it, because a partial
// snapshot is the one that gets restored six months later.
func downloadSnapshot(ctx context.Context, req plugin.Request,
	collection, name, path string) (int64, *view.Error) {
	httpReq, verr := newRequest(ctx, req, http.MethodGet,
		pathFor("/collections/%s/snapshots/", collection)+pathFor("%s", name), nil)
	if verr != nil {
		return 0, verr
	}
	client, verr := httpClient(req)
	if verr != nil {
		return 0, verr
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, classify(err, req)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		return 0, classifyStatus(resp.StatusCode, payload, req)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return 0, alreadyThere(path)
	}
	if err != nil {
		return 0, view.Errorf("qdrant.dump.create", "creating %s: %v", path, err)
	}
	// Unbounded on purpose, unlike every enveloped call: the body is the
	// snapshot, its size is the collection's, and the destination is the
	// operator's own disk — the bound that matters is the one the filesystem
	// enforces.
	written, err := io.Copy(f, resp.Body)
	closeErr := f.Close()
	switch {
	case err != nil:
		_ = os.Remove(path)
		return 0, view.Errorf("qdrant.dump.transfer", "downloading the snapshot: %v", err).
			WithHint("the partial file has been removed")
	case closeErr != nil:
		_ = os.Remove(path)
		return 0, view.Errorf("qdrant.dump.write", "finishing %s: %v", path, closeErr)
	}
	return written, nil
}

// deleteSnapshot removes the server-side copy. It runs detached from the
// caller's cancellation — an operator's interrupt mid-download must not also
// skip the cleanup, or every aborted dump leaves a snapshot eating the
// server's disk — but bounded, because cleanup hanging forever is worse than
// cleanup reported as failed.
func deleteSnapshot(ctx context.Context, req plugin.Request, collection, name string) *view.Error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
	defer cancel()
	return slowCall(cleanup, req, http.MethodDelete,
		pathFor("/collections/%s/snapshots/", collection)+pathFor("%s", name)+"?wait=true", nil, nil)
}

// serverVersion asks the root endpoint what it is — best effort, for the
// receipt: a dump that succeeded is not failed retroactively because the
// decoration could not be read.
func serverVersion(ctx context.Context, req plugin.Request) string {
	var root struct {
		Version string `json:"version"`
	}
	if verr := getRaw(ctx, req, "/", &root); verr != nil || root.Version == "" {
		return "unknown version"
	}
	return "Qdrant " + root.Version
}

func describeContents(info collectionInfo, version string) string {
	points := "point count unknown"
	if info.PointsCount != nil {
		points = fmt.Sprintf("%d points", *info.PointsCount)
	}
	return fmt.Sprintf("%s at snapshot time, with indexes and collection config — from %s",
		points, version)
}

func alreadyThere(path string) *view.Error {
	return view.Errorf("qdrant.dump.exists", "%s already exists", path).
		WithHint("a snapshot is never written over an existing file — name a new one, or " +
			"move that one aside")
}

// expandHome resolves a leading ~ the way every other consumer of a Path
// input in this codebase does. Mirrors builtin/kv, builtin/keys and
// plugins/pg's own copies rather than centralizing a shared helper: an
// external plugin cannot reach internal/pathguard, and the rule is ten lines.
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
