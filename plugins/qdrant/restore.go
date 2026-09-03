package main

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The other half of qdrant.dump — the file back into a collection.
//
// **It refuses MCP for the dump's reason run in reverse.** The dump refuses
// because everything would leave; a restore is everything arriving —
// whatever the file says becomes the collection, wholesale. Neither
// direction has a blast radius a grant could name, so both belong to the
// person at the keyboard. Destructive besides, because that is what it is:
// Qdrant's snapshot recovery replaces the collection rather than merging
// into it, so unlike the dump this one gets the --yes gate a person should
// have to type through.
//
// **A collection already holding points is refused unless --replace says
// that is the point** — the dump's O_EXCL pointing the other way. The dump
// never writes over an existing file; the restore never lands on a
// collection somebody's search is running against, silently. An existing
// but empty collection passes: recovery replaces its config along with its
// contents, and there is nothing in it to lose.
//
// **priority=snapshot is always sent, and it is the one flag that decides
// what "restore" means.** On a distributed deployment, recovery without it
// prefers the data the replicas already hold — the restore that reports
// success and restores nothing, which is the worst answer a backup tool can
// give. The snapshot is the authority here by definition: a person chose a
// file and asked for it back.
//
// The file's only validation is that it exists and is not empty. The server
// is the sole reader of its own snapshot format and rejects a corrupt one
// with an error classifyStatus hands through — unlike pg.restore, there is
// no wrong-tool failure mode to head off, because there is only one tool.

func restoreCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "qdrant.restore",
		Summary: "Restore a qdrant.dump snapshot into a collection, for a person at a terminal",
		// Destructive, because that is what it is: recovery replaces the
		// collection wholesale. The class buys the --yes gate a person
		// should have to type through.
		Safety:     plugin.Destructive,
		Idempotent: false,
		Description: "The other half of qdrant.dump — the file back into a collection. **Refuses " +
			"MCP outright** for the dump's reason run in reverse: the dump refuses because " +
			"everything would leave, and a restore is everything arriving, becoming the " +
			"collection wholesale. Neither direction has a blast radius a grant could name, " +
			"so both belong to the person at the keyboard.\n\n" +
			"**A collection already holding points is refused unless --replace says that is " +
			"the point**, which is the dump's no-overwrite rule pointing the other way. The " +
			"collection named here does not have to be the one the snapshot came from — " +
			"restoring into a fresh name is how you inspect a backup without touching the " +
			"original.\n\n" +
			"Recovery is the server's own: the snapshot carries the collection's config and " +
			"indexes, and priority=snapshot makes the file the authority — without it a " +
			"distributed deployment prefers what its replicas already hold, which is a " +
			"restore that reports success and restores nothing. The receipt reports what the " +
			"collection holds afterwards, read back rather than assumed.",
		Run: runRestore,
	}, collectionField("collection to restore into (created if missing)"),
		plugin.Field{Name: "file", Type: plugin.Path, Local: true, Positional: true,
			Required: true,
			Help:     "the snapshot to restore — what qdrant.dump wrote"},
		plugin.Field{Name: "replace", Type: plugin.Bool,
			Help: "hand a collection that already holds points over to the snapshot"})
}

func runRestore(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := humanOnly(req, "qdrant.restore",
		"a restore makes a file the whole collection — a blast radius no grant could "+
			"name, in the direction that overwrites. The snapshot this file came from was "+
			"made by a person at a terminal, and it goes back the same way"); verr != nil {
		return nil, verr
	}
	collection := req.String("collection")

	path, err := expandHome(strings.TrimSpace(req.String("file")))
	if err != nil {
		return nil, view.Errorf("qdrant.restore.path", "resolving the snapshot path: %v", err)
	}
	if verr := checkSnapshotFile(path); verr != nil {
		return nil, verr
	}

	if req.DryRun {
		return view.Text{Body: fmt.Sprintf(
			"would upload %s to %s, recovering collection %q from it (priority=snapshot)",
			path, req.String("endpoint"), collection)}, nil
	}

	// Ask the server what is there before writing into it — the dump's
	// describeSource discipline, with the one question only a restore has to
	// ask: is there a live collection about to be replaced?
	if verr := checkTarget(ctx, req, collection); verr != nil {
		return nil, verr
	}

	started := time.Now()
	if verr := uploadSnapshot(ctx, req, collection, path); verr != nil {
		return nil, verr
	}

	return view.KeyValue{Pairs: []view.Pair{
		{Key: "restored", Value: path},
		{Key: "into", Value: fmt.Sprintf("collection %q on %s", collection, req.String("endpoint"))},
		{Key: "took", Value: time.Since(started).Round(time.Millisecond).String()},
		// Read back rather than assumed: the count is the server's own answer
		// about what the collection now holds, which is the closest thing a
		// restore has to proof it restored.
		{Key: "now holds", Value: countAfterRestore(ctx, req, collection)},
		{Key: "guarantee", Value: "the snapshot replaced the collection wholesale — config, " +
			"indexes and points came from the file, not from what was there"},
	}}, nil
}

