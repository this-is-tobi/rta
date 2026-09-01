//go:build livecnpg

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// One read of a real cluster, because the unit tests' fixtures are only as
// good as the day they were copied from the CRD: this is what notices the
// operator changing a field's spelling or a timestamp's format. Read-only by
// the plugin's own construction — every capability here is Safety: Read —
// and it runs against whatever context is current, so it is tagged out of
// every ordinary test run:
//
//	go test -tags livecnpg -run TestLive ./...
func TestLiveStatusReadsARealCluster(t *testing.T) {
	ctx := context.Background()
	var list clusterList
	if verr := getJSON(ctx, selection{allNamespace: true}, "", &list); verr != nil {
		t.Skipf("no reachable cluster: %v", verr)
	}
	if len(list.Items) == 0 {
		t.Skip("the current context has no CloudNativePG clusters")
	}

	c := list.Items[0]
	v, err := runStatus(ctx, req(map[string]any{
		"name": c.Metadata.Name, "namespace": c.Metadata.Namespace,
	}))
	if err != nil {
		t.Fatalf("status of %s/%s: %v", c.Metadata.Namespace, c.Metadata.Name, err)
	}
	sections, ok := v.(view.Sections)
	if !ok || len(sections.Items) < 2 {
		t.Fatalf("view = %#v, want overview and instances at least", v)
	}
	overview := map[string]string{}
	for _, p := range sections.Items[0].View.(view.KeyValue).Pairs {
		overview[p.Key] = p.Value
	}
	t.Logf("overview of %s/%s: %v", c.Metadata.Namespace, c.Metadata.Name, overview)

	// The derived rows must derive from live data, not only from fixtures.
	if overview["PostgreSQL"] == "" && overview["Image"] == "" {
		t.Error("neither a version nor an image row was built from the live resource")
	}
	if !strings.Contains(overview["Primary"], "primary for") &&
		!strings.Contains(overview["Primary"], "switching over") &&
		!strings.Contains(overview["Primary"], "none") {
		t.Errorf("Primary = %q carries no tenure and names no transition", overview["Primary"])
	}
	if overview["Certificates"] == "" {
		t.Error("a live CNPG cluster always reports certificate expirations, and none was read")
	}
}
