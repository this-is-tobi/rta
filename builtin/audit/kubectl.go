package audit

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The kube.* audits shell out to kubectl rather than linking a Kubernetes
// client, for the same reason plugins/kube does: linking one into the
// binary this file compiles into would add the client-go-shaped weight
// plugins/kube's own kubectl.go already measured and turned down — except
// here the cost is worse, not the same, because builtin/audit compiles into
// the core rta binary every user carries, not an opt-in plugin process only
// Kubernetes users install. Shelling out is a small, novel pattern for a
// builtin (only builtin/kv's editor launch does anything similar today, and
// that launches a local interactive process rather than querying a remote
// service) — chosen deliberately here rather than by default.
//
// This is a leaner exec than plugins/kube/kubectl.go's: an audit either
// reaches the cluster or reports that it could not, which is a coarser
// question than kube.go's per-capability, per-verb error classification
// needs to answer.

var kubectlBin = "kubectl"

const kubectlTimeout = 20 * time.Second

// list is the shape every `kubectl get -o json` returns.
type list[T any] struct {
	Items []T `json:"items"`
}

// kubeGetJSON runs `kubectl get <kind> -o json [-n namespace] [--context]`
// and decodes the list it returns.
func kubeGetJSON(ctx context.Context, kubeContext, namespace, kind string, out any) *view.Error {
	args := []string{"get", kind, "-o", "json", "--request-timeout=15s"}
	if kubeContext != "" {
		args = append(args, "--context="+kubeContext)
	}
	if namespace != "" {
		args = append(args, "--namespace="+namespace)
	} else {
		args = append(args, "--all-namespaces")
	}

	ctx, cancel := context.WithTimeout(ctx, kubectlTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, kubectlBin, args...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	cmd.Stdin = nil
	raw, err := cmd.Output()
	if err != nil {
		return classifyKubectl(ctx, err, errBuf.String())
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return view.Errorf("audit.kube.unreadable", "kubectl's answer for %s could not be read: %v", kind, err)
	}
	return nil
}

func classifyKubectl(ctx context.Context, err error, stderr string) *view.Error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return view.Errorf("audit.kube.unreachable", "kubectl did not answer within %s", kubectlTimeout).
			WithHint("the cluster may be unreachable, or its endpoint may be behind a VPN that is not up")
	}
	var notFound *exec.Error
	if errors.As(err, &notFound) {
		return view.Errorf("audit.kube.missing", "kubectl is not on this machine's PATH").
			WithHint("this audit drives kubectl rather than linking a Kubernetes client, so it needs " +
				"the binary the operator already uses")
	}
	msg := strings.TrimSpace(stderr)
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	msg = strings.TrimSpace(strings.TrimPrefix(msg, "error:"))
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "forbidden"), strings.Contains(low, "is not allowed"):
		return view.Errorf("audit.kube.forbidden", "%s", msg).
			WithHint("the credential this context uses does not have permission to list this resource")
	case msg != "":
		return view.Errorf("audit.kube.failed", "%s", msg)
	}
	return view.Errorf("audit.kube.failed", "kubectl failed: %v", err)
}

// contextField is the connection input every kube.* audit shares — a
// destination, so Local for the same reason plugins/kube's connFields is:
// a remote caller may never choose which cluster a call reaches.
func contextField() plugin.Field {
	return plugin.Field{Name: "context", Type: plugin.String, Config: "context", Local: true,
		Help: "kubeconfig context to audit — the current one when omitted"}
}

// namespaceField narrows an audit to one namespace.
//
// Not Local, and the difference from contextField above is the whole point: a
// context names *which cluster*, which is a destination and therefore never a
// remote caller's to choose; a namespace names a record inside a cluster
// somebody already chose, which is what Scope means everywhere else in rta.
// plugins/kube draws the same line between its own two fields.
func namespaceField() plugin.Field {
	return plugin.Field{Name: "namespace", Type: plugin.String, Config: "namespace",
		Help: "audit one namespace instead of the whole cluster"}
}

// nsRe is what may be passed to kubectl as a namespace: Kubernetes' own rule
// for the kind (an RFC 1123 label), which excludes a leading dash on its own.
//
// The `--namespace=value` form already keeps a dash-leading value inside its
// own argv element, so this is the second of two rather than the only one —
// but it also turns a typo into a sentence about the namespace name rather
// than kubectl's report about a flag it could not place.
var nsRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func checkNamespace(v string) *view.Error {
	if v == "" || nsRe.MatchString(v) {
		return nil
	}
	return view.Errorf("audit.kube.namespace.invalid", "%q is not a usable namespace name", v).
		WithHint("namespace names are lowercase letters, digits and dashes, up to 63 characters")
}

// scopeOf reads and validates the namespace narrowing shared by every kube.*
// audit that can honour one.
func scopeOf(req plugin.Request) (string, *view.Error) {
	ns := strings.TrimSpace(req.String("namespace"))
	if verr := checkNamespace(ns); verr != nil {
		return "", verr
	}
	return ns, nil
}

// within phrases a clean result so it claims only what was actually examined.
//
// **This is the difference between a true report and a false one, not a
// wording preference.** Every kube.* audit's OK finding was an absolute claim
// about the cluster — "no pod runs privileged", "every non-system namespace
// has at least one NetworkPolicy" — written when these checks could only ever
// run cluster-wide. Narrowed to one namespace those sentences stay just as
// absolute and become untrue: an audit of `gitea` would report that no pod
// runs privileged on a cluster where seventy-one do, which is worse than
// having no check at all, because somebody would believe it.
func within(ns string) string {
	if ns == "" {
		return "cluster-wide"
	}
	return "in namespace " + ns
}

// coverageClean is within's counterpart for the two coverage audits, whose
// clean sentence quantifies over namespaces rather than describing one.
func coverageClean(ns, kind string) string {
	if ns == "" {
		return "every non-system namespace has at least one " + kind
	}
	return "namespace " + ns + " has at least one " + kind
}

// rbacClean states only what a narrowed RBAC audit actually looked at. The
// cluster-wide sentence names cluster-admin; the narrowed one must not, having
// not examined a single ClusterRoleBinding.
func rbacClean(ns string) string {
	if ns == "" {
		return "no cluster-admin binding and no wildcard verb/resource/apiGroup found"
	}
	return "no Role in namespace " + ns + " uses a wildcard verb, resource or apiGroup"
}
