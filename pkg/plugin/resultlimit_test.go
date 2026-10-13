package plugin

import (
	"strings"
	"testing"
)

// The limit is an MCP one, so the only reader of the refusal is an agent, and
// an agent called a tool by its tool name.
func TestAResultOverTheLimitNamesTheToolThatRan(t *testing.T) {
	verr := ResultTooLarge("kube.pod.list", 9<<20, DefaultResultLimit)
	if verr.Code != "core.result.toolarge" {
		t.Fatalf("code = %q", verr.Code)
	}
	if !strings.HasPrefix(verr.Message, "kube_pod_list ran and its result,") {
		t.Errorf("the refusal names the call as %q, want the kube_pod_list tool", verr.Message)
	}
}
