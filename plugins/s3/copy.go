package main

import (
	"context"

	"github.com/minio/minio-go/v7"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func copyFields(bucketHelp, keyHelp string) []plugin.Field {
	return []plugin.Field{
		bucketField(bucketHelp),
		keyField(keyHelp),
		{Name: "dest-bucket", Type: plugin.String, Help: "destination bucket; same as --bucket if omitted"},
		{Name: "dest-key", Type: plugin.String, Required: true, Help: "destination object name"},
	}
}

// destination resolves dest-bucket/dest-key, defaulting the bucket to the
// source's own — the common case (renaming or copying within one bucket)
// should not require repeating it.
func destination(req plugin.Request) (bucket, key string) {
	bucket = req.String("dest-bucket")
	if bucket == "" {
		bucket = req.String("bucket")
	}
	return bucket, req.String("dest-key")
}

// s3.object.copy duplicates an object server-side — CopyObject never reads
// the bytes through this process — mirroring kv.copy's own shape: a new
// name, the original untouched.
func s3ObjectCopyCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID: "s3.object.copy", Summary: "Copy an object to a new bucket/key", Safety: plugin.Write,
		NeedsGrant: true, Scope: "key",
		Description: "Copies server-side; the content never passes through this process. " +
			"Overwrites --dest-key if it already exists, the same overwrite risk s3.object.set " +
			"carries for the same reason.",
		Run: runObjectCopy,
	}, copyFields("source bucket", "object to copy")...)
}

func runObjectCopy(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		srcBucket, srcKey := req.String("bucket"), req.String("key")
		dstBucket, dstKey := destination(req)
		if req.DryRun {
			return view.Text{Body: "would copy " + srcBucket + "/" + srcKey + " to " + dstBucket + "/" + dstKey}, nil
		}
		_, err := client.CopyObject(ctx,
			minio.CopyDestOptions{Bucket: dstBucket, Object: dstKey},
			minio.CopySrcOptions{Bucket: srcBucket, Object: srcKey})
		if err != nil {
			return nil, classify(err, req)
		}
		return view.Text{Body: "copied " + srcBucket + "/" + srcKey + " to " + dstBucket + "/" + dstKey}, nil
	})
}

// s3.object.rename is copy followed by removing the source — S3 has no
// native rename, every tool that offers one composes these same two calls.
// Doing it as one capability means one grant-scoped call rather than two,
// and a partial failure (the copy lands, the delete does not) is reported
// rather than left for the caller to notice a duplicate exists.
func s3ObjectRenameCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID: "s3.object.rename", Summary: "Move an object to a new bucket/key", Safety: plugin.Write,
		NeedsGrant: true, Scope: "key",
		Description: "S3 has no native rename — this copies server-side, then removes the " +
			"source. If the copy succeeds and the remove fails, the object exists in both " +
			"places and the failure says so rather than reporting success.",
		Run: runObjectRename,
	}, copyFields("source bucket", "object to move")...)
}

func runObjectRename(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		srcBucket, srcKey := req.String("bucket"), req.String("key")
		dstBucket, dstKey := destination(req)
		if req.DryRun {
			return view.Text{Body: "would move " + srcBucket + "/" + srcKey + " to " + dstBucket + "/" + dstKey}, nil
		}
		_, err := client.CopyObject(ctx,
			minio.CopyDestOptions{Bucket: dstBucket, Object: dstKey},
			minio.CopySrcOptions{Bucket: srcBucket, Object: srcKey})
		if err != nil {
			return nil, classify(err, req)
		}
		// Not classified: the code that matters here is not "why did the
		// remove fail" but "the object now exists in two places" — a state
		// only this capability can produce, and the one the caller has to
		// act on. The underlying reason travels in the message.
		if err := client.RemoveObject(ctx, srcBucket, srcKey, minio.RemoveObjectOptions{}); err != nil {
			return nil, view.Errorf("s3.rename.partial",
				"copied to %s/%s but could not remove the source %s/%s: %v",
				dstBucket, dstKey, srcBucket, srcKey, err)
		}
		return view.Text{Body: "moved " + srcBucket + "/" + srcKey + " to " + dstBucket + "/" + dstKey}, nil
	})
}
