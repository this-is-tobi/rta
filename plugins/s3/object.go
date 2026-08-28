package main

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/minio/minio-go/v7"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func keyField(help string) plugin.Field {
	return plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: help,
		Live: true, Suggest: suggestKeys("key")}
}

func s3ObjectListCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "s3.object.list",
		Summary:    "List objects in a bucket",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Grouped by \"/\" like a directory listing unless --recursive flattens the " +
			"whole prefix.",
		Run: runObjectList,
	}, bucketField("bucket to list"),
		plugin.Field{Name: "prefix", Type: plugin.String, Help: "only keys starting with this",
			Live: true, Suggest: suggestKeys("prefix")},
		plugin.Field{Name: "recursive", Type: plugin.Bool, Help: "ignore the \"/\" delimiter and list everything under prefix"})
}

func runObjectList(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		bucket := req.String("bucket")
		t := view.Table{Columns: []view.Column{{Name: "Key"}, {Name: "Size"}, {Name: "Modified"}}}
		for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
			Prefix:    req.String("prefix"),
			Recursive: req.Bool("recursive"),
		}) {
			if obj.Err != nil {
				return nil, classify(obj.Err, req)
			}
			if strings.HasSuffix(obj.Key, "/") && obj.Size == 0 {
				// A common-prefix "directory" marker under a non-recursive
				// listing, not a real object — shown as a folder, not a
				// zero-byte file nobody wrote.
				t.Rows = append(t.Rows, []string{obj.Key, "", ""})
				continue
			}
			t.Rows = append(t.Rows, []string{obj.Key, strconv.FormatInt(obj.Size, 10), obj.LastModified.Format("2006-01-02 15:04")})
		}
		t.Total = len(t.Rows)
		return t, nil
	})
}

func s3ObjectShowCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "s3.object.show",
		Summary:    "Show one object's metadata — size, type, etag — never its content",
		Safety:     plugin.Read,
		Idempotent: true,
		Run:        runObjectShow,
	}, bucketField("bucket the object is in"), keyField("object to inspect"))
}

func runObjectShow(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		info, err := client.StatObject(ctx, req.String("bucket"), req.String("key"), minio.StatObjectOptions{})
		if err != nil {
			return nil, classify(err, req)
		}
		pairs := []view.Pair{
			{Key: "size", Value: strconv.FormatInt(info.Size, 10)},
			{Key: "content-type", Value: info.ContentType},
			{Key: "etag", Value: info.ETag},
			{Key: "modified", Value: info.LastModified.Format("2006-01-02 15:04:05")},
		}
		if info.StorageClass != "" {
			pairs = append(pairs, view.Pair{Key: "storage-class", Value: info.StorageClass})
		}
		return view.KeyValue{Pairs: pairs}, nil
	})
}

// s3.object.get is Write+NeedsGrant, the same correction kv.get made:
// revealing an object's content is the sensitive act, whatever the HTTP
// verb underneath is called. --out is Local — a person's flag only, since a
// grant authorizes revealing one object's content, not choosing which file
// on this machine it gets written to (kv.get's own reasoning, verbatim).
func s3ObjectGetCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID: "s3.object.get", Summary: "Download an object's content", Safety: plugin.Write, Idempotent: true,
		NeedsGrant: true, Scope: "key",
		Description: "Writes the content to stdout with no framing; for the byte-exact copy, or " +
			"anything binary, --out writes it to a file (0600) instead — a person's flag only, " +
			"since a grant authorizes revealing the content, not choosing where on this machine " +
			"it lands. An MCP caller always gets the content back in the response, bounded the " +
			"same way http.get bounds a response body.",
		Run: runObjectGet,
	}, bucketField("bucket the object is in"), keyField("object to reveal"),
		plugin.Field{Name: "out", Type: plugin.Path, Local: true, Help: "write the content to this file instead of printing it"})
}

// maxInline mirrors http's maxBody: enough for anything worth printing to a
// terminal or handing to an agent, not a general-purpose download path —
// --out exists for the rest, and streams rather than buffering.
const maxInline = 1 << 20 // 1 MiB

func runObjectGet(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		bucket, key := req.String("bucket"), req.String("key")
		// GetObject is lazy: it validates the names and returns, and the
		// HTTP request does not happen until the first Read. So the error
		// that actually says "no such key" arrives from io.Copy/io.ReadAll
		// below, not from here — both of those go through classify for
		// exactly that reason, and wrapping them in a local code instead
		// would turn every real S3 error on this path into "reading
		// bucket/key: ...".
		obj, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
		if err != nil {
			return nil, classify(err, req)
		}
		defer obj.Close()

		if out := req.String("out"); out != "" {
			f, ferr := os.OpenFile(expandHome(out), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if ferr != nil {
				return nil, view.Errorf("s3.object.out", "opening %s: %v", out, ferr)
			}
			defer f.Close()
			n, cerr := io.Copy(f, obj)
			if cerr != nil {
				return nil, classify(cerr, req)
			}
			return view.Text{Body: "wrote " + strconv.FormatInt(n, 10) + " bytes to " + out}, nil
		}

		body, err := io.ReadAll(io.LimitReader(obj, maxInline+1))
		if err != nil {
			return nil, classify(err, req)
		}
		if len(body) > maxInline {
			return nil, view.Errorf("s3.object.toolarge", "%s/%s is larger than %d bytes", bucket, key, maxInline).
				WithHint("use --out to write it to a file instead of printing it")
		}
		return view.Text{Body: string(body)}, nil
	})
}

