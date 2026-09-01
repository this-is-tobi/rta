package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

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
//
// Pods, quotas and certificates are three independent kubectl calls with
// nothing to say to each other, fetched concurrently rather than one after
// another — a dashboard tile that takes four round-trips to refresh instead
// of one is a tile somebody turns back off. Metrics are deliberately not a
// fourth: metrics-server is an add-on a bare cluster often does not have, and
// folding a frequently-absent dependency into the one view meant to always
// answer would make the common case noisier, not more useful. kube.metrics.pod
// and kube.metrics.node stay their own capabilities for exactly that reason.

// overviewFetch is the concurrent read: what every goroutine below writes to
// its own field, read only after wg.Wait() — no field is written by more
// than one goroutine, so nothing here needs a mutex.
type overviewFetch struct {
	namespaces list[namespaceItem]
	nsErr      *view.Error
	pods       []podItem
	podErr     *view.Error
	quotas     list[resourceQuotaItem]
	quotaErr   *view.Error
	certs      list[tlsSecretItem]
	certErr    *view.Error
}

func fetchOverview(ctx context.Context, all, nsSel selection) overviewFetch {
	var f overviewFetch
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); f.nsErr = getJSON(ctx, nsSel, "namespaces", &f.namespaces) }()
	go func() { defer wg.Done(); f.pods, f.podErr = fetchPods(ctx, all) }()
	go func() { defer wg.Done(); f.quotas, f.quotaErr = fetchQuotas(ctx, all) }()
	go func() { defer wg.Done(); f.certs, f.certErr = fetchTLSSecrets(ctx, all) }()
	wg.Wait()
	return f
}

// quotaPressureThreshold is when a tracked resource is worth naming in an
// overview meant to be read in passing: 80%, the point a person still has
// time to act before a namespace starts refusing pods rather than the point
// it already has.
const quotaPressureThreshold = 0.8

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

	f := fetchOverview(ctx, all, nsSel)

	if f.nsErr != nil {
		// The cluster not answering is the headline, not a failure: this is
		// the capability somebody runs *because* something is wrong, so it
		// reports the unreachability and everything it could still learn from
		// the kubeconfig, rather than refusing outright.
		//
		// But "did not answer" was applied to every failure, and only one of
		// the three kinds classify() distinguishes actually means silence.
		//
		// Two of them mean the cluster answered and said no, which rendered as
		// `did not answer — namespaces is forbidden: User ... cannot list
		// resource "namespaces"` — a sentence that contradicts itself inside
		// its own line.
		//
		// The rest never reached a cluster at all: no kubeconfig, an unknown
		// context, kubectl not installed, the call cancelled. Those read worse
		// than the refusal did, because "did not answer" sends somebody to
		// check a VPN and a firewall over a purely local problem — and
		// `kubectl` missing is the most likely way this plugin fails on a
		// machine the first time.
		//
		// classify() already draws both distinctions and hands over its own
		// hint per code; all this does is stop discarding them.
		// The default is deliberately the vaguest of the four rather than the
		// most common one. "did not answer" is a claim about the network, and
		// a code this does not recognise — kube.failed is the catch-all, and
		// classify() may grow others — is precisely the case where that claim
		// is unsupported. An unmapped code now says only that the read did not
		// happen, which is the part that is always true.
		answer := "could not be read"
		switch f.nsErr.Code {
		case "kube.forbidden":
			answer = "answered, and refused"
		case "kube.unauthorized":
			answer = "answered, and did not accept this credential"
		case "kube.noconfig", "kube.context.unknown", "kube.kubectl.missing", "kube.cancelled":
			answer = "was never contacted"
		case "kube.unreachable":
			answer = "did not answer"
		}
		pairs = append(pairs,
			view.Pair{Key: "cluster", Value: answer + " — " + f.nsErr.Message},
			view.Pair{Key: "what to check", Value: hintOf(f.nsErr)})
		return view.KeyValue{Pairs: pairs}, nil
	}
	pairs = append(pairs, view.Pair{Key: "cluster", Value: "answering"})
	pairs = append(pairs, view.Pair{Key: "namespaces",
		Value: fmt.Sprintf("%d", len(f.namespaces.Items))})

	if f.podErr != nil {
		pairs = append(pairs, view.Pair{Key: "pods", Value: "could not be read — " + f.podErr.Message})
		return view.KeyValue{Pairs: pairs}, nil
	}
	var unhealthy []podItem
	var podNames []string
	for _, p := range f.pods {
		if !healthOf(p).healthy {
			unhealthy = append(unhealthy, p)
			podNames = append(podNames, p.Metadata.Namespace+"/"+p.Metadata.Name)
		}
	}
	pairs = append(pairs, view.Pair{Key: "pods",
		Value: fmt.Sprintf("%d, %d not ready", len(f.pods), len(unhealthy))})
	if len(unhealthy) > 0 {
		pairs = append(pairs, view.Pair{
			Key:   plural(len(unhealthy), "not ready", "not ready"),
			Value: truncate(podNames, 6)})
	}

	var pressure []string
	if f.quotaErr == nil {
		pressure = quotaPressure(f.quotas, quotaPressureThreshold)
		if len(pressure) > 0 {
			pairs = append(pairs, view.Pair{
				Key:   plural(len(pressure), "quota over 80%", "quotas over 80%"),
				Value: truncate(pressure, 6)})
		}
	}
	var expiredCerts, soonCerts []string
	if f.certErr == nil {
		expiredCerts, soonCerts = certPressure(f.certs)
		if len(expiredCerts) > 0 {
			pairs = append(pairs, view.Pair{
				Key:   plural(len(expiredCerts), "cert expired", "certs expired"),
				Value: truncate(expiredCerts, 6)})
		}
		if len(soonCerts) > 0 {
			pairs = append(pairs, view.Pair{
				Key:   plural(len(soonCerts), "cert expiring soon", "certs expiring soon"),
				Value: truncate(soonCerts, 6)})
		}
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
	if len(pressure) > 0 {
		sections = append(sections, view.Section{
			ID: "quotas", Title: "Resource quotas over 80%",
			View: view.Text{Body: strings.Join(pressure, "\n")}})
	}
	if len(expiredCerts)+len(soonCerts) > 0 {
		var lines []string
		for _, l := range expiredCerts {
			lines = append(lines, l+": expired")
		}
		for _, l := range soonCerts {
			lines = append(lines, fmt.Sprintf("%s: expiring within %d days", l, warnDays))
		}
		sections = append(sections, view.Section{
			ID: "certs", Title: "Certificates needing attention",
			View: view.Text{Body: strings.Join(lines, "\n")}})
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
