package main

import (
	"context"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Completion for the three things somebody types here: a context, a
// namespace, a cluster name. The Live split follows the Field contract to the
// letter — a Suggest that reads a cluster answers a deliberate completion
// press only, one that reads a local file may answer the keystroke channel —
// and plugins/kube pins the same split with a test, as does main_test.go
// here.

// suggestContexts completes a context name. `kubectl config get-contexts` is
// a read of the local kubeconfig — no cluster is contacted, which is what
// lets this stay off the Live channel.
func suggestContexts(ctx context.Context, _ plugin.Request) []string {
	raw, verr := run(ctx, "config", "get-contexts", "-o=name")
	if verr != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	sort.Strings(out)
	return out
}

// suggestNamespaces completes a namespace name, and contacts the cluster to
// do it — hence Live at every field that carries it.
func suggestNamespaces(ctx context.Context, req plugin.Request) []string {
	kctx := strings.TrimSpace(req.String("context"))
	if checkName("context", kctx) != nil {
		return nil
	}
	args := []string{"get", "namespaces", "-o=name", "--request-timeout=" + requestTimeout}
	if kctx != "" {
		args = append(args, "--context="+kctx)
	}
	raw, verr := run(ctx, args...)
	if verr != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		// `-o name` prints "namespace/foo"; the value to submit is "foo".
		if name := strings.TrimPrefix(strings.TrimSpace(line), "namespace/"); name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// suggestClusters completes cnpg.status's cluster name — Live, one GET.
//
// With no namespace chosen it deliberately lists every namespace, because a
// CloudNativePG cluster conventionally lives in its own: completing against
// the kubeconfig's default namespace would answer nothing on exactly the
// setups this plugin is for. Each entry carries its namespace as the
// description, which is also the flag the caller still has to pass — the
// suggestion fills one field, not two.
func suggestClusters(ctx context.Context, req plugin.Request) []string {
	s := selection{
		context:   strings.TrimSpace(req.String("context")),
		namespace: strings.TrimSpace(req.String("namespace")),
	}
	if checkName("context", s.context) != nil || checkName("namespace", s.namespace) != nil {
		return nil
	}
	if s.namespace == "" {
		s.allNamespace = true
	}
	var list clusterList
	if getJSON(ctx, s, "", &list) != nil {
		return nil
	}
	out := make([]string, 0, len(list.Items))
	for _, c := range list.Items {
		entry := c.Metadata.Name
		if s.allNamespace {
			entry += "\t--namespace=" + c.Metadata.Namespace
		}
		out = append(out, entry)
	}
	sort.Strings(out)
	return out
}
