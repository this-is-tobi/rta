package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A whole bucket onto local disk, for a person.
//
// **The security problem here is not the one the other backups have.** A
// database dump writes one file to a path the operator named; this writes
// thousands of files to paths a *remote service* named, because an object
// key becomes a filename. A key of `../../../../etc/cron.d/root` is a legal
// S3 key, and joining it to a destination directory without checking is the
// oldest archive-extraction bug there is. Nothing in this plugin did any
// path handling before — `s3.object.get --out` writes to the operator's own
// path and never derives one from a key — so the confinement below is new
// code rather than a helper being reused, and it is the part of this file
// worth reviewing.
//
// It refuses MCP outright, for the same reason pg.dump and vault.snapshot
// do: a whole bucket has no blast radius a grant could name. --out is also
// Local, so an MCP caller could not choose the destination even if the
// refusal were removed; the two protect against different mistakes.

// downloadLimit bounds how many objects one call will take. A build-artifact
// bucket holds millions of keys and a backup of one is a different operation
// from the one this capability is: refused past the bound rather than
// truncated, because a partial backup reported as a whole one is the failure
// this whole family of capabilities exists to avoid.
const downloadLimit = 10000

// unsafeShown bounds how many refused keys the error lists. Enough to see
// the shape of the problem without an error message the size of a bucket.
const unsafeShown = 10

func s3BucketDownloadCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "s3.bucket.download",
		Summary: "Copy a bucket to a local directory, for a person at a terminal",
		Safety:  plugin.Write,
		// Running it twice at the same --out refuses rather than merging into
		// a directory whose contents would then be from two different times.
		Idempotent: false,
		Description: "Every object under a prefix, onto local disk, in parallel. **Refuses MCP " +
			"outright** rather than asking for a grant — the line keys.backup and kv.copy draw, " +
			"and the one pg.dump and vault.snapshot follow: a whole bucket has no blast radius a " +
			"grant could name. An agent that needs an object asks for s3.object.get with a grant " +
			"naming that key.\n\n" +
			"**An object key becomes a filename, and the key comes from the server.** " +
			"`../../../../etc/cron.d/root` is a legal S3 key, so every destination is resolved and " +
			"checked to be inside the directory you named; a key that escapes refuses the whole " +
			"download and names the keys rather than skipping them quietly, because a backup " +
			"missing the interesting objects is worse than one that did not run. The listing is " +
			"checked in full before anything is written, so a refusal costs no partial directory.\n\n" +
			"Written into a directory this creates — never one that already exists, so a backup " +
			"is never half of one run and half of another — at mode 0700 with each object at 0600. " +
			"A run that fails takes the whole directory with it. --parallel is the flag that " +
			"changes the transfer rate: object storage is latency-bound per object, so a bucket of " +
			"many small files goes as fast as you are willing to ask for at once.",
		Run: runBucketDownload,
	},
		bucketField("bucket to copy"),
		plugin.Field{Name: "prefix", Type: plugin.String, Live: true, Suggest: suggestKeys("prefix"),
			Help: "only objects under this prefix; empty means the whole bucket"},
		plugin.Field{Name: "out", Type: plugin.Path, Local: true,
			Help: "directory to create and write into; refused if it already exists"},
		plugin.Field{Name: "parallel", Type: plugin.Int, Default: 8, Min: 1, Max: 64,
			Help: "how many objects to download at once"},
		plugin.Field{Name: "limit", Type: plugin.Int, Default: downloadLimit, Min: 1, Max: 1000000,
			Help: "maximum objects to copy; a larger bucket is refused, not truncated"})
}

// humanOnly is this plugin's copy of the gate builtin/keys opens with, which
// pg.dump and vault.snapshot repeat. It comes first, before a client is
// built, so an agent's call never spends the operator's credentials on a
// question that was always going to be answered no.
func humanOnly(req plugin.Request, id string) *view.Error {
	if req.Surface() != plugin.SurfaceMCP {
		return nil
	}
	return view.Refusef("s3.human", "%s can only be run by a person at a terminal", id).
		WithHint("a whole bucket has no blast radius a grant could name — its one authorized " +
			"use is everything. Ask for the object you need with s3.object.get, which takes a " +
			"grant naming that key")
}

