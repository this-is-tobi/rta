// Command rta-plugin-kube is the read-first fast path onto a Kubernetes
// cluster: which contexts this machine has, which namespaces, pods and
// deployments a cluster holds, and one composed overview — plus a small set
// of deliberate mutations: switching the current context, and minting (and
// revoking) a scoped ServiceAccount identity for an agent to use instead of
// the operator's own ambient kubeconfig (kube.serviceaccount.*, in
// serviceaccount.go).
//
// Build it and put it on your $PATH as `rta-plugin-kube`:
//
//	cd plugins/kube && go build -o ~/.local/bin/rta-plugin-kube .
//
// It needs `kubectl` and nothing else: no address to configure, no credential
// to state. Whatever cluster kubectl can already reach, this can read, using
// the same kubeconfig, the same contexts and the same exec credential plugins
// the operator already keeps working. See kubectl.go for why that is a
// decision rather than a shortcut.
//
// # What it deliberately does not do
//
// No describe, logs, exec, scale or delete on arbitrary resources — those are
// `kubectl`'s job, and this is a fast path for the common 80% — the same line
// `git` holds against growing a `git.commit`. A plugin that reaches for "just
// one more mutation" ends up as a worse copy of the CLI it was meant to save
// you from. kube.serviceaccount.provision/.revoke are not that: they are not
// a wrapped kubectl verb on a caller-chosen resource, they are a single
// purpose-built workflow (ServiceAccount + Role + RoleBinding + token,
// composed and torn down together) that no one kubectl command offers, built
// specifically so an agent never needs the operator's own credential at all.
//
// # Pin a context, or do not
//
// State one in the operator's config and every call goes there:
//
//	plugins:
//	  kube@<digest>:
//	    context: kind-rta-lab
//
// Leave it out and calls follow whatever kubectl's current context is, which
// is what a person at a terminal usually wants. `rta explain kube.pod.list`
// prints the exact heading including the digest.
//
// Every capability here reaches off the box, so none of them appear on the
// automatic dashboard on their own; add one explicitly once you have decided
// polling it is fine:
//
//	dashboard:
//	  tiles:
//	    - id: kube.overview
package main

import (
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
)

func main() { sdk.Serve(Plugin()) }

// connFields is the connection half of every capability's inputs.
//
// # Why `context` is Local
//
// **Choosing which cluster a call reaches is choosing a destination, and a
// remote caller may never choose a destination.** That is Field.Local's own
// rule, and a destination is a destination: a service plugin declares its connection as
// ordinary inputs so config can fill them, ordinary meant published in the
// MCP tool schema and accepted from a caller, and plugin.Resolve applies
// caller values above config — so an agent could name any server it liked and
// have the operator's credential filled in beside it.
//
// A kubeconfig is that hole with the credentials already attached. It is a
// list of every cluster this machine can reach, each with a working identity,
// and `--context` selects among them. An agent free to pass one would not be
// reading "the cluster the operator is working in" but *any* cluster in the
// file — production included, from a grant issued while pointed at a lab.
// internal/tunnel states the same rule for the same reason: a caller "can
// never supply a cluster coordinate of its own".
//
// So: config fills it, a person at a terminal passes it, and MCP cannot.
// Nothing is lost — an operator who wants an agent reading a specific cluster
// states it in config, which is the deliberate act that decision deserves.
//
// `namespace` is *not* Local, and the difference is the point: a namespace is
// a record inside a cluster somebody already chose, which is exactly what
// Scope means everywhere else in rta. kv.get's key works the same way.
func connFields() []plugin.Field {
	return []plugin.Field{
		{Name: "context", Type: plugin.String, Config: "context", Local: true,
			Help:    "kubeconfig context to use — the current one when omitted",
			Suggest: suggestContexts},
	}
}

// nsFields are the inputs for the capabilities that read namespaced objects.
func nsFields() []plugin.Field {
	return []plugin.Field{
		{Name: "namespace", Type: plugin.String, Positional: true,
			Help: "namespace to read — the context's own when omitted", Suggest: suggestNamespaces},
		{Name: "all-namespaces", Type: plugin.Bool,
			Help: "every namespace instead of one"},
	}
}

