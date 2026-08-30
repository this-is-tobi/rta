package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Everything in this file is the read tier: it describes collections and
// returns not one point. See the package comment for why that line is drawn
// where it is — the short version is that a vector is not a hash.

type collectionsResponse struct {
	Collections []struct {
		Name string `json:"name"`
	} `json:"collections"`
}

type collectionInfo struct {
	Status              string `json:"status"`
	OptimizerStatus     any    `json:"optimizer_status"`
	PointsCount         *int64 `json:"points_count"`
	IndexedVectorsCount *int64 `json:"indexed_vectors_count"`
	SegmentsCount       int64  `json:"segments_count"`
	Config              struct {
		Params struct {
			// Qdrant answers this two ways: a single unnamed vector config, or
			// a map of named ones. Decoded as `any` and sorted out below,
			// because a struct that only fits one shape silently reports
			// nothing for the other.
			Vectors           any   `json:"vectors"`
			ShardNumber       int64 `json:"shard_number"`
			ReplicationFactor int64 `json:"replication_factor"`
			OnDiskPayload     bool  `json:"on_disk_payload"`
		} `json:"params"`
	} `json:"config"`
}

func collectionField(help string) plugin.Field {
	return plugin.Field{Name: "collection", Type: plugin.String, Positional: true, Required: true,
		Help: help, Live: true, Suggest: suggestCollections}
}

func suggestCollections(ctx context.Context, req plugin.Request) []string {
	var out collectionsResponse
	if verr := get(ctx, req, "/collections", &out); verr != nil {
		// No suggestions rather than an error: there is nowhere to show one at
		// a completion prompt, and a half-typed flag is not where somebody
		// should learn the instance is unreachable.
		return nil
	}
	names := make([]string, 0, len(out.Collections))
	for _, c := range out.Collections {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return names
}

func overviewCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "qdrant.overview",
		Summary:    "What this instance is and what it holds",
		Safety:     plugin.Read,
		Idempotent: true,
		Detailed:   true,
		Description: "Version, reachability and every collection with its point count and status.\n\n" +
			"The status column is the one to read. A collection in `yellow` is serving searches " +
			"from a partly-built index, so its results are quietly incomplete rather than absent " +
			"— which looks like a working search returning slightly wrong answers.\n\n" +
			"Describes collections and returns no point. Reading points is qdrant.points.scroll, " +
			"and it is a write.",
		Run: runOverview,
	})
}

func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	var root struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	}
	// The root endpoint is unwrapped — it is the one response with no
	// "result" envelope — so it is decoded straight rather than through get.
	if verr := getRaw(ctx, req, "/", &root); verr != nil {
		return nil, verr
	}

	table, verr := collectionTable(ctx, req)
	if verr != nil {
		return nil, verr
	}

	p := plugin.NewPage(ctx, req)
	p.Put("status", view.KeyValue{Pairs: []view.Pair{
		{Key: "endpoint", Value: req.String("endpoint")},
		{Key: "title", Value: root.Title},
		{Key: "version", Value: root.Version},
		{Key: "collections", Value: strconv.Itoa(len(table.Rows))},
	}})
	p.Put("collections", table)
	return p.View(), nil
}

func collectionListCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "qdrant.collection.list",
		Summary:    "Every collection, with its size and index status",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Names, point counts, how many of those vectors are actually indexed, and " +
			"each collection's status.\n\n" +
			"Indexed against total is the number worth watching: a collection still building its " +
			"index answers searches from what it has, so the gap between those two columns is " +
			"how incomplete the answers currently are.\n\n" +
			"Names and counts only, never a point.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			return collectionTable(ctx, req)
		},
	})
}

func collectionTable(ctx context.Context, req plugin.Request) (view.Table, *view.Error) {
	var list collectionsResponse
	if verr := get(ctx, req, "/collections", &list); verr != nil {
		return view.Table{}, verr
	}
	names := make([]string, 0, len(list.Collections))
	for _, c := range list.Collections {
		names = append(names, c.Name)
	}
	// Qdrant does not promise an order, and a listing that reshuffles between
	// calls cannot be diffed.
	sort.Strings(names)

	t := view.Table{Columns: []view.Column{
		{Name: "Collection"},
		{Name: "Points", Kind: view.KindNumber},
		{Name: "Indexed", Kind: view.KindNumber},
		{Name: "Segments", Kind: view.KindNumber},
		{Name: "Status", Kind: view.KindStatus},
	}}
	for _, name := range names {
		var info collectionInfo
		if verr := get(ctx, req, pathFor("/collections/%s", name), &info); verr != nil {
			// Reported, not fatal. A collection this key may not see is the
			// normal case on a shared instance, and ending the listing at the
			// first one would turn "here is what you can see" into nothing.
			t.Rows = append(t.Rows, []string{name, "-", "-", "-", "unreadable"})
			continue
		}
		t.Rows = append(t.Rows, []string{
			name,
			countText(info.PointsCount),
			countText(info.IndexedVectorsCount),
			strconv.FormatInt(info.SegmentsCount, 10),
			info.Status,
		})
	}
	t.Total = len(t.Rows)
	return t, nil
}