// checkSnapshotFile refuses the two files that would waste a destructive
// call: one that is not there, and one that is empty — restoring an empty
// file would fail against the server anyway, but "the snapshot did not
// finish being written" is the answer, and the server does not know it.
func checkSnapshotFile(path string) *view.Error {
	info, err := os.Stat(path)
	if err != nil {
		return view.Errorf("qdrant.restore.missing", "no snapshot at %s", path).
			WithHint("`rta qdrant dump <collection> --out <path>` writes one; this restores " +
				"what that wrote")
	}
	if info.IsDir() {
		return view.Errorf("qdrant.restore.notafile", "%s is a directory", path).
			WithHint("a qdrant.dump snapshot is a single file")
	}
	if info.Size() == 0 {
		return view.Errorf("qdrant.restore.empty", "%s is empty", path).
			WithHint("an empty file holds no collection — if this was a dump, it did not finish")
	}
	return nil
}

// checkTarget refuses a collection that already holds points, unless
// --replace names the intent. A missing collection passes — recovery creates
// it — and so does an existing empty one, which has nothing to lose.
func checkTarget(ctx context.Context, req plugin.Request, collection string) *view.Error {
	var info collectionInfo
	verr := get(ctx, req, pathFor("/collections/%s", collection), &info)
	if verr != nil {
		if verr.Code == "qdrant.notfound" {
			return nil
		}
		return verr
	}
	if req.Bool("replace") {
		return nil
	}
	if info.PointsCount != nil && *info.PointsCount > 0 {
		return view.Errorf("qdrant.restore.notempty",
			"%q already holds %d points", collection, *info.PointsCount).
			WithHint("--replace hands the collection to the snapshot wholesale, or restore " +
				"into a fresh name — the snapshot does not care what the collection it lands " +
				"in is called")
	}
	return nil
}

// uploadSnapshot streams the file to the server's recovery endpoint as
// multipart form data — through a pipe, so a multi-gigabyte snapshot never
// sits in this process's memory, and without the requestTimeout bound, for
// slowCall's reason: the transfer takes as long as the file is large.
func uploadSnapshot(ctx context.Context, req plugin.Request, collection, path string) *view.Error {
	f, err := os.Open(path)
	if err != nil {
		return view.Errorf("qdrant.restore.unreadable", "opening %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("snapshot", filepath.Base(path))
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, f); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.CloseWithError(mw.Close())
	}()

	httpReq, verr := newRequest(ctx, req, http.MethodPost,
		pathFor("/collections/%s/snapshots/upload", collection)+"?priority=snapshot&wait=true", pr)
	if verr != nil {
		return verr
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	client, verr := httpClient(req)
	if verr != nil {
		return verr
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return classify(err, req)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return classify(err, req)
	}
	if resp.StatusCode >= 300 {
		return classifyStatus(resp.StatusCode, payload, req)
	}
	return nil
}

// countAfterRestore reads what the collection reports now — best effort, for
// the receipt: a restore that succeeded is not failed retroactively because
// the read-back could not be made.
func countAfterRestore(ctx context.Context, req plugin.Request, collection string) string {
	var info collectionInfo
	if verr := get(ctx, req, pathFor("/collections/%s", collection), &info); verr != nil {
		return "unknown — the collection could not be read back: " + verr.Message
	}
	if info.PointsCount == nil {
		return "unknown — the server reported no point count"
	}
	return fmt.Sprintf("%d points", *info.PointsCount)
}
