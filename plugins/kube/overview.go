package main

import (
	"context"
	"fmt"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// kube.overview: where you are pointed, and what is not healthy.
//
// The composed capability every plugin here has (vault.overview, pg.overview)
// and the one an operator actually puts on a dashboard. It answers the
// question somebody asks before they know which question to ask, so it leads
// with *where* — a perfectly healthy cluster is the wrong answer if it is not
// the cluster you thought you were looking at.

func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	cfg, cfgErr := readConfig(ctx)
	where := s.Context
	if where == "" && cfgErr == nil {
		where = cfg.CurrentContext
	}
	if where == "" {
		where = "unknown"
	}
	pairs := []view.Pair{{Key: "context", Value: where}}
	if cfgErr == nil {
		if cluster, _, ns, ok := cfg.find(where); ok {
			pairs = append(pairs, view.Pair{Key: "server", Value: cfg.server(cluster)})
			if ns == "" {
				ns = "default"
			}
			pairs = append(pairs, view.Pair{Key: "namespace", Value: ns})
		}
	}

	// One selection for the whole overview: every namespace, because "what is
	// unhealthy" is not a question about one of them.
	all := s
	all.Namespace, all.AllNS = "", true

	nsSel := s
	nsSel.Namespace, nsSel.AllNS = "", false
	var namespaces list[namespaceItem]
	nsErr := getJSON(ctx, nsSel, "namespaces", &namespaces)
	if nsErr != nil {
		// The cluster not answering is the headline, not a failure: this is
		// the capability somebody runs *because* something is wrong, so it
		// reports the unreachability and everything it could still learn from
		// the kubeconfig, rather than refusing outright.
		pairs = append(pairs,
			view.Pair{Key: "cluster", Value: "did not answer — " + nsErr.Message},
			view.Pair{Key: "what to check", Value: hintOf(nsErr)})
		return view.KeyValue{Pairs: pairs}, nil
	}
	pairs = append(pairs, view.Pair{Key: "cluster", Value: "answering"})
	pairs = append(pairs, view.Pair{Key: "namespaces",
		Value: fmt.Sprintf("%d", len(namespaces.Items))})

	pods, podErr := fetchPods(ctx, all)
	if podErr != nil {
		pairs = append(pairs, view.Pair{Key: "pods", Value: "could not be read — " + podErr.Message})
		return view.KeyValue{Pairs: pairs}, nil
	}
	var unhealthy []podItem
	var names []string
	for _, p := range pods {
		if !healthOf(p).healthy {
			unhealthy = append(unhealthy, p)
			names = append(names, p.Metadata.Namespace+"/"+p.Metadata.Name)
		}
	}
	pairs = append(pairs, view.Pair{Key: "pods",
		Value: fmt.Sprintf("%d, %d not ready", len(pods), len(unhealthy))})
	if len(unhealthy) > 0 {
		pairs = append(pairs, view.Pair{
			Key:   plural(len(unhealthy), "not ready", "not ready"),
			Value: truncate(names, 6)})
	}

	if !req.Bool("detail") {
		return view.KeyValue{Pairs: pairs}, nil
	}

	sections := []view.Section{
		{ID: "cluster", Title: "Cluster", View: view.KeyValue{Pairs: pairs}},
	}
	if len(unhealthy) > 0 {
		sections = append(sections, view.Section{
			ID: "pods", Title: "Pods that are not ready", View: podTable(unhealthy, true)})
	}
	deps, depErr := fetchDeployments(ctx, all)
	if depErr == nil {
		var short []deploymentItem
		for _, d := range deps {
			if d.Status.ReadyReplicas < desired(d) {
				short = append(short, d)
			}
		}
		if len(short) > 0 {
			sections = append(sections, view.Section{
				ID: "deployments", Title: "Deployments short of their replicas",
				View: deploymentTable(short, true)})
		} else {
			sections = append(sections, view.Section{
				ID: "deployments", Title: "Deployments",
				View: view.Text{Body: fmt.Sprintf("all %d %s have every replica they asked for",
					len(deps), plural(len(deps), "deployment", "deployments"))}})
		}
	}
	return view.Sections{Items: sections}, nil
}

// hintOf recovers the remedy from a classified error, so the overview can put
// it on its own row instead of dropping it.
func hintOf(e *view.Error) string {
	if e == nil || e.Hint == "" {
		return "`rta kube context list` shows what this machine is configured for"
	}
	return e.Hint
}
