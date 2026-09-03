package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"mime"
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

// A local directory into a bucket — s3.bucket.download's other half, which
// makes the pair this plugin's restore story.
//
// **The traversal hazard runs the other way here.** The download's danger is
// a remote key becoming a local path; the upload's is a local *symlink*
// becoming remote content — a link inside the directory pointing at
// ~/.ssh/id_ed25519 would ship the operator's key to the bucket as
// faithfully as any file. So the walk takes regular files only, and a
// symlink refuses the whole upload by name rather than being skipped
// quietly: a backup directory this plugin wrote contains no links, so one
// appearing means either an attack or a directory nobody understands, and
// both deserve a person looking. The same goes for sockets and fifos, which
// additionally hang the reader.
//
// **It refuses MCP for the download's reason run in reverse**: everything
// arriving instead of everything leaving, no blast radius a grant could
// name. Destructive rather than Write, unlike the download, because with
// --overwrite it can replace remote objects — and the --yes gate is what a
// person should type through before a directory becomes a bucket.
//
// **A failed upload deletes nothing remote**, which deliberately breaks the
// download's take-the-partial-with-you symmetry. The download's partial is
// rta's own file to remove; the upload's partial lives in the bucket where
// a delete could also destroy what --overwrite already replaced — cleanup
// that can compound the damage is not cleanup. The error says the prefix
// may hold a partial upload instead, so the operator decides.

func s3BucketUploadCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "s3.bucket.upload",
		Summary: "Copy a local directory into a bucket, for a person at a terminal",
		// Destructive, not Write like the download: with --overwrite this
		// replaces remote objects, and the class buys the --yes gate a
		// person should have to type through before a directory becomes a
		// bucket.
		Safety:     plugin.Destructive,
		Idempotent: false,
		Scope:      "bucket",
		Description: "Every regular file under a directory, into a bucket, in parallel — " +
			"s3.bucket.download's other half, and what restores one of its backups. **Refuses " +
			"MCP outright** for the download's reason run in reverse: everything arriving " +
			"instead of everything leaving, and no blast radius a grant could name. An agent " +
			"that needs to write an object asks for s3.object.set with a grant naming that " +
			"key.\n\n" +
			"**A destination already holding objects under the prefix is refused unless " +
			"--overwrite says that is the point** — the download's fresh-directory rule " +
			"pointing the other way. --overwrite replaces objects whose keys collide and " +
			"leaves the rest, and the receipt says so.\n\n" +
			"Regular files only: a symlink refuses the whole upload by name — a link pointing " +
			"at a credential would ship it as faithfully as any file, and a backup directory " +
			"this plugin wrote contains no links, so one appearing deserves a person looking. " +
			"Refused past --limit rather than truncated. A failed upload deletes nothing " +
			"remote: a delete could also destroy what --overwrite already replaced, so the " +
			"error names the possibly-partial prefix and the operator decides.",
		Run: runBucketUpload,
	},
		bucketField("bucket to upload into"),
		plugin.Field{Name: "dir", Type: plugin.Path, Local: true, Positional: true,
			Required: true,
			Help:     "directory whose files become objects — what s3.bucket.download wrote"},
		plugin.Field{Name: "prefix", Type: plugin.String, Live: true, Suggest: suggestKeys("prefix"),
			Help: "key prefix to upload under; empty means the bucket root"},
		plugin.Field{Name: "overwrite", Type: plugin.Bool,
			Help: "allow replacing remote objects whose keys collide; others are left as they are"},
		plugin.Field{Name: "parallel", Type: plugin.Int, Default: 8, Min: 1, Max: 64,
			Help: "how many objects to upload at once"},
		plugin.Field{Name: "limit", Type: plugin.Int, Default: downloadLimit, Min: 1, Max: 1000000,
			Help: "maximum files to upload; a larger directory is refused, not truncated"})
}

func runBucketUpload(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := humanOnly(req, "s3.bucket.upload",
		"a directory's whole contents into a bucket has no blast radius a grant could "+
			"name, in the direction that overwrites. Ask to write the object you need with "+
			"s3.object.set, which takes a grant naming that key"); verr != nil {
		return nil, verr
	}

	root, err := filepath.Abs(expandHome(strings.TrimSpace(req.String("dir"))))
	if err != nil {
		return nil, view.Errorf("s3.upload.path", "resolving the directory: %v", err)
	}
	prefix := normalizePrefix(req.String("prefix"))

	plan, total, verr := planUpload(root, req.Int("limit"))
	if verr != nil {
		return nil, verr
	}

	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		if verr := checkUploadTarget(ctx, client, req, prefix); verr != nil {
			return nil, verr
		}
		if req.DryRun {
			return view.Text{Body: fmt.Sprintf("would upload %d files (%s) from %s into %s/%s",
				len(plan), format.Bytes(uint64(total)), root, req.String("bucket"), prefix)}, nil
		}

		started := time.Now()
		written, verr := putAll(ctx, client, req, prefix, plan)
		if verr != nil {
			return nil, verr
		}

		pairs := []view.Pair{
			{Key: "uploaded", Value: root},
			{Key: "objects", Value: fmt.Sprintf("%d", len(plan))},
			{Key: "size", Value: format.Bytes(uint64(written))},
			{Key: "took", Value: time.Since(started).Round(time.Millisecond).String()},
			{Key: "to", Value: req.String("bucket") + "/" + prefix},
		}
		if req.Bool("overwrite") {
			pairs = append(pairs, view.Pair{Key: "overwrite",
				Value: "remote objects were replaced where keys collided; others were left as they were"})
		}
		return view.KeyValue{Pairs: pairs}, nil
	})
}

