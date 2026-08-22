package main

import (
	"context"
	"sort"
	"strconv"

	"github.com/minio/minio-go/v7"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// s3.overview composes a bucket count through the one client its own Run
// already built — the same reason pg.overview and vault.overview call their
// sections directly rather than through plugin.Page.AddAs, which would open
// one connection per section for what is supposed to be a single glance.
//
// NoPreview like every capability here (cap's job): the automatic dashboard
// must not decide on its own that an S3 endpoint is worth polling every few
// seconds. dashboard.tiles still accepts it explicitly — naming a
// capability in a config file is the asking.
func overviewCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "s3.overview",
		Summary:    "Endpoint and bucket count at a glance",
		Safety:     plugin.Read,
		Idempotent: true,
		Detailed:   true,
		Description: "Whether this endpoint is reachable at all, and how many buckets the " +
			"configured credentials can see. --detail adds the bucket list, with region and age, " +
			"to the same page.",
		Run: runOverview,
	})
}

func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(ctx context.Context, client *minio.Client) (view.View, error) {
		buckets, err := client.ListBuckets(ctx)
		if err != nil {
			return nil, classify(err, req)
		}
		if req.Bool("detail") {
			return detailedOverview(req, buckets), nil
		}
		return compactOverview(req, buckets), nil
	})
}

func compactOverview(req plugin.Request, buckets []minio.BucketInfo) view.View {
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "endpoint", Value: req.String("endpoint")},
		{Key: "buckets", Value: strconv.Itoa(len(buckets))},
	}}
}

func detailedOverview(req plugin.Request, buckets []minio.BucketInfo) view.View {
	p := plugin.NewPage(context.Background(), req)
	p.Put("status", compactOverview(req, buckets))

	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Name < buckets[j].Name })
	t := view.Table{Columns: []view.Column{{Name: "Name"}, {Name: "Region"}, {Name: "Created"}}}
	for _, b := range buckets {
		t.Rows = append(t.Rows, []string{b.Name, b.BucketRegion, b.CreationDate.Format("2006-01-02")})
	}
	t.Total = len(t.Rows)
	p.Put("buckets", t)

	return p.View()
}
