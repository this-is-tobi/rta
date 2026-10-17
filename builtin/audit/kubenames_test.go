package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A name in a cluster is chosen by whoever may create the object, and the
// principal an RBAC audit watches is precisely one who can create a Role. Put
// raw into a row, a name holding a newline starts a second row inside the cell
// ("wildcard rule: kube-system/fake" below), an escape sequence is stripped on
// the way to a terminal so the name shown is a different, ordinary one, and a
// right-to-left override reverses what follows it. A name cannot be cleaned,
// only shown (plugin.ListedName).
const hostileName = "r-ops\x1b[31m-admin\nwildcard rule: kube-system/fake (verbs: [*])\xe2\x80\xae"

// hostileImage is the same attack on an image reference, which is split at its
// last "/" and first ":" before it is shown, so it holds neither.
const hostileImage = "x\n\x1b[31mred\xe2\x80\xae"

func jsonBody(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type obj = map[string]any

// cells is every cell of every table a view holds.
func cells(t *testing.T, v view.View) []string {
	t.Helper()
	var out []string
	switch x := v.(type) {
	case view.Table:
		for _, row := range x.Rows {
			out = append(out, row...)
		}
	case view.Sections:
		for _, s := range x.Items {
			out = append(out, cells(t, s.View)...)
		}
	}
	return out
}

func TestAClusterSuppliedNameIsListedNotPutRawInARow(t *testing.T) {
	hostile := obj{"name": hostileName, "namespace": hostileName}
	srv, _ := newEOLServer(t)
	cases := []struct {
		name   string
		bodies map[string]string
		run    func(context.Context, plugin.Request) (view.View, error)
		// shown is what must be in the output for the name to be there at all:
		// quoted, each character a reader would not see written out.
		shown []string
	}{
		{"rbac", map[string]string{
			"clusterrolebindings": jsonBody(t, obj{"items": []obj{{
				"metadata": hostile, "roleRef": obj{"name": "cluster-admin"},
				"subjects": []obj{
					{"kind": "User", "name": hostileName},
					{"kind": "ServiceAccount", "name": hostileName, "namespace": hostileName},
				},
			}}}),
			"roles": jsonBody(t, obj{"items": []obj{{
				"metadata": hostile, "rules": []obj{{"verbs": []string{"*"}}}}}}),
			"clusterroles": jsonBody(t, obj{"items": []obj{{
				"metadata": obj{"name": hostileName}, "rules": []obj{{"verbs": []string{"*"}}}}}}),
		}, runKubeRBAC, []string{
			"cluster-admin binding: " + plugin.ListedName(hostileName),
			"wildcard rule: " + plugin.ListedName(hostileName) + "/" + plugin.ListedName(hostileName),
			"wildcard rule: " + plugin.ListedName(hostileName) + " |",
			"User " + plugin.ListedName(hostileName),
			"ServiceAccount " + plugin.ListedName(hostileName) + "/" + plugin.ListedName(hostileName),
		}},
		{"podsecurity", map[string]string{
			"pods": jsonBody(t, obj{"items": []obj{{"metadata": hostile, "spec": obj{
				"hostNetwork": true,
				"containers":  []obj{{"name": hostileName, "securityContext": obj{"privileged": true}}},
			}}}}),
		}, runKubePodSecurity, []string{
			"host namespace: " + plugin.ListedName(hostileName) + "/" + plugin.ListedName(hostileName),
			"privileged container: " + plugin.ListedName(hostileName) + "/" + plugin.ListedName(hostileName) +
				"/" + plugin.ListedName(hostileName),
			"root not excluded: " + plugin.ListedName(hostileName) + "/" + plugin.ListedName(hostileName),
		}},
		{"quotas", map[string]string{
			"namespaces":     jsonBody(t, obj{"items": []obj{{"metadata": obj{"name": hostileName}}}}),
			"resourcequotas": `{"items":[]}`,
		}, runKubeQuotas, []string{"no ResourceQuota: " + plugin.ListedName(hostileName)}},
		{"netpol", map[string]string{
			"namespaces":      jsonBody(t, obj{"items": []obj{{"metadata": obj{"name": hostileName}}}}),
			"networkpolicies": `{"items":[]}`,
		}, runKubeNetworkPolicy, []string{"no NetworkPolicy: " + plugin.ListedName(hostileName)}},
		{"eol", map[string]string{
			"version": jsonBody(t, obj{"serverVersion": obj{"gitVersion": "v1.30.2" + hostileName}}),
			"nodes": jsonBody(t, obj{"items": []obj{{
				"metadata": obj{"name": hostileName},
				"status":   obj{"nodeInfo": obj{"kubeletVersion": "v1.30.2" + hostileName}},
			}}}),
			"pods": jsonBody(t, obj{"items": []obj{
				{"metadata": obj{"namespace": "a", "name": "p"}, "spec": obj{"containers": []obj{
					{"image": "nginx:1.28" + hostileImage},
					{"image": "registry/untracked" + hostileImage + ":1"},
				}}},
			}}),
		}, func(ctx context.Context, req plugin.Request) (view.View, error) {
			return runKubeEOLAt(ctx, req, srv.URL)
		}, []string{
			"control plane: " + plugin.ListedName("v1.30.2"+hostileName),
			"kubelets at " + plugin.ListedName("v1.30.2"+hostileName),
			plugin.ListedName(hostileName),
			plugin.ListedName("nginx:1.28" + hostileImage),
			plugin.ListedName("untracked" + hostileImage + ":1"),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeKubectl(t, c.bodies)
			// The detail page: it holds the compact table's rows in its
			// sections, and the one table the eol audit lists what it could not
			// grade in.
			req := eolRequest(map[string]any{"detail": true}).With(map[string]any{"detail": true})
			if c.name != "eol" {
				req = plugin.NewRequest(map[string]any{"detail": true}, false, false)
			}
			// Over MCP, which is also where a name forged into a row reaches a
			// model, and where no detail is clipped: the clip would cut a long
			// name short of the part this asserts.
			out, err := c.run(t.Context(), req.WithSurface(plugin.SurfaceMCP))
			if err != nil {
				t.Fatal(err)
			}
			all := cells(t, out)
			if len(all) == 0 {
				t.Fatal("no cells to inspect")
			}
			for _, cell := range all {
				for _, r := range cell {
					if r != ' ' && !unicode.IsPrint(r) {
						t.Errorf("a cell holds %U a reader would not see as itself, or that moves what follows it: %q", r, cell)
						break
					}
				}
			}
			joined := strings.Join(all, " | ")
			for _, want := range c.shown {
				if !strings.Contains(joined, want) {
					t.Errorf("missing %q in:\n%s", want, joined)
				}
			}
		})
	}
}