// s3.object.set is Write+NeedsGrant, the same overwrite risk kv.set is
// gated for: setting a key that already exists replaces the object and
// nothing keeps the old one. --file is Local for the mirror-image reason
// kv.set's own --file is: it names a path on *this* machine, and a grant to
// write one key does not say which of the host's files may be read into
// it — without that, "upload the build artifact" would be reachable as
// "upload ~/.aws/credentials", the agent picking the path and this
// capability's own success message confirming what it found.
func s3ObjectSetCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID: "s3.object.set", Summary: "Upload (or overwrite) an object", Safety: plugin.Write, Idempotent: true,
		NeedsGrant: true, Scope: "key",
		Description: "The content comes from the argument or from --file; PutObject handles " +
			"large files with multipart upload internally, so there is no separate multipart " +
			"capability to reach for.",
		Run: runObjectSet,
	}, bucketField("bucket to write to"), keyField("object to set"),
		plugin.Field{Name: "value", Type: plugin.Text, Positional: true, Help: "content to upload"},
		plugin.Field{Name: "file", Type: plugin.Path, Local: true, Help: "upload this file's content instead"},
		plugin.Field{Name: "content-type", Type: plugin.String, Suggest: suggestContentTypes,
			Help: "MIME type; guessed from --file's extension if omitted"},
		plugin.Field{Name: "storage-class", Type: plugin.String, Suggest: suggestStorageClasses,
			Help: "e.g. STANDARD, STANDARD_IA, GLACIER — left to the server's default if omitted"})
}

func runObjectSet(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		bucket, key := req.String("bucket"), req.String("key")

		var body io.Reader
		var size int64 = -1
		contentType := req.String("content-type")
		if path := req.String("file"); path != "" {
			f, err := os.Open(expandHome(path))
			if err != nil {
				return nil, view.Errorf("s3.file.unreadable", "reading %s: %v", path, err)
			}
			defer f.Close()
			if info, err := f.Stat(); err == nil {
				size = info.Size()
			}
			body = f
		} else {
			value := req.String("value")
			if value == "" {
				return nil, view.Errorf("s3.set.novalue", "no content given").
					WithHint("pass a value, or --file to upload from disk")
			}
			body = strings.NewReader(value)
			size = int64(len(value))
			if contentType == "" {
				contentType = "text/plain; charset=utf-8"
			}
		}

		info, err := client.PutObject(ctx, bucket, key, body, size, minio.PutObjectOptions{
			ContentType:  contentType,
			StorageClass: req.String("storage-class"),
		})
		if err != nil {
			return nil, classify(err, req)
		}
		return view.Text{Body: "set " + bucket + "/" + key + " (" + strconv.FormatInt(info.Size, 10) + " bytes)"}, nil
	})
}

func s3ObjectRemoveCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID: "s3.object.rm", Summary: "Delete an object", Safety: plugin.Destructive,
		Description: "No history, no backup, no undo — the same loss `kv rm` is. S3's DELETE is " +
			"idempotent: removing a key that is already gone is not an error, on this or the real " +
			"call — --dry-run does not probe for existence first, since that would report a " +
			"failure the real call would not.",
		Run: runObjectRemove,
	}, bucketField("bucket the object is in"), keyField("object to delete"))
}

func runObjectRemove(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		bucket, key := req.String("bucket"), req.String("key")
		if req.DryRun {
			return view.Text{Body: "would remove " + bucket + "/" + key}, nil
		}
		if err := client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
			return nil, classify(err, req)
		}
		return view.Text{Body: "removed " + bucket + "/" + key}, nil
	})
}

// suggestStorageClasses and suggestContentTypes turn two Help strings into
// something a surface can use.
//
// Both are Suggest rather than Options, and that distinction is the whole
// point: S3-compatible servers are not one server. MinIO, R2 and Ceph accept
// classes AWS never defined, and a closed set would refuse a value the
// operator's own storage understands. This offers the answers that are almost
// always right and takes nothing away.
//
// Static, so no connection is opened and nothing is enumerated on a keystroke.
func suggestStorageClasses(context.Context, plugin.Request) []string {
	return []string{
		"STANDARD\tthe default — immediate access",
		"STANDARD_IA\tinfrequent access, cheaper to store and dearer to read",
		"ONEZONE_IA\tinfrequent access in one availability zone",
		"INTELLIGENT_TIERING\tmoved between tiers by access pattern",
		"GLACIER_IR\tarchive with immediate retrieval",
		"GLACIER\tarchive, retrieved in minutes to hours",
		"DEEP_ARCHIVE\tcheapest archive, retrieved in hours",
		"REDUCED_REDUNDANCY\tlegacy, kept for older buckets",
	}
}

func suggestContentTypes(context.Context, plugin.Request) []string {
	return []string{
		"application/json\t",
		"application/octet-stream\tunknown bytes — what a server assumes",
		"application/pdf\t",
		"application/zip\t",
		"image/jpeg\t",
		"image/png\t",
		"image/svg+xml\t",
		"text/csv; charset=utf-8\t",
		"text/html; charset=utf-8\t",
		"text/plain; charset=utf-8\twhat this capability assumes for text",
	}
}
