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
		// Live, because suggestNamespaces contacts the cluster: that read
		// must be something a completion press asked for, never something
		// typing caused — the per-keystroke channel re-evaluates plain
		// Suggests on every sibling edit, which is exactly the cadence
		// suggestNamespaces's own comment promises it is not called at.
		{Name: "namespace", Type: plugin.String, Positional: true,
			Help: "namespace to read — the context's own when omitted",
			Live: true, Suggest: suggestNamespaces},
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
			ID:      "kube.node.list",
			Summary: "Nodes, with readiness, cordon state and the pressures a kubelet reports",
			Description: "Conditions, not usage — no metrics-server needed, unlike " +
				"kube.metrics.node. Three statuses and they mean different things: NotReady is " +
				"a kubelet reporting a problem, Unknown is a kubelet that stopped reporting at " +
				"all, and SchedulingDisabled is an operator having cordoned the node on purpose. " +
				"The pressure column is the kubelet's own MemoryPressure/DiskPressure/PIDPressure " +
				"— a node can be Ready and under pressure at the same time, which is the state " +
				"worth catching before it evicts anything.",
			Safety:     plugin.Read,
			Idempotent: true,
			Run:        runNodeList,
		}),
		cap(plugin.Capability{
			ID:      "kube.pod.list",
			Summary: "Pods in a namespace, with readiness, restarts and age",
			Description: "One namespace by default — the context's own — or every namespace " +
				"with --all-namespaces. Restarts are worth reading: a pod that is Running and " +
				"has restarted forty times is not healthy, and only one of those two facts " +
				"shows in its status. --unhealthy narrows to pods that are not serving: Failed, " +
				"Pending, Unknown, or Running without every container ready. A pod in Succeeded is not " +
				"included — a finished Job is not a broken one. The same judgement " +
				"kube.overview makes, available here without the rest of the overview.",
			Safety:     plugin.Read,
			Idempotent: true,
			Scope:      "namespace",
			Run:        runPodList,
		}, append(nsFields(), plugin.Field{Name: "unhealthy", Type: plugin.Bool,
			Help: "only pods that are not serving — Failed, Pending, Unknown, or Running without every container ready"})...),
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
			ID:      "kube.event.list",
			Summary: "What the cluster is complaining about, oldest-running problems still visible",
			Description: "Warnings only unless --normal: on a cluster running any active " +
				"operator the Normal events are routine narration and outnumber the warnings " +
				"heavily.\n\nAn Event is a counter, not a log line — a recurring problem updates " +
				"the existing event rather than appending one — so first-seen and count are " +
				"reported alongside last-seen. Those two columns are the payload: an event first " +
				"seen eleven days ago with thirteen thousand occurrences is a different signal " +
				"from a one-off thirty seconds ago, and last-seen alone renders them the same. " +
				"It also means the usual \"events only go back an hour\" is true only for " +
				"problems that stopped: the TTL runs from last-seen, so anything still recurring " +
				"is never collected and its first-seen can be weeks old.",
			Safety:     plugin.Read,
			Idempotent: true,
			Scope:      "namespace",
			Run:        runEventList,
		}, append(nsFields(), plugin.Field{Name: "normal", Type: plugin.Bool,
			Help: "include Normal events, not only Warnings"})...),
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
			ID:      "kube.metrics.pressure",
			Summary: "Kernel pressure stall per node: is anything waiting, and is it getting worse",
			Description: "Reads the kubelet's own Summary API, not metrics-server. Pressure " +
				"answers a question a usage percentage cannot: whether work is actually being " +
				"held up. A node at 90% CPU with nothing waiting is a node being used well.\n\n" +
				"Each resource is reported over a 10-second and a 5-minute window, and the pair " +
				"is the point — a short window above the long one is pressure building, below it " +
				"is pressure clearing. That is the shape you would otherwise open a dashboard " +
				"for.\n\nThe \"some\" series is reported, meaning at least one task was stalled. " +
				"The \"full\" series is not: for CPU it is defined as zero at system level, so a " +
				"node stalling a third of the time reads as perfectly idle through it.\n\nNeeds " +
				"cgroup v2 and a Linux kernel 4.20 or newer; nodes without it are named rather " +
				"than shown as zeroes. Needs the nodes/proxy permission — see kube.pvc.usage.",
			Safety:     plugin.Read,
			Idempotent: true,
			Run:        runMetricsPressure,
		}, plugin.Field{Name: "node", Type: plugin.String,
			Help: "one node instead of every node"}),
		cap(plugin.Capability{
			ID:      "kube.pvc.usage",
			Summary: "How full each PersistentVolumeClaim actually is, worst first",
			Description: "The number kube.pvc.list deliberately does not report, because it " +
				"comes from somewhere else entirely: the kubelet's Summary API, one call per " +
				"node, rather than the PVC objects themselves.\n\nTwo limits worth knowing. Only " +
				"volumes a live pod currently mounts are measured at all — an unmounted claim " +
				"has no kubelet reporting on it and simply will not appear. And reaching this " +
				"needs the nodes/proxy permission, which is indivisible: it covers the whole " +
				"kubelet API including exec on every pod on the node. That is why this is a " +
				"separate capability rather than columns on kube.pvc.list, and why neither it " +
				"nor kube.metrics.pressure can be granted to a minted ServiceAccount.\n\nA node " +
				"that cannot be read is named, because a missing node means missing claims.",
			Safety:     plugin.Read,
			Idempotent: true,
			Run:        runPVCUsage,
		}, plugin.Field{Name: "node", Type: plugin.String,
			Help: "one node instead of every node"}),
		cap(plugin.Capability{
			ID:      "kube.overview",
			Summary: "One cluster at a glance: where you are pointed and what is not healthy",
			Description: "The context, whether the cluster answers, how many namespaces it " +
				"has, which nodes are not Ready, and every pod that is not serving — Failed, " +
				"Pending, Unknown, or Running without every container ready. A finished Job in " +
				"Succeeded is not one of them, and a cordoned node is reported separately rather " +
				"than counted as not ready: both are deliberate states, not faults. Pod-slot " +
				"headroom comes from the schedulable nodes' own max-pods, which is the number " +
				"that says whether a cluster can still take work when CPU and memory look fine. " +
				"With --detail: every node, deployments whose replicas are short, and the pods " +
				"themselves.\n\nReads more than it names: every ResourceQuota and every " +
				"TLS Secret in every namespace, on every run and regardless of any namespace " +
				"narrowing, to report quota pressure and certificate expiry. See kube.cert.list " +
				"for what reading a TLS Secret costs — it applies here too, unconditionally. A " +
				"credential that cannot list nodes still gets the rest: the node read is " +
				"reported as unavailable and stepped over, not treated as a failure.",
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