// countText distinguishes "none" from "not reported". Qdrant omits these
// fields entirely on a collection it has not finished loading, and rendering
// that as 0 would say the collection is empty when it is merely not ready.
func countText(n *int64) string {
	if n == nil {
		return "not reported"
	}
	return strconv.FormatInt(*n, 10)
}

func collectionShowCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "qdrant.collection.show",
		Summary:    "How one collection is configured, and whether its index is built",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Vector dimensions and distance metric, sharding and replication, payload " +
			"storage, and index progress.\n\n" +
			"The dimension and the distance metric are the two that make a collection " +
			"incompatible with a model: embedding with something that produces a different " +
			"dimension fails loudly, and embedding with a model trained for a different metric " +
			"fails silently, returning plausible and wrong neighbours.\n\n" +
			"Configuration only, never a point — this describes the shape of the data and " +
			"returns none of it.",
		Run: runCollectionShow,
	}, collectionField("collection to describe"))
}

func runCollectionShow(ctx context.Context, req plugin.Request) (view.View, error) {
	name := req.String("collection")
	var info collectionInfo
	if verr := get(ctx, req, pathFor("/collections/%s", name), &info); verr != nil {
		return nil, verr
	}

	pairs := []view.Pair{
		{Key: "collection", Value: name},
		{Key: "status", Value: info.Status},
		{Key: "points", Value: countText(info.PointsCount)},
		{Key: "indexed vectors", Value: countText(info.IndexedVectorsCount)},
		{Key: "segments", Value: strconv.FormatInt(info.SegmentsCount, 10)},
		{Key: "shards", Value: strconv.FormatInt(info.Config.Params.ShardNumber, 10)},
		{Key: "replication factor", Value: strconv.FormatInt(info.Config.Params.ReplicationFactor, 10)},
		{Key: "payload on disk", Value: strconv.FormatBool(info.Config.Params.OnDiskPayload)},
	}
	for _, v := range describeVectors(info.Config.Params.Vectors) {
		pairs = append(pairs, v)
	}
	if info.Status == "yellow" {
		pairs = append(pairs, view.Pair{
			Key: "note",
			Value: "yellow means the index is still building — searches answer from what is " +
				"indexed so far, so results are incomplete rather than delayed",
		})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// describeVectors flattens Qdrant's two shapes into rows.
//
// A collection with one unnamed vector reports {"size": n, "distance": "..."}.
// A collection with named vectors reports {"name": {"size": ...}, ...}. Both
// are ordinary and a struct fitting one reports nothing for the other, which
// is why this walks the decoded map instead.
func describeVectors(v any) []view.Pair {
	switch shape := v.(type) {
	case map[string]any:
		if _, single := shape["size"]; single {
			return []view.Pair{{Key: "vectors", Value: vectorText("", shape)}}
		}
		names := make([]string, 0, len(shape))
		for name := range shape {
			names = append(names, name)
		}
		sort.Strings(names)
		out := make([]view.Pair, 0, len(names))
		for _, name := range names {
			cfg, _ := shape[name].(map[string]any)
			out = append(out, view.Pair{Key: "vector " + name, Value: vectorText(name, cfg)})
		}
		return out
	default:
		return []view.Pair{{Key: "vectors", Value: "not reported"}}
	}
}

func vectorText(_ string, cfg map[string]any) string {
	if cfg == nil {
		return "not reported"
	}
	size, distance := "?", "?"
	if n, ok := cfg["size"].(float64); ok {
		size = strconv.FormatInt(int64(n), 10)
	}
	if d, ok := cfg["distance"].(string); ok {
		distance = d
	}
	text := fmt.Sprintf("%s dimensions, %s distance", size, distance)
	if onDisk, ok := cfg["on_disk"].(bool); ok && onDisk {
		text += ", on disk"
	}
	return text
}