func runBucketDownload(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := humanOnly(req, "s3.bucket.download"); verr != nil {
		return nil, verr
	}

	out := strings.TrimSpace(req.String("out"))
	if out == "" {
		return nil, view.Errorf("s3.download.nooutput", "say where the objects should be written").
			WithHint("--out ./" + req.String("bucket") + "-backup — a bucket is a directory of " +
				"files, not something to read in a terminal")
	}
	root, err := filepath.Abs(expandHome(out))
	if err != nil {
		return nil, view.Errorf("s3.download.path", "resolving --out: %v", err)
	}

	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		// **List and check every key before writing anything.** Doing it in
		// one pass would mean discovering a hostile key with half a bucket
		// already on disk, and then having to decide whether to keep it.
		objects, verr := listForDownload(ctx, client, req)
		if verr != nil {
			return nil, verr
		}
		plan, verr := planDownload(root, objects)
		if verr != nil {
			return nil, verr
		}
		if req.DryRun {
			return view.Text{Body: fmt.Sprintf("would copy %d objects (%s) from %s into %s",
				len(plan), format.Bytes(uint64(totalSize(objects))), req.String("bucket"), root)}, nil
		}

		// Created rather than reused, so a backup is never half of one run
		// and half of another — and exclusively, which is the same
		// no-overwrite guarantee pg.dump and vault.snapshot make, in one
		// syscall. 0700 because what goes in it is every object in a bucket.
		// Parents first, so `--out ./backups/2026-08-29` works without
		// pre-creating `backups` — then the leaf exclusively, which is where
		// the no-overwrite guarantee lives. MkdirAll on the leaf would happily
		// accept an existing directory and lose it.
		if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
			return nil, view.Errorf("s3.download.create",
				"creating %s: %v", filepath.Dir(root), err)
		}
		if err := os.Mkdir(root, 0o700); errors.Is(err, os.ErrExist) {
			return nil, view.Errorf("s3.download.exists", "%s already exists", root).
				WithHint("a download always makes its own directory, so what lands in it is " +
					"one bucket at one moment — name a new one, or move that one aside")
		} else if err != nil {
			return nil, view.Errorf("s3.download.create", "creating %s: %v", root, err)
		}

		started := time.Now()
		written, verr := fetchAll(ctx, client, req, plan)
		if verr != nil {
			// A partial backup is the one that gets restored six months
			// later, so the whole directory goes rather than staying to be
			// mistaken for a complete copy.
			_ = os.RemoveAll(root)
			return nil, verr
		}

		return view.KeyValue{Pairs: []view.Pair{
			{Key: "wrote", Value: root},
			{Key: "objects", Value: fmt.Sprintf("%d", len(plan))},
			{Key: "size", Value: format.Bytes(uint64(written))},
			{Key: "took", Value: time.Since(started).Round(time.Millisecond).String()},
			{Key: "from", Value: req.String("bucket") + "/" + req.String("prefix")},
			{Key: "at rest", Value: "unencrypted, directory 0700 and files 0600 — " +
				"`rta kv` or `age` if it is going anywhere"},
		}}, nil
	})
}

// listForDownload reads the whole listing, bounded.
func listForDownload(ctx context.Context, client *minio.Client,
	req plugin.Request) ([]minio.ObjectInfo, *view.Error) {
	limit := req.Int("limit")
	var out []minio.ObjectInfo
	for obj := range client.ListObjects(ctx, req.String("bucket"), minio.ListObjectsOptions{
		Prefix:    req.String("prefix"),
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, classify(obj.Err, req)
		}
		// A key ending in "/" is a folder marker — what the AWS console
		// creates when somebody makes a "folder" — and not an object. Skipped
		// regardless of size rather than only when empty: `nested/` and
		// `nested/a.txt` both resolve to a path named `nested`, so keeping it
		// would mean writing a file where a directory has to go, and the size
		// is not what decides that. This is the one thing here that is
		// dropped rather than refused, because it is a naming convention
		// rather than a key that could not be stored — every S3 client reads
		// it the same way.
		if strings.HasSuffix(obj.Key, "/") {
			continue
		}
		if len(out) == limit {
			return nil, view.Errorf("s3.download.toomany",
				"%s holds more than %d objects", req.String("bucket"), limit).
				WithHint("raise --limit, or narrow it with --prefix — refused rather than " +
					"truncated, because a backup missing objects nobody named is worse than " +
					"one that did not run")
		}
		out = append(out, obj)
	}
	return out, nil
}

// target is one object and the local file it will become.
type target struct {
	key  string
	path string
}

