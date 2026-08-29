package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// docker.overview: what is running, what is not well, and where the disk went.
//
// The composed capability an operator actually puts on a dashboard. It leads
// with whether the daemon answers, because every other number here is
// meaningless if it does not.

func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	c, verr := connectionOf(req)
	if verr != nil {
		return nil, verr
	}
	rows, verr := fetchContainers(ctx, c, true)
	if verr != nil {
		// The daemon not answering is the headline rather than a failure:
		// this is the capability somebody runs *because* something is wrong.
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "daemon", Value: "did not answer — " + verr.Message},
			{Key: "what to check", Value: hintOf(verr)},
		}}, nil
	}

	running, sick := 0, []containerRow{}
	for _, r := range rows {
		if strings.EqualFold(r.State, "running") {
			running++
		}
		if unhealthy(r) {
			sick = append(sick, r)
		}
	}
	pairs := []view.Pair{
		{Key: "daemon", Value: "answering"},
		{Key: "containers", Value: fmt.Sprintf("%d running of %d", running, len(rows))},
	}
	if len(sick) > 0 {
		names := make([]string, 0, len(sick))
		for _, r := range sick {
			names = append(names, r.Names+" ("+strings.ToLower(r.State)+")")
		}
		pairs = append(pairs, view.Pair{
			Key:   "not running or not healthy",
			Value: truncate(names, 6)})
	}

	images, imgErr := fetchImages(ctx, c)
	var dangling int
	if imgErr == nil {
		var total int64
		for _, im := range images {
			total += parseSize(im.Size)
			if im.dangling() {
				dangling++
			}
		}
		v := fmt.Sprintf("%d, %s", len(images), format.Bytes(uint64(max64(total, 0))))
		if dangling > 0 {
			v += fmt.Sprintf(" — %d dangling", dangling)
		}
		pairs = append(pairs, view.Pair{Key: "images", Value: v})
	}

	if !req.Bool("detail") {
		return view.KeyValue{Pairs: pairs}, nil
	}
	sections := []view.Section{
		{ID: "daemon", Title: "Daemon", View: view.KeyValue{Pairs: pairs}},
	}
	if len(sick) > 0 {
		sections = append(sections, view.Section{
			ID: "containers", Title: "Containers not running or not healthy",
			View: containerTable(sick)})
	} else {
		sections = append(sections, view.Section{
			ID: "containers", Title: "Containers",
			View: view.Text{Body: fmt.Sprintf("all %s running and healthy",
				count(len(rows), "container is", "containers are"))}})
	}
	if imgErr == nil {
		top := images
		if len(top) > 10 {
			top = top[:10]
		}
		sections = append(sections, view.Section{
			ID: "images", Title: "Largest images", View: imageTable(top)})
	}
	return view.Sections{Items: sections}, nil
}

func hintOf(e *view.Error) string {
	if e == nil || e.Hint == "" {
		return "`docker info` asks the same question directly"
	}
	return e.Hint
}

func truncate(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(" and %d more", len(items)-max)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
