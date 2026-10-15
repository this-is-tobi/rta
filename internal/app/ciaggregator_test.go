package app

import (
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/goccy/go-yaml"
)

// all-jobs-passed is the one status check the repository requires, and it
// passes for any job it does not name: a job added to ci.yml and left out of its
// `needs` can fail and merge. The list is kept by hand, and the day the one test
// job became four it grew by four ids. This holds the two to each other, so the
// next addition cannot be forgotten, and a job that is removed cannot stay
// behind in it, which would be a workflow that does not start.
func TestTheAggregateCheckWaitsForEveryOtherJobOfTheWorkflow(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}

	const aggregate = "all-jobs-passed"
	job, ok := workflow.Jobs[aggregate]
	if !ok {
		t.Fatalf("ci.yml has no %s job", aggregate)
	}
	list, _ := job["needs"].([]any)
	var waits []string
	for _, id := range list {
		waits = append(waits, fmt.Sprint(id))
	}
	if len(waits) == 0 {
		t.Fatalf("%s waits for nothing", aggregate)
	}

	for id := range workflow.Jobs {
		if id != aggregate && !slices.Contains(waits, id) {
			t.Errorf("the job %q is not in the needs of %s, so it can fail and the required check still passes", id, aggregate)
		}
	}
	for _, id := range waits {
		if _, ok := workflow.Jobs[id]; !ok {
			t.Errorf("%s waits for %q, which is not a job of ci.yml", aggregate, id)
		}
	}
}
