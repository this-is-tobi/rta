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
		// Local, because a grant on this capability is checked against `key`
		// alone — internal/grant's scopes() reads exactly one field — and a
		// destination bucket is a place the operator never named. Without
		// this, "copy reports/q1.csv" authorized writing those bytes into any
		// bucket the credentials reach, which is Field.Local's own stated
		// case: a destination is a destination whether or not it is on this
		// machine, and a caller may not choose one for a record a grant only
		// authorized copying.
		//
		// It costs nothing to take away: destination() below already defaults
		// it to the source bucket, so copying and renaming within one bucket
		// — the common case, and the only one an agent has ever needed —
		// reads exactly the same. A person at a terminal still has it.
		{Name: "dest-bucket", Type: plugin.String, Local: true,
			Help: "destination bucket; same as --bucket if omitted",
			Live: true, Suggest: suggestBuckets},
		{Name: "dest-key", Type: plugin.String, Required: true, Help: "destination object name"},
	}
}

// refuseIfTaken stops a copy or a move from writing over an object that is
// already there.
//
// Local on dest-bucket closes the cross-bucket write; this closes the rest of
// it. A grant scoped to `key` still says nothing about the key being written,
// so within one bucket "copy reports/q1.csv" would otherwise authorize
// destroying app/settings.json — the same hole, one container in.
//
// kv.rename settled this for the identical shape and its sentence transfers
// unchanged: "a grant scoped to the key being renamed says nothing at all
// about the one being clobbered". Refusing turns both capabilities into
// something the `key` scope can honestly bound — the worst they do is add an
// object, never destroy one.
//
// Stat-then-copy has a window, and it is the narrower risk: losing the race
// means overwriting an object created in the last few milliseconds, where
// leaving this out means overwriting anything at all. minio-go's CopyObject
// has no conditional-destination option to close it properly.
func refuseIfTaken(ctx context.Context, client *minio.Client, verb, bucket, key string, req plugin.Request) *view.Error {
	if _, err := client.StatObject(ctx, bucket, key, minio.StatObjectOptions{}); err == nil {
		return view.Errorf("s3."+verb+".taken", "%s/%s already exists", bucket, key).
			WithHint("writing over it would destroy what it holds — remove it first: " +
				"rta s3 object rm " + key + " --bucket " + bucket)
	} else if minio.ToErrorResponse(err).StatusCode != 404 {
		// Anything other than "not there" is not permission to proceed: a 403
		// on the destination means the credentials cannot see it, not that it
		// is absent.
		return classify(err, req)
	}
	return nil
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
			"Refuses if --dest-key already exists rather than writing over it: a grant names " +
			"the source key and says nothing about the object it would replace.",
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
		// After the dry-run branch on purpose: a dry run must send nothing at
		// all, so it predicts the copy without predicting this refusal.
		if verr := refuseIfTaken(ctx, client, "copy", dstBucket, dstKey, req); verr != nil {
			return nil, verr
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
		if verr := refuseIfTaken(ctx, client, "rename", dstBucket, dstKey, req); verr != nil {
			return nil, verr
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
