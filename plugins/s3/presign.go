package main

import (
	"context"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// s3.object.presign is Write+NeedsGrant regardless of --method: a presigned
// URL is a bearer credential handed to whoever holds the link, not only to
// the caller — a "get" presign gives away read access to anyone who gets
// the URL for its whole ttl, which is at least as sensitive as
// s3.object.get revealing the content to the caller directly, and a "put"
// presign gives away write access the same way s3.object.set does. Both
// need the same grant s3.object.get/set already require, on the same key.
func s3ObjectPresignCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID: "s3.object.presign", Summary: "Generate a time-limited URL for an object", Safety: plugin.Write,
		NeedsGrant: true, Scope: "key",
		Description: "The URL itself is a credential: anyone who has it can act on the object " +
			"until --ttl expires, with no further authentication and no further grant check — " +
			"this call is the one gated moment, not each use of the link. --method put grants " +
			"write access to a caller-chosen key instead of read access to an existing one.",
		Run: runObjectPresign,
	}, bucketField("bucket the object is in"), keyField("object to presign"),
		plugin.Field{Name: "method", Type: plugin.String, Default: "get", Options: []string{"get", "put"},
			Help: "get for a download link, put for an upload link"},
		plugin.Field{Name: "ttl", Type: plugin.Int, Default: 900, Min: 1, Max: 604800,
			Help: "seconds the URL stays valid — S3's own cap is 7 days (604800)"})
}

func runObjectPresign(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		bucket, key := req.String("bucket"), req.String("key")
		ttl := time.Duration(req.Int("ttl")) * time.Second

		var u *url.URL
		var err error
		switch req.String("method") {
		case "put":
			u, err = client.PresignedPutObject(ctx, bucket, key, ttl)
		default:
			u, err = client.PresignedGetObject(ctx, bucket, key, ttl, nil)
		}
		if err != nil {
			return nil, classify(err, req)
		}
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "url", Value: u.String()},
			{Key: "method", Value: req.String("method")},
			{Key: "expires in", Value: ttl.String()},
		}}, nil
	})
}
