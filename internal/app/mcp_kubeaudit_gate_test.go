package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/mcp"
)

// The kube audits run kubectl on whichever context the operator has current,
// and an agent is never offered the context — which is exactly why a call
// with no grant behind it cannot be allowed to run: it would read production
// on the strength of nothing anybody said. A grant covers the namespace it
// names, or with no namespace named the whole cluster, as audit.web's covers
// a host.
//
// The kubectl on the path is a stand-in that records that it was started, so
// a gate that fails open is caught by the file it leaves and not by whatever
// cluster this machine happens to be pointed at.
func TestTheKubeAuditsRunOnlyUnderAGrantThatCoversTheNamespace(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	bin := t.TempDir()
	started := filepath.Join(bin, "started")
	stub := "#!/bin/sh\necho \"$@\" >> " + started + "\necho '{\"items\":[]}'\n"
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	server := mcp.NewServer(reg, "kube-audit-gate", mcp.Options{})
	st, ct := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "kube-audit-gate", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	call := func(tool, namespace string) (refused bool, text string) {
		args := map[string]any{}
		if namespace != "" {
			args["namespace"] = namespace
		}
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		text = res.Content[0].(*sdk.TextContent).Text
		return res.IsError && strings.Contains(text, "core.grant.required"), text
	}
	ran := func() bool {
		_, err := os.Stat(started)
		return err == nil
	}

	tools := []string{
		"audit_kube_eol", "audit_kube_netpol", "audit_kube_podsecurity", "audit_kube_quotas", "audit_kube_rbac",
	}
	for _, tool := range tools {
		if refused, text := call(tool, ""); !refused {
			t.Errorf("%s with no grant was not refused for want of one: %s", tool, text)
		}
		if refused, text := call(tool, "payments"); !refused {
			t.Errorf("%s on a namespace with no grant was not refused for want of one: %s", tool, text)
		}
	}
	if ran() {
		t.Fatal("kubectl was started for a call no grant covered")
	}

	if verr := grant.Issue(grant.Grant{
		Target: "audit.kube.podsecurity", Scope: "payments",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}
	if refused, text := call("audit_kube_podsecurity", "payments"); refused {
		t.Errorf("a grant naming payments did not cover payments: %s", text)
	}
	if !ran() {
		t.Error("the granted call never reached kubectl")
	}
	if refused, text := call("audit_kube_podsecurity", "kube-system"); !refused {
		t.Errorf("a grant naming payments covered another namespace: %s", text)
	}
	if refused, text := call("audit_kube_podsecurity", ""); !refused {
		t.Errorf("a grant naming payments covered the whole cluster: %s", text)
	}
	if refused, text := call("audit_kube_rbac", "payments"); !refused {
		t.Errorf("a grant for one audit covered another: %s", text)
	}
}
