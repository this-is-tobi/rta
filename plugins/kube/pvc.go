package main

import (
	"context"
	"sort"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// kube.pvc.list: what is provisioned, not what is used.
//
// **Deliberately capacity, not %-used.** The PVC object states requested and
// (once bound) actual capacity; how full the filesystem inside it is is a
// different question the object cannot answer — that number lives in the
// kubelet's per-node stats/summary endpoint (cAdvisor volume stats), reached
// through a node proxy rather than a plain `kubectl get -o json`, and is not
// what this capability does. A capacity-only PVC list without that caveat
// would silently read as more than it is; the caveat is worth stating rather
// than shipping the column disguised as something it is not.

type pvcItem struct {
	Metadata meta `json:"metadata"`
	Spec     struct {
		StorageClassName string `json:"storageClassName"`
		Resources        struct {
			Requests struct {
				Storage string `json:"storage"`
			} `json:"requests"`
		} `json:"resources"`
	} `json:"spec"`
	Status struct {
		Phase    string `json:"phase"`
		Capacity struct {
			Storage string `json:"storage"`
		} `json:"capacity"`
	} `json:"status"`
}

func runPVCList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	var pvcs list[pvcItem]
	if verr := getJSON(ctx, s, "persistentvolumeclaims", &pvcs); verr != nil {
		return nil, verr
	}
	sort.Slice(pvcs.Items, func(i, j int) bool {
		a, b := pvcs.Items[i].Metadata, pvcs.Items[j].Metadata
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})

	cols := []view.Column{}
	if s.AllNS {
		cols = append(cols, view.Column{Name: "namespace"})
	}
	cols = append(cols,
		view.Column{Name: "pvc"},
		view.Column{Name: "status", Kind: view.KindStatus},
		view.Column{Name: "capacity", Kind: view.KindBytes},
		view.Column{Name: "requested", Kind: view.KindBytes},
		view.Column{Name: "storage class"},
		view.Column{Name: "age", Kind: view.KindDuration},
	)
	rows := make([][]string, 0, len(pvcs.Items))
	for _, p := range pvcs.Items {
		row := []string{}
		if s.AllNS {
			row = append(row, p.Metadata.Namespace)
		}
		rows = append(rows, append(row,
			p.Metadata.Name, p.Status.Phase,
			quantityBytes(p.Status.Capacity.Storage),
			quantityBytes(p.Spec.Resources.Requests.Storage),
			p.Spec.StorageClassName, age(p.Metadata.CreationTimestamp)))
	}
	return view.Table{Columns: cols, Rows: rows, Total: len(rows)}, nil
}

// quantityBytes renders a Kubernetes byte quantity through this codebase's
// shared format.Bytes, so a PVC's capacity reads in the same units as every
// other byte count rta shows — rather than passing "10Gi" through as-is,
// which looks the same until a neighbouring row is "500Mi" and the two no
// longer share a scale at a glance. An unparseable or absent value (a PVC
// still Pending has no status.capacity yet) reports "" rather than "0 B",
// which would read as an empty volume instead of an unknown one.
func quantityBytes(q string) string {
	b, ok := parseBytes(q)
	if !ok {
		return ""
	}
	return format.Bytes(b)
}
