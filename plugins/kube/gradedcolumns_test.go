package main

import (
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Which percentages are graded, and which are deliberately not.
//
// view.KindUsage is what makes a renderer paint a figure green, amber or red,
// and the whole reason it is a separate kind from KindPercent is that most
// percentages have no bad end. This plugin holds both sorts, side by side in
// the same package, which makes it the likeliest place for one to be given the
// other's kind by pattern-matching on the "%" in a column name.
//
// So both lists are written down. A column moving between them should be a
// change somebody made on purpose, with an argument, not a diff nobody read.
func TestOnlyTheColumnsWithACapacityBehindThemAreGraded(t *testing.T) {
	graded := map[string]string{
		// used against the claim's own capacity — the disk-full question
		"kube.pvc.usage/used %": "used %",
		// used against the container's limit, which is where it is throttled
		// or killed
		"kube.metrics.pod/cpu %":    "cpu %",
		"kube.metrics.pod/memory %": "memory %",
		// used against the node's allocatable, which is what the scheduler
		// has left to give
		"kube.metrics.node/cpu %":    "cpu %",
		"kube.metrics.node/memory %": "memory %",
		// used against the quota's hard limit
		"kube.quota.list/%": "%",
	}
	// PSI: the share of an interval tasks spent stalled, not a share of a
	// capacity. A node at 40% memory pressure is in serious trouble and one at
	// 8% sustained io pressure has a disk that cannot keep up — both of which
	// a capacity's 80/90 bands paint green. There is no single threshold for
	// the three either, so the number is shown and the reading is left to
	// somebody who knows which one they are looking at.
	ungraded := []string{"cpu 10s", "cpu 5m", "memory 10s", "memory 5m",
		"io 10s", "io 5m"}

	byName := map[string]view.ColumnKind{}
	for id, cols := range map[string][]view.Column{
		"kube.pvc.usage":        pvcUsageColumns(),
		"kube.metrics.pod":      podMetricColumns(false),
		"kube.metrics.node":     nodeMetricColumns(),
		"kube.quota.list":       quotaColumns(false),
		"kube.metrics.pressure": pressureColumns(),
	} {
		for _, c := range cols {
			byName[id+"/"+c.Name] = c.Kind
		}
	}

	for key := range graded {
		if got := byName[key]; got != view.KindUsage {
			t.Errorf("%s is %q, want usage — nothing else colours it", key, got)
		}
	}
	for _, name := range ungraded {
		key := "kube.metrics.pressure/" + name
		if got := byName[key]; got != view.KindPercent {
			t.Errorf("%s is %q — PSI graded against a capacity's bands paints a "+
				"stalling node green", key, got)
		}
	}
}