// cap assembles a capability's inputs in the order a person reads them: its
// own first, then the namespace selection, then the connection.
//
// NoPreview on every capability, the way plugins/pg and plugins/vault set it
// on every one of theirs, and for the property rather than the accident: each
// of these reaches off the box, so none belongs on a dashboard that refreshes
// on a timer without somebody having said so.
func cap(c plugin.Capability, own ...plugin.Field) plugin.Capability {
	c.Inputs = append(append(own, c.Inputs...), connFields()...)
	c.NoPreview = true
	return c
}

func Plugin() plugin.Plugin {
	capabilities := []plugin.Capability{
		{
			ID:      "kube.context.list",
			Summary: "Every context in this machine's kubeconfig, and which one is current",
			Description: "Reads the kubeconfig only — no cluster is contacted, so this answers " +
				"even when every cluster in it is unreachable. The current context is marked, " +
				"and it is the one every other capability here uses unless config names another.",
			Safety:     plugin.Read,
			Idempotent: true,
			NoPreview:  true,
			Run:        runContextList,
		},
		cap(plugin.Capability{
			ID:      "kube.context.get",
			Summary: "The current context in full: cluster, user and default namespace",
			Description: "What a call from this machine would reach right now. Reads the " +
				"kubeconfig only; the cluster is not contacted.",
			Safety:     plugin.Read,
			Idempotent: true,
			Run:        runContextGet,
		}),
		cap(plugin.Capability{
			ID:      "kube.namespace.list",
			Summary: "Namespaces in the cluster, with their status and age",
			Description: "The first capability here that contacts the cluster, so it is also " +
				"the quickest way to find out whether the current context can reach one.",
			Safety:     plugin.Read,
			Idempotent: true,
			Run:        runNamespaceList,
		}),
		cap(plugin.Capability{
			ID:      "kube.pod.list",
			Summary: "Pods in a namespace, with readiness, restarts and age",
			Description: "One namespace by default — the context's own — or every namespace " +
				"with --all-namespaces. Restarts are worth reading: a pod that is Running and " +
				"has restarted forty times is not healthy, and only one of those two facts " +
				"shows in its status. --unhealthy narrows to pods that are not serving: Failed, " +
				"Pending, or Running without every container ready. A pod in Succeeded is not " +
				"included — a finished Job is not a broken one. The same judgement " +
				"kube.overview makes, available here without the rest of the overview.",
			Safety:     plugin.Read,
			Idempotent: true,
			Scope:      "namespace",
			Run:        runPodList,
		}, append(nsFields(), plugin.Field{Name: "unhealthy", Type: plugin.Bool,
			Help: "only pods that are not serving — Failed, Pending, or Running without every container ready"})...),
		cap(plugin.Capability{
			ID:      "kube.deployment.list",
			Summary: "Deployments in a namespace, with how many replicas are actually ready",
			Description: "Ready against desired, which is the number that says whether a " +
				"rollout finished. One namespace by default, or every one with --all-namespaces.",
			Safety:     plugin.Read,
			Idempotent: true,
			Scope:      "namespace",
			Run:        runDeploymentList,
		}, nsFields()...),
		cap(plugin.Capability{
			ID:      "kube.quota.list",
			Summary: "ResourceQuota pressure per namespace: used against hard, as a percentage",
			Description: "One row per resource a quota tracks, not one row per quota object — " +
				"cpu, memory and pod-count headroom read as a percentage rather than two numbers " +
				"to do the division on by hand. LimitRange objects are noted by count rather than " +
				"fully modeled; their shape does not table-ize alongside a used/hard resource map.",
			Safety:     plugin.Read,
			Idempotent: true,
			Scope:      "namespace",
			Run:        runQuotaList,
		}, nsFields()...),
		cap(plugin.Capability{
			ID:      "kube.pvc.list",
			Summary: "PersistentVolumeClaims: capacity, requested size, storage class and phase",
			Description: "Provisioned capacity, not how full a volume actually is — that number " +
				"lives in kubelet volume stats, a different and more involved mechanism this does " +
				"not reach. A Pending PVC (no bound PersistentVolume yet) reports its requested " +
				"size and an empty capacity, which is the honest state of an unfulfilled claim.",
			Safety:     plugin.Read,
			Idempotent: true,
			Scope:      "namespace",
			Run:        runPVCList,
		}, nsFields()...),
		cap(plugin.Capability{
			ID:      "kube.cert.list",
			Summary: "Every TLS certificate this cluster stores as a Secret, and its expiry",
			Description: "Reads type: kubernetes.io/tls Secrets only, selected server-side so no " +
				"other secret's data ever leaves the API server for this process. The TLS Secrets " +
				"it does select arrive whole, tls.key included — Kubernetes cannot project a subset " +
				"of a Secret's data, so no way of asking avoids that. Only tls.crt is decoded; the " +
				"private key is never parsed, rendered, logged or stored, but it does cross the " +
				"wire into this process. The leaf certificate's own expiry is judged on the same " +
				"30-day window `cert expiry` and `rta audit web` use.",
			Safety:     plugin.Read,
			Idempotent: true,
			Scope:      "namespace",
			Run:        runCertList,
		}, nsFields()...),
		cap(plugin.Capability{
			ID:      "kube.metrics.pod",
			Summary: "Pod CPU/memory usage against each pod's own limit, worst pressure first",
			Description: "Needs the metrics-server add-on (metrics.k8s.io); a cluster without it " +
				"names that in the error rather than a bare \"not found\". Sorted by memory " +
				"pressure — the failure mode a container hits is OOMKilled, not \"CPU too high\" " +
				"— so the pod closest to its own limit leads regardless of namespace or name.",
			Safety:     plugin.Read,
			Idempotent: true,
			Scope:      "namespace",
			Run:        runMetricsPod,
		}, nsFields()...),
		cap(plugin.Capability{
			ID:      "kube.metrics.node",
			Summary: "Node CPU/memory usage against what the node can actually allocate",
			Description: "Same metrics-server dependency as kube.metrics.pod. Allocatable, not " +
				"capacity: a node reserves some of its own resources for the kubelet and system " +
				"daemons, and allocatable is what workloads can actually be scheduled into.",
			Safety:     plugin.Read,
			Idempotent: true,
			Run:        runMetricsNode,
		}),
		cap(plugin.Capability{
			ID:      "kube.overview",
			Summary: "One cluster at a glance: where you are pointed and what is not healthy",
			Description: "The context, whether the cluster answers, how many namespaces it " +
				"has, and every pod that is not Running or not fully ready. With --detail: " +
				"deployments whose replicas are short, and the pods themselves.",
			Safety:     plugin.Read,
			Idempotent: true,
			Detailed:   true,
			Run:        runOverview,
		}),
		{
			// Not through cap(): every other capability takes a
			// `--context` saying which cluster to read, and this one's
			// whole subject is which context becomes current. Offering
			// both would be two spellings of the same argument, one of
			// which kubectl ignores.
			ID:      "kube.context.set",
			Summary: "Switch this machine's current kubeconfig context",
			Description: "Rewrites current-context in the kubeconfig, which is what `kubectl " +
				"config use-context` does. Every later command on this machine follows it — " +
				"kubectl's, this plugin's, and anything else reading the same file — which is " +
				"why it needs a grant naming the context you mean, and why the grant is worth " +
				"reading twice before you issue it.",
			// Write and not Destructive: nothing is deleted and the
			// previous context is one call away. NeedsGrant anyway, on
			// the third trigger for one — a quiet, reversible mutation whose
			// real risk is what it silently enables afterward. Scope is
			// the context name, so a grant reads "kube.context.set
			// kind-rta-lab" and authorizes exactly that switch and no
			// other.
			Safety:     plugin.Write,
			Idempotent: true,
			NeedsGrant: true,
			Scope:      "name",
			NoPreview:  true,
			Inputs: []plugin.Field{
				{Name: "name", Type: plugin.String, Positional: true, Required: true,
					Help:    "the context to switch to — `rta kube context list` shows them",
					Suggest: suggestContexts},
			},
			Run: runContextSet,
		},
	}
	capabilities = append(capabilities, serviceAccountCapabilities()...)
	return plugin.Plugin{
		Name:    "kube",
		Summary: "Read-first Kubernetes: contexts, namespaces, pods, deployments",
		Version: "0.1.0",
		// Everything this plugin does is a kubectl call, and kubectl cannot
		// make one without the kubeconfig. Declaring it is a request, not a
		// grant: rta denies credential locations to every plugin by default,
		// and `rta plugin allow kube` is where an operator decides — against
		// this artifact's digest, so a rebuild asks again.
		Needs:        []plugin.Need{plugin.NeedKubeconfig},
		Capabilities: capabilities,
	}
}