// planDownload resolves every key to a local path and refuses the download if
// any of them escapes.
//
// **Refused rather than skipped, and refused before anything is written.** A
// key that cannot be stored under the destination is either an attack or a
// bucket nobody understands, and both deserve a person looking rather than a
// line in a summary. Skipping quietly would produce a directory that looks
// like a complete backup and is not.
func planDownload(root string, objects []minio.ObjectInfo) ([]target, *view.Error) {
	var plan []target
	var unsafe []string
	for _, obj := range objects {
		path, err := destinationFor(root, obj.Key)
		if err != nil {
			if len(unsafe) < unsafeShown {
				unsafe = append(unsafe, fmt.Sprintf("%q (%s)", obj.Key, err))
			}
			continue
		}
		plan = append(plan, target{key: obj.Key, path: path})
	}
	if len(unsafe) > 0 {
		return nil, view.Errorf("s3.download.unsafekey",
			"%d object key(s) would be written outside %s: %s",
			len(unsafe), root, strings.Join(unsafe, ", ")).
			WithHint("an object key becomes a filename and the key comes from the server, so " +
				"one that escapes the destination is refused rather than skipped — nothing has " +
				"been written. Narrow the copy with --prefix to exclude them")
	}
	return plan, nil
}

// destinationFor turns an object key into a local path inside root, or says
// why it cannot.
//
// The check is `filepath.Rel` on the joined path rather than a scan for
// ".." in the key, because the scan is the version that gets defeated: it
// has to anticipate encodings, separators and normalisation on three
// platforms, and Join already does the normalising this needs. Whatever the
// key contains, the question asked here is the only one that matters — after
// resolution, is this path still under the directory the operator named.
func destinationFor(root, key string) (string, error) {
	if key == "" {
		return "", errors.New("empty key")
	}
	if strings.ContainsRune(key, 0) {
		// No filesystem here takes it, and it is how a checked prefix gets
		// separated from what is actually opened.
		return "", errors.New("contains a NUL byte")
	}
	// A leading separator is legal in a key and means nothing about the local
	// filesystem; trimmed rather than refused, since it is otherwise an
	// ordinary relative key.
	trimmed := strings.TrimLeft(key, "/")
	if trimmed == "" {
		return "", errors.New("names no object")
	}

	path := filepath.Join(root, filepath.FromSlash(trimmed))
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", errors.New("cannot be resolved against the destination")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("escapes the destination directory")
	}
	if rel == "." {
		return "", errors.New("names the destination directory itself")
	}
	return path, nil
}

// fetchAll downloads the plan with the requested concurrency.
//
// **--parallel is the flag that changes the transfer rate here**, and the
// reason is different from pg.dump's: object storage is latency-bound per
// object rather than throughput-bound, so a bucket of ten thousand small
// files spends nearly all of its time waiting. One error stops everything
// and the caller removes the directory, because a backup that is missing
// objects and does not say so is the artifact this refuses to produce.
func fetchAll(ctx context.Context, client *minio.Client, req plugin.Request,
	plan []target) (int64, *view.Error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	work := make(chan target)
	var (
		mu      sync.Mutex
		written int64
		failure *view.Error
	)

	workers := min(req.Int("parallel"), len(plan))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				n, verr := fetchOne(ctx, client, req, t)
				mu.Lock()
				if verr != nil && failure == nil {
					failure = verr
					cancel()
				}
				written += n
				mu.Unlock()
			}
		}()
	}

	for _, t := range plan {
		select {
		case work <- t:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()

	if failure != nil {
		return 0, failure
	}
	return written, nil
}

// fetchOne streams one object to its resolved path.
func fetchOne(ctx context.Context, client *minio.Client, req plugin.Request,
	t target) (int64, *view.Error) {
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return 0, view.Errorf("s3.download.create", "creating %s: %v", filepath.Dir(t.path), err)
	}
	obj, err := client.GetObject(ctx, req.String("bucket"), t.key, minio.GetObjectOptions{})
	if err != nil {
		return 0, classify(err, req)
	}
	defer func() { _ = obj.Close() }()

	// O_EXCL, so two keys that resolve to the same path — possible on a
	// case-insensitive filesystem holding `Report` and `report` — is an error
	// rather than one silently replacing the other.
	f, err := os.OpenFile(t.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return 0, view.Errorf("s3.download.collision",
			"two object keys resolve to the same local file: %s", t.path).
			WithHint("usually a case-insensitive filesystem holding keys that differ only in " +
				"case — copy to a case-sensitive volume, or narrow it with --prefix")
	}
	if err != nil {
		return 0, view.Errorf("s3.download.create", "creating %s: %v", t.path, err)
	}
	// Streamed rather than buffered: an object is whatever size it is, and
	// this is a person's explicit copy of it.
	n, copyErr := io.Copy(f, obj)
	closeErr := f.Close()
	if copyErr != nil {
		return 0, classify(copyErr, req)
	}
	if closeErr != nil {
		return 0, view.Errorf("s3.download.write", "finishing %s: %v", t.path, closeErr)
	}
	return n, nil
}

func totalSize(objects []minio.ObjectInfo) int64 {
	var total int64
	for _, o := range objects {
		total += o.Size
	}
	return total
}