// normalizePrefix makes a non-empty prefix end in "/", because the upload
// *constructs* keys from it — "backup" + "a.txt" must become "backup/a.txt",
// not "backupa.txt". The download passes its prefix to S3 raw, where prefix
// means string-prefix by definition; the asymmetry is deliberate and this
// comment is where it lives.
func normalizePrefix(p string) string {
	p = strings.TrimLeft(strings.TrimSpace(p), "/")
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// upload is one local file and the key it will become.
type upload struct {
	path string
	key  string
	size int64
}

// planUpload walks the directory and resolves every regular file to a key,
// refusing the whole upload — before anything is sent — on a symlink or any
// other non-regular entry, on a directory over the limit, and on a directory
// with nothing to send: an empty upload reporting success is the lie an
// empty dump file tells, in directory form.
func planUpload(root string, limit int) ([]upload, int64, *view.Error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, 0, view.Errorf("s3.upload.missing", "no directory at %s", root).
			WithHint("`rta s3 bucket download --out <dir>` writes one; this uploads what that wrote")
	}
	if !info.IsDir() {
		return nil, 0, view.Errorf("s3.upload.notadir", "%s is not a directory", root).
			WithHint("one file is s3.object.set's job — this uploads a directory")
	}

	var plan []upload
	var total int64
	var unsafe []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			if len(unsafe) < unsafeShown {
				kind := "not a regular file"
				if d.Type()&fs.ModeSymlink != 0 {
					kind = "a symlink"
				}
				unsafe = append(unsafe, fmt.Sprintf("%q (%s)", path, kind))
			}
			return nil
		}
		if len(plan) == limit {
			return errTooManyFiles
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		fi, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		plan = append(plan, upload{path: path, key: filepath.ToSlash(rel), size: fi.Size()})
		total += fi.Size()
		return nil
	})
	switch {
	case walkErr == errTooManyFiles:
		return nil, 0, view.Errorf("s3.upload.toomany",
			"%s holds more than %d files", root, limit).
			WithHint("raise --limit, or upload a subdirectory — refused rather than truncated, " +
				"because a restore missing files nobody named is worse than one that did not run")
	case walkErr != nil:
		return nil, 0, view.Errorf("s3.upload.walk", "reading %s: %v", root, walkErr)
	}
	if len(unsafe) > 0 {
		return nil, 0, view.Errorf("s3.upload.notregular",
			"%d entr(ies) under %s are not regular files: %s",
			len(unsafe), root, strings.Join(unsafe, ", ")).
			WithHint("a symlink would upload whatever it points at — a credential included — so " +
				"it refuses the upload rather than being followed or skipped quietly. Nothing " +
				"has been sent; remove or replace the entries and run it again")
	}
	if len(plan) == 0 {
		return nil, 0, view.Errorf("s3.upload.empty", "%s holds no files", root).
			WithHint("an empty directory uploads nothing and would report success — if this was " +
				"a backup, it did not finish")
	}
	return plan, total, nil
}

// errTooManyFiles is a sentinel for the walk; planUpload turns it into the
// coded refusal so the walk callback stays small.
var errTooManyFiles = errors.New("too many files")

// checkUploadTarget refuses a destination already holding objects under the
// prefix, unless --overwrite names the intent — the download's
// fresh-directory rule pointing the other way. One listed object is enough
// to answer, so the listing stops at the first.
func checkUploadTarget(ctx context.Context, client *minio.Client, req plugin.Request,
	prefix string) *view.Error {
	if req.Bool("overwrite") {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for obj := range client.ListObjects(ctx, req.String("bucket"), minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return classify(obj.Err, req)
		}
		where := req.String("bucket") + "/" + prefix
		return view.Errorf("s3.upload.notempty",
			"%s already holds objects (%s, and possibly more)", where, obj.Key).
			WithHint("--overwrite replaces objects whose keys collide and leaves the rest, or " +
				"upload under a fresh --prefix — the bucket does not care what the prefix is " +
				"called")
	}
	return nil
}

// putAll uploads the plan with the requested concurrency — fetchAll's shape,
// pointing the other way. One error stops everything; what was already sent
// stays, for the reason the file comment gives: a remote delete could also
// destroy what --overwrite already replaced.
func putAll(ctx context.Context, client *minio.Client, req plugin.Request,
	prefix string, plan []upload) (int64, *view.Error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	work := make(chan upload)
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
			for u := range work {
				n, verr := putOne(ctx, client, req, prefix, u)
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

	for _, u := range plan {
		select {
		case work <- u:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()

	if failure != nil {
		partial := req.String("bucket") + "/" + prefix + " may hold a partial upload; rta does " +
			"not delete remote objects on failure, because with --overwrite a delete could " +
			"also destroy what was already replaced"
		if failure.Hint != "" {
			partial = failure.Hint + " — " + partial
		}
		return 0, failure.WithHint(partial)
	}
	return written, nil
}

// putOne sends one file. FPutObject streams from the path — multipart for a
// large file, one request for a small one — so the file never sits in this
// process's memory. The content type rides the extension: a restored web
// asset that comes back as application/octet-stream serves as a download
// where it used to render, which is a working-but-wrong restore.
func putOne(ctx context.Context, client *minio.Client, req plugin.Request,
	prefix string, u upload) (int64, *view.Error) {
	contentType := mime.TypeByExtension(filepath.Ext(u.path))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	info, err := client.FPutObject(ctx, req.String("bucket"), prefix+u.key, u.path,
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return 0, classify(err, req)
	}
	return info.Size, nil
}
