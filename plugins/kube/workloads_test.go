package main

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// podFrom decodes a pod the way kubectl hands one over, so these cases
// exercise the same struct tags runPodList and kube.overview read through
// rather than a hand-built literal that could drift from the wire shape.
func podFrom(t *testing.T, body string) podItem {
	t.Helper()
	var p podItem
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// The case that made kube.overview cry wolf: a Job that ran to completion
// sits in Succeeded with no ready containers forever, and the original
// judgement (`phase == "Running" && ready == total`) counted every one of
// them as "not ready". On a healthy cluster with any CronJob history that is
// a permanent false alarm in the one view whose whole job is to say whether
// anything is wrong.
//
// Walked as a table over every phase rather than spot-checking Succeeded,
// because the fix has to *keep* the other terminal phase unhealthy: Failed is
// also "not Running and not ready" and is exactly the thing worth surfacing.
// A fix that keyed on "terminal" instead of on Succeeded would have silenced
// both.
func TestOnlySucceededIsHealthyAmongTheNonRunningPhases(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "completed job",
			body: `{"status":{"phase":"Succeeded","containerStatuses":[{"ready":false,"restartCount":0,
				"state":{"terminated":{"reason":"Completed"}}}]}}`,
			want: true,
		},
		{
			name: "running and fully ready",
			body: `{"status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":0},
				{"ready":true,"restartCount":0}]}}`,
			want: true,
		},
		{
			name: "running but only some containers ready",
			body: `{"status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":0},
				{"ready":false,"restartCount":7,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}`,
			want: false,
		},
		{
			name: "failed",
			body: `{"status":{"phase":"Failed","containerStatuses":[{"ready":false,"restartCount":0,
				"state":{"terminated":{"reason":"Error"}}}]}}`,
			want: false,
		},
		{
			name: "pending",
			body: `{"status":{"phase":"Pending","containerStatuses":[]}}`,
			want: false,
		},
		{
			name: "unknown",
			body: `{"status":{"phase":"Unknown","containerStatuses":[{"ready":false,"restartCount":0}]}}`,
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := healthOf(podFrom(t, c.body)).healthy; got != c.want {
				t.Errorf("healthy = %v, want %v", got, c.want)
			}
		})
	}
}

// A Running pod with no containers at all is not serving anything, so the
// `total > 0` guard has to survive the Succeeded change — without it an empty
// containerStatuses list reads as "0 of 0 ready" and passes.
func TestRunningWithNoContainersIsNotHealthy(t *testing.T) {
	p := podFrom(t, `{"status":{"phase":"Running","containerStatuses":[]}}`)
	if healthOf(p).healthy {
		t.Error("a Running pod with no container statuses was judged healthy")
	}
}

// selectionOf refuses --namespace together with --all-namespaces, which is
// right for a call and wrong for a completion: the moment somebody is
// tab-completing a namespace on a line that already carries
// --all-namespaces is precisely when they want to see the list. Completion
// is not an action, so it reads the context and nothing else.
func TestNamespaceCompletionSurvivesTheAmbiguousCombination(t *testing.T) {
	withFixtureKubectl(t, `{"items":[{"metadata":{"name":"gitea"}},{"metadata":{"name":"argo-cd"}}]}`)

	got := suggestNamespaces(context.Background(), plugin.NewRequest(map[string]any{
		"namespace": "gi", "all-namespaces": true,
	}, false, false))
	if !slices.Contains(got, "gitea") || !slices.Contains(got, "argo-cd") {
		t.Errorf("suggestNamespaces = %v, want the namespaces listed despite the ambiguous pair", got)
	}
}

// The context is the one field completion does read, so an unusable one still
// stops it — a value kubectl would take for a flag must not reach argv.
func TestNamespaceCompletionStillRefusesAnUnusableContext(t *testing.T) {
	if got := suggestNamespaces(context.Background(), plugin.NewRequest(map[string]any{
		"context": "--kubeconfig=/tmp/theirs",
	}, false, false)); got != nil {
		t.Errorf("suggestNamespaces = %v, want nothing for an injectable context", got)
	}
}
