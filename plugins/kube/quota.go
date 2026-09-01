package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// kube.quota.list: how much of a namespace's ResourceQuota is actually used.
//
// One row per resource tracked by a quota, not one row per quota object —
// "cpu: 3.5/4 (88%)" is the sentence an SRE reads before a namespace runs out
// of headroom and starts refusing pods; "quota compute-resources exists" is
// not. LimitRange objects are noted by count rather than fully modeled: their
// shape (per-object-type default/min/max/defaultRequest) does not table-ize
// alongside a used/hard resource map, and "how many are defined" is enough to
// point someone at `kubectl describe limitrange` for the rest.

type resourceQuotaItem struct {
	Metadata meta `json:"metadata"`
	Status   struct {
		Hard map[string]string `json:"hard"`
		Used map[string]string `json:"used"`
	} `json:"status"`
}

type limitRangeItem struct {
	Metadata meta `json:"metadata"`
}

// fetchQuotas is shared with kube.overview's own composition, so both read
// the same object the same way.
func fetchQuotas(ctx context.Context, s selection) (list[resourceQuotaItem], *view.Error) {
	var quotas list[resourceQuotaItem]
	verr := getJSON(ctx, s, "resourcequotas", &quotas)
	return quotas, verr
}

func runQuotaList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}

	quotas, qerr := fetchQuotas(ctx, s)
	if qerr != nil {
		return nil, qerr
	}
	var limits list[limitRangeItem]
	limitErr := getJSON(ctx, s, "limitranges", &limits)

	cols := []view.Column{}
	if s.AllNS {
		cols = append(cols, view.Column{Name: "namespace"})
	}
	cols = append(cols,
		view.Column{Name: "quota"},
		view.Column{Name: "resource"},
		view.Column{Name: "used"},
		view.Column{Name: "hard"},
		view.Column{Name: "%", Kind: view.KindPercent},
	)

	sort.Slice(quotas.Items, func(i, j int) bool {
		a, b := quotas.Items[i].Metadata, quotas.Items[j].Metadata
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})

	rows := make([][]string, 0, len(quotas.Items))
	for _, q := range quotas.Items {
		resources := make([]string, 0, len(q.Status.Hard))
		for r := range q.Status.Hard {
			resources = append(resources, r)
		}
		sort.Strings(resources)
		for _, r := range resources {
			hard := q.Status.Hard[r]
			used := q.Status.Used[r]
			row := []string{}
			if s.AllNS {
				row = append(row, q.Metadata.Namespace)
			}
			rows = append(rows, append(row, q.Metadata.Name, r, used, hard, quotaPercent(used, hard)))
		}
	}

	t := view.Table{Columns: cols, Rows: rows, Total: len(rows)}
	return quotaView(t, limits, limitErr), nil
}

// quotaView folds the LimitRange count in as a trailing note rather than a
// second table — one sentence, not a page, for a signal that is "present or
// not" far more often than it is something to read row by row.
func quotaView(t view.Table, limits list[limitRangeItem], limitErr *view.Error) view.View {
	if limitErr != nil || len(limits.Items) == 0 {
		return t
	}
	return view.Sections{Items: []view.Section{
		{ID: "quotas", Title: "Resource quotas", View: t},
		{ID: "limits", Title: "Limit ranges", View: view.Text{
			Body: fmt.Sprintf("%d %s defined — `kubectl describe limitrange` for the "+
				"per-object-type defaults and bounds",
				len(limits.Items), plural(len(limits.Items), "limit range", "limit ranges")),
		}},
	}}
}

// quotaPercentValue parses both sides as Kubernetes quantities and reports
// used against hard as a fraction. Either side failing to parse (an
// unrecognised resource, a quantity shape this plugin's minimal parser does
// not cover) reports ok=false rather than a wrong number.
func quotaPercentValue(used, hard string) (float64, bool) {
	// cpu is the one resource whose quantities are typically sub-1 and
	// suffixed "m" rather than byte-suffixed; everything else here (memory,
	// storage, ephemeral-storage) and unitless counts (pods, count/things)
	// parse the same way as bytes because a bare integer is valid input to
	// both parsers.
	if u, uok := parseCPU(used); uok {
		if h, hok := parseCPU(hard); hok && h > 0 {
			return u / h, true
		}
	}
	u, uok := parseBytes(used)
	h, hok := parseBytes(hard)
	if !uok || !hok || h == 0 {
		return 0, false
	}
	return float64(u) / float64(h), true
}

// quotaPercent is quotaPercentValue rendered for a table cell.
func quotaPercent(used, hard string) string {
	v, ok := quotaPercentValue(used, hard)
	if !ok {
		return ""
	}
	return percentOf(v, 1)
}

// quotaPressure returns "namespace/quota resource: NN%" for every tracked
// resource at or above threshold — the line kube.overview folds in, so a
// namespace running out of headroom shows up before it starts refusing pods
// rather than after.
func quotaPressure(quotas list[resourceQuotaItem], threshold float64) []string {
	var out []string
	for _, q := range quotas.Items {
		for r, hard := range q.Status.Hard {
			used := q.Status.Used[r]
			v, ok := quotaPercentValue(used, hard)
			if !ok || v < threshold {
				continue
			}
			out = append(out, fmt.Sprintf("%s/%s %s: %s",
				q.Metadata.Namespace, q.Metadata.Name, r, percentOf(v, 1)))
		}
	}
	sort.Strings(out)
	return out
}
