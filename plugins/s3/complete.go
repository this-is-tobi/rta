package main

import (
	"context"
	"sort"

	"github.com/minio/minio-go/v7"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Live completion: the inputs whose values exist server-side, completed on
// the deliberate channel (Field.Live). Each one is a
// listing — a read, made with the credentials the run would use, visible in
// the provider's own audit trail as the same principal. Silent on every
// failure, like any Suggest: a completion that cannot answer slows nobody
// down, and the run that follows classifies the same failure properly.

// completionCap bounds one listing's answer. A suggestion list is read by a
// person; past a screenful the answer is "type more", and an unbounded walk
// of a million-object bucket is a bill, not a completion.
const completionCap = 60

// suggestBuckets lists what the configured credentials can see — the same
// call s3.bucket.list makes, minus everything but the names.
func suggestBuckets(ctx context.Context, req plugin.Request) []string {
	client, verr := connect(req)
	if verr != nil {
		return nil
	}
	buckets, err := client.ListBuckets(ctx)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(buckets))
	for _, b := range buckets {
		names = append(names, b.Name)
	}
	sort.Strings(names)
	return names
}

// suggestKeys walks a bucket one "/" segment at a time: the box's own text
// is the prefix (the host puts it under the field's name — Field.Live), the
// delimiter groups what lies below it, and a common prefix comes back with
// its trailing "/" — which is what lets the next press fetch deeper instead
// of re-accepting (needsFetch's extends-or-fetch rule, the same convention
// the kube coordinate's segments use).
func suggestKeys(field string) func(context.Context, plugin.Request) []string {
	return func(ctx context.Context, req plugin.Request) []string {
		bucket := req.String("bucket")
		if bucket == "" {
			return nil // nothing to walk until the sibling names it
		}
		client, verr := connect(req)
		if verr != nil {
			return nil
		}
		// Cancelled on return, so an answer capped mid-listing also stops
		// the goroutine feeding the channel.
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		var out []string
		for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{
			Prefix: req.String(field),
		}) {
			if obj.Err != nil {
				return nil
			}
			out = append(out, obj.Key)
			if len(out) == completionCap {
				break
			}
		}
		return out
	}
}
