package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The half that contacts the cluster: namespaces, pods, deployments.

type namespaceItem struct {
	Metadata meta `json:"metadata"`
	Status   struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

type podItem struct {
	Metadata meta `json:"metadata"`
	Spec     struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		ContainerStatuses []struct {
			Ready        bool  `json:"ready"`
			RestartCount int   `json:"restartCount"`
			State        state `json:"state"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

// state is a container's current state, of which exactly one field is set.
type state struct {
	Waiting *struct {
		Reason string `json:"reason"`
	} `json:"waiting,omitempty"`
	Terminated *struct {
		Reason string `json:"reason"`
	} `json:"terminated,omitempty"`
}

type deploymentItem struct {
	Metadata meta `json:"metadata"`
	Spec     struct {
		Replicas *int `json:"replicas"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas     int `json:"readyReplicas"`
		UpdatedReplicas   int `json:"updatedReplicas"`
		AvailableReplicas int `json:"availableReplicas"`
	} `json:"status"`
}

func runNamespaceList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	// Namespaces are cluster-scoped, so a namespace selection would be
	// meaningless here and kubectl would ignore it. Cleared rather than
	// passed, so the request rta makes is the request this describes.
	s.Namespace, s.AllNS = "", false
	var out list[namespaceItem]
	if verr := getJSON(ctx, s, "namespaces", &out); verr != nil {
		return nil, verr
	}
	rows := make([][]string, 0, len(out.Items))
	for _, n := range out.Items {
		rows = append(rows, []string{n.Metadata.Name, n.Status.Phase, age(n.Metadata.CreationTimestamp)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	return view.Table{
		Columns: []view.Column{
			{Name: "namespace"}, {Name: "status", Kind: view.KindStatus}, {Name: "age", Kind: view.KindDuration},
		},
		Rows: rows, Total: len(rows),
	}, nil
}

// podHealth is the two things about a pod that a phase alone does not say.
type podHealth struct {
	ready    string
	restarts int
	status   string
	healthy  bool
}

func healthOf(p podItem) podHealth {
	ready, total, restarts := 0, len(p.Status.ContainerStatuses), 0
	reason := ""
	for _, c := range p.Status.ContainerStatuses {
		if c.Ready {
			ready++
		}
		restarts += c.RestartCount
		// The first container that has something to say about why it is not
		// running. A waiting reason (ImagePullBackOff, CrashLoopBackOff) is
		// the actual diagnosis, and the pod's own phase reports "Pending" for
		// all of them.
		if reason == "" {
			switch {
			case c.State.Waiting != nil && c.State.Waiting.Reason != "":
				reason = c.State.Waiting.Reason
			case c.State.Terminated != nil && c.State.Terminated.Reason != "":
				reason = c.State.Terminated.Reason
			}
		}
	}
	status := p.Status.Phase
	if reason != "" {
		status = reason
	}
	return podHealth{
		ready:    fmt.Sprintf("%d/%d", ready, total),
		restarts: restarts,
		status:   status,
		// Running is not enough on its own: a pod whose containers are not
		// all ready is not serving, and that is the case an overview exists
		// to surface.
		healthy: p.Status.Phase == "Running" && total > 0 && ready == total,
	}
}

func fetchPods(ctx context.Context, s selection) ([]podItem, *view.Error) {
	var out list[podItem]
	if verr := getJSON(ctx, s, "pods", &out); verr != nil {
		return nil, verr
	}
	sort.Slice(out.Items, func(i, j int) bool {
		if out.Items[i].Metadata.Namespace != out.Items[j].Metadata.Namespace {
			return out.Items[i].Metadata.Namespace < out.Items[j].Metadata.Namespace
		}
		return out.Items[i].Metadata.Name < out.Items[j].Metadata.Name
	})
	return out.Items, nil
}

func runPodList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	pods, verr := fetchPods(ctx, s)
	if verr != nil {
		return nil, verr
	}
	return podTable(pods, s.AllNS), nil
}

func podTable(pods []podItem, withNS bool) view.Table {
	cols := []view.Column{}
	if withNS {
		cols = append(cols, view.Column{Name: "namespace"})
	}
	cols = append(cols,
		view.Column{Name: "pod"},
		view.Column{Name: "ready"},
		view.Column{Name: "status", Kind: view.KindStatus},
		view.Column{Name: "restarts", Kind: view.KindNumber},
		view.Column{Name: "age", Kind: view.KindDuration},
		view.Column{Name: "node"},
	)
	rows := make([][]string, 0, len(pods))
	for _, p := range pods {
		h := healthOf(p)
		row := []string{}
		if withNS {
			row = append(row, p.Metadata.Namespace)
		}
		rows = append(rows, append(row,
			p.Metadata.Name, h.ready, h.status,
			fmt.Sprintf("%d", h.restarts), age(p.Metadata.CreationTimestamp), p.Spec.NodeName))
	}
	return view.Table{Columns: cols, Rows: rows, Total: len(rows)}
}

func fetchDeployments(ctx context.Context, s selection) ([]deploymentItem, *view.Error) {
	var out list[deploymentItem]
	if verr := getJSON(ctx, s, "deployments", &out); verr != nil {
		return nil, verr
	}
	sort.Slice(out.Items, func(i, j int) bool {
		if out.Items[i].Metadata.Namespace != out.Items[j].Metadata.Namespace {
			return out.Items[i].Metadata.Namespace < out.Items[j].Metadata.Namespace
		}
		return out.Items[i].Metadata.Name < out.Items[j].Metadata.Name
	})
	return out.Items, nil
}

// desired reads spec.replicas, which is a pointer because zero and unset are
// different: a deployment scaled to zero on purpose is not the same as one
// whose replica count the API server is filling in.
func desired(d deploymentItem) int {
	if d.Spec.Replicas == nil {
		return 1
	}
	return *d.Spec.Replicas
}

func runDeploymentList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	deps, verr := fetchDeployments(ctx, s)
	if verr != nil {
		return nil, verr
	}
	return deploymentTable(deps, s.AllNS), nil
}

func deploymentTable(deps []deploymentItem, withNS bool) view.Table {
	cols := []view.Column{}
	if withNS {
		cols = append(cols, view.Column{Name: "namespace"})
	}
	cols = append(cols,
		view.Column{Name: "deployment"},
		view.Column{Name: "ready"},
		view.Column{Name: "up to date", Kind: view.KindNumber},
		view.Column{Name: "available", Kind: view.KindNumber},
		view.Column{Name: "age", Kind: view.KindDuration},
	)
	rows := make([][]string, 0, len(deps))
	for _, d := range deps {
		row := []string{}
		if withNS {
			row = append(row, d.Metadata.Namespace)
		}
		rows = append(rows, append(row,
			d.Metadata.Name,
			fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, desired(d)),
			fmt.Sprintf("%d", d.Status.UpdatedReplicas),
			fmt.Sprintf("%d", d.Status.AvailableReplicas),
			age(d.Metadata.CreationTimestamp)))
	}
	return view.Table{Columns: cols, Rows: rows, Total: len(rows)}
}

// suggestNamespaces completes a namespace name.
//
// This one *does* contact the cluster, unlike suggestContexts, and that is
// the reason it is offered on the CLI's deliberate completion and nowhere
// speculative: the whole rule turns on a completion not being an
// action. A namespace list is a read the caller could have made anyway, and
// it is bounded by the same timeout as every other call here.
func suggestNamespaces(ctx context.Context, req plugin.Request) []string {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil
	}
	s.Namespace, s.AllNS = "", false
	var out list[namespaceItem]
	if verr := getJSON(ctx, s, "namespaces", &out); verr != nil {
		return nil
	}
	names := make([]string, 0, len(out.Items))
	for _, n := range out.Items {
		names = append(names, n.Metadata.Name)
	}
	sort.Strings(names)
	return names
}

// plural is one word or two, for sentences the overview builds.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// truncate keeps a joined list readable when a cluster is very unhealthy.
func truncate(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(" and %d more", len(items)-max)
}
