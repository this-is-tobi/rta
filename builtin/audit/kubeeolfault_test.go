package audit

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func runEOLAgainst(t *testing.T, srvURL, pods string, values map[string]any) (view.View, error) {
	t.Helper()
	fakeKubectl(t, map[string]string{"version": clusterVersionBody, "nodes": clusterNodesBody, "pods": pods})
	req := eolRequest(nil).With(values).WithSurface(plugin.SurfaceCLI)
	return runKubeEOLAt(t.Context(), req, srvURL)
}

// endoflife.date documents 429 and 502/503 from its host. One product's page
// failing used to return an error for the whole audit after the cluster had
// been read, so a transient 503 on one of seventeen products lost the sixteen
// good rows and the control plane's verdict with them.
func TestKubeEOLOneProductFailingKeepsTheRestOfTheReport(t *testing.T) {
	redisHits := 0
	srv, _ := newFaultyEOLServer(t, map[string]eolFault{
		"/products/redis": func(n int) (int, string) { redisHits = n; return http.StatusServiceUnavailable, "" },
	})
	pods := podsRunning(t, "postgres:15.4", "redis:7.2.4", "redis:6.2.1-alpine")

	out, err := runEOLAgainst(t, srv.URL, pods, nil)
	if err != nil {
		t.Fatalf("one product failing sank the audit: %v", err)
	}
	rendered := renderText(t, out)
	for _, want := range []string{
		"control plane: v1.30.2+k3s1 | ok",
		"image postgres:15.4 (1 pod in 1 namespace) | ok",
		"images not graded (2 of 3) | info | 2 unanswered by endoflife.date",
		// The two redis images are checks that could not run, and the overall
		// line says so.
		"1 failing, 2 checks could not run",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in:\n%s", want, rendered)
		}
	}
	if redisHits != 1 {
		t.Errorf("redis was asked %d times: one failed lookup serves every image of the product", redisHits)
	}

	out, err = runEOLAgainst(t, srv.URL, pods, map[string]any{"detail": true})
	if err != nil {
		t.Fatal(err)
	}
	rows := notGradedByImage(t, out)
	for _, image := range []string{"redis:7.2.4", "redis:6.2.1-alpine"} {
		row, ok := rows[image]
		if !ok {
			t.Errorf("%s is missing from the not-graded section", image)
			continue
		}
		if row[2] != "unanswered" || !strings.Contains(row[3], "503") || !strings.Contains(row[3], "redis") {
			t.Errorf("%s: row = %q, want the reason and the status endoflife.date answered with", image, row)
		}
	}
}

// 429 carries a Retry-After, and a rate limit is the answer a public API gives
// to exactly this audit: a request per product, in a row.
func TestKubeEOLARateLimitedProductIsAskedForAgain(t *testing.T) {
	srv, _ := newFaultyEOLServer(t, map[string]eolFault{
		"/products/redis": func(n int) (int, string) {
			if n == 1 {
				return http.StatusTooManyRequests, "0"
			}
			return 0, ""
		},
	})
	out, err := runEOLAgainst(t, srv.URL, podsRunning(t, "redis:6.2.1-alpine"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rendered := renderText(t, out); !strings.Contains(rendered, "image redis:6.2.1-alpine (1 pod in 1 namespace) | fail") {
		t.Errorf("the rate-limited product was not graded on the retry:\n%s", rendered)
	}
}

// A run that was cancelled is not a report of products that did not answer.
func TestKubeEOLACancelledRunIsAnErrorNotAReportOfUnansweredProducts(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv, _ := newFaultyEOLServer(t, map[string]eolFault{
		"/products/redis": func(int) (int, string) { cancel(); return http.StatusServiceUnavailable, "" },
	})
	fakeKubectl(t, map[string]string{"version": clusterVersionBody, "nodes": clusterNodesBody,
		"pods": podsRunning(t, "redis:7.2.4")})

	if _, err := runKubeEOLAt(ctx, eolRequest(nil), srv.URL); err == nil {
		t.Fatal("a cancelled run answered with a report")
	}
}
