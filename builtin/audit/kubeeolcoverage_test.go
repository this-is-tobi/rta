package audit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// podsRunning is a cluster with one pod per image, each in the namespace "ns".
func podsRunning(t *testing.T, images ...string) string {
	t.Helper()
	pods := make([]obj, len(images))
	for i, image := range images {
		pods[i] = obj{
			"metadata": obj{"namespace": "ns", "name": fmt.Sprintf("p%d", i)},
			"spec":     obj{"containers": []obj{{"image": image}}},
		}
	}
	return jsonBody(t, obj{"items": pods})
}

func runEOL(t *testing.T, pods string, values map[string]any) view.View {
	t.Helper()
	fakeKubectl(t, map[string]string{"version": clusterVersionBody, "nodes": clusterNodesBody, "pods": pods})
	srv, _ := newEOLServer(t)
	req := eolRequest(nil).With(values).WithSurface(plugin.SurfaceCLI)
	out, err := runKubeEOLAt(t.Context(), req, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// notGradedByImage is the detail page's "not graded" section, keyed by the
// image each row names.
func notGradedByImage(t *testing.T, out view.View) map[string][]string {
	t.Helper()
	page, ok := out.(view.Sections)
	if !ok {
		t.Fatalf("a detail run returned %T, want view.Sections", out)
	}
	for _, s := range page.Items {
		if s.ID != "not-graded" {
			continue
		}
		table, ok := s.View.(view.Table)
		if !ok {
			t.Fatalf("the not-graded section is %T, want a table of its own", s.View)
		}
		var names []string
		for _, c := range table.Columns {
			names = append(names, c.Name)
		}
		if got := strings.Join(names, ","); got != "Image,Where,Reason,Next Step" {
			t.Fatalf("columns = %s", got)
		}
		rows := map[string][]string{}
		for _, row := range table.Rows {
			rows[row[0]] = row
		}
		return rows
	}
	t.Fatalf("no not-graded section on the page: %+v", page.Items)
	return nil
}

// An audit that graded four images in a hundred read "no issues in what was
// checked": the images it could not place were one Info row, which the overall
// line does not count.
func TestKubeEOLCountsUngradedImagesInTheOverallLine(t *testing.T) {
	out := runEOL(t, podsRunning(t, "postgres:15.4", "team/one:1", "team/two:1", "team/three:1"), nil)

	rendered := renderText(t, out)
	want := "overall | fail | 1 failing, 3 checks could not run"
	if !strings.Contains(rendered, want) {
		t.Errorf("missing %q in:\n%s", want, rendered)
	}
}

// The list of what could not be graded was the names joined into one cell and
// clipped to a line, so the names after the first few could never be read on a
// terminal. It is a table now, and every image is in it.
func TestKubeEOLListsEveryUngradedImageWithItsReasonUnclipped(t *testing.T) {
	images := make([]string, 60)
	for i := range images {
		images[i] = fmt.Sprintf("registry.example/team/service-number-%02d:1.%d.0", i, i)
	}
	out := runEOL(t, podsRunning(t, images...), map[string]any{"detail": true})

	rows := notGradedByImage(t, out)
	if len(rows) != 60 {
		t.Fatalf("%d images in the not-graded section, want 60", len(rows))
	}
	for i := range images {
		image := fmt.Sprintf("service-number-%02d:1.%d.0", i, i)
		row, ok := rows[image]
		if !ok {
			t.Errorf("%s is missing from the not-graded section", image)
			continue
		}
		for _, cell := range row {
			if strings.HasSuffix(cell, "…") {
				t.Errorf("%s: a cell was clipped: %q", image, cell)
			}
		}
		if row[1] != "1 pod in 1 namespace" || row[2] != "no-product" {
			t.Errorf("%s: row = %q", image, row)
		}
	}
}

// Every image that is not graded says why, in a code a script can select on
// and a sentence that says what to do about it. The code names the first thing
// in the way: an image the catalogue does not list gets no advice to pin its
// tag, since nothing would be graded if it were.
func TestKubeEOLReasonCodes(t *testing.T) {
	out := runEOL(t, podsRunning(t,
		"mystery:latest",
		"mystery-two@sha256:0123",
		"nginx",
		"redis@sha256:0123",
		"mysql:8",
		"redis:9.1",
		"ghost:5.9",
		"redis-exporter:1.2",
	), map[string]any{"detail": true})

	rows := notGradedByImage(t, out)
	for image, tc := range map[string]struct{ reason, next string }{
		"mystery:latest":     {"no-product", "endoflife.date does not list it"},
		"mystery-two@digest": {"no-product", "endoflife.date does not list it"},
		"nginx":              {"floating", "pin a version"},
		"redis@digest":       {"digest-only", "a digest names bytes, not a release"},
		"mysql:8":            {"no-cycle", "names a major with several cycles under it (8.4, 8.0): pin one"},
		"redis:9.1":          {"no-cycle", "newer than the newest cycle endoflife.date lists (7.2)"},
		"ghost:5.9":          {"no-cycle", "names no release cycle of ghost"},
		"redis-exporter:1.2": {"suggested", "named like redis"},
	} {
		row, ok := rows[image]
		if !ok {
			t.Errorf("%s is missing from the not-graded section; have %v", image, imageNames(rows))
			continue
		}
		if row[2] != tc.reason {
			t.Errorf("%s: reason = %q, want %q", image, row[2], tc.reason)
		}
		if !strings.Contains(row[3], tc.next) {
			t.Errorf("%s: next step = %q, want it to say %q", image, row[3], tc.next)
		}
	}
	if len(rows) != 8 {
		t.Errorf("%d rows in the section, want 8: %v", len(rows), imageNames(rows))
	}
}

func imageNames(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestKubeEOLNotGradedSaysWhereAnImageRuns(t *testing.T) {
	pods := jsonBody(t, obj{"items": []obj{
		{"metadata": obj{"namespace": "a", "name": "p1"}, "spec": obj{"containers": []obj{{"image": "mystery:1"}}}},
		{"metadata": obj{"namespace": "a", "name": "p2"}, "spec": obj{"containers": []obj{{"image": "mystery:1"}}}},
		{"metadata": obj{"namespace": "b", "name": "p3"}, "spec": obj{"containers": []obj{{"image": "mystery:1"}}}},
	}})
	rows := notGradedByImage(t, runEOL(t, pods, map[string]any{"detail": true}))
	if got := rows["mystery:1"][1]; got != "3 pods in 2 namespaces" {
		t.Errorf("where = %q", got)
	}
	// Narrowed, the namespace is by construction one and the count is noise.
	rows = notGradedByImage(t, runEOL(t, pods, map[string]any{"detail": true, "namespace": "a"}))
	if got := rows["mystery:1"][1]; got != "3 pods" {
		t.Errorf("where, narrowed = %q", got)
	}
}
