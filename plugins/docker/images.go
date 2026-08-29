package main

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// imageRow is one line of `docker image ls --format json`.
//
// Size arrives as a human string ("1.6GB"), not a number — the CLI formats
// before it prints. parseSize turns it back into bytes so the overview can
// add them up and so the largest-first sort means what it says.
type imageRow struct {
	ID           string `json:"ID"`
	Repository   string `json:"Repository"`
	Tag          string `json:"Tag"`
	Size         string `json:"Size"`
	CreatedSince string `json:"CreatedSince"`
	Containers   string `json:"Containers"`
}

// dangling reports an untagged leftover of a rebuild — the usual answer to
// "where did my disk go".
func (r imageRow) dangling() bool {
	return r.Repository == "<none>" || r.Tag == "<none>"
}

func (r imageRow) name() string {
	if r.dangling() {
		return "<dangling>"
	}
	return r.Repository + ":" + r.Tag
}

// parseSize reads the CLI's formatted size back into bytes.
//
// Docker writes decimal units ("1.6GB" is 1.6 × 10^9, not 2^30), which is
// what its own `system df` totals agree with, so this uses the same base.
// An unparseable value yields 0 rather than an error: a size that could not
// be read must not fail a listing that is otherwise correct.
func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	units := []struct {
		suffix string
		mul    float64
	}{
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1},
	}
	for _, u := range units {
		if !strings.HasSuffix(s, u.suffix) {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), 64)
		if err != nil {
			return 0
		}
		return int64(n * u.mul)
	}
	return 0
}

func fetchImages(ctx context.Context, c connection) ([]imageRow, *view.Error) {
	raw, verr := run(ctx, c, "image", "ls", "--format", "json", "--no-trunc")
	if verr != nil {
		return nil, verr
	}
	rows, verr := jsonLines[imageRow](raw)
	if verr != nil {
		return nil, verr
	}
	sort.Slice(rows, func(i, j int) bool {
		return parseSize(rows[i].Size) > parseSize(rows[j].Size)
	})
	return rows, nil
}

func imageTable(rows []imageRow) view.Table {
	out := make([][]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, []string{r.name(), short(r.ID), r.Size, r.CreatedSince})
	}
	return view.Table{
		Columns: []view.Column{
			{Name: "image"}, {Name: "id"}, {Name: "size", Kind: view.KindBytes}, {Name: "created"},
		},
		Rows: out, Total: len(out),
	}
}

func runImageList(ctx context.Context, req plugin.Request) (view.View, error) {
	c, verr := connectionOf(req)
	if verr != nil {
		return nil, verr
	}
	rows, verr := fetchImages(ctx, c)
	if verr != nil {
		return nil, verr
	}
	return imageTable(rows), nil
}
