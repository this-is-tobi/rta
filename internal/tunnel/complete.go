package tunnel

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Completing a coordinate, one segment at a time.
//
// Everything here runs only when an operator pressed the completion key on a
// field — never per keystroke, never at form build, never from shell
// completion, which stays pure. The one exception written into §8 is the
// segment that needs no cluster at all: the kind list is static and returns
// before kubectl is even looked for, so a machine without kubectl still
// completes what it locally can.

// Completion is what completing one segment produced.
type Completion struct {
	// Items are full field values ready for a suggestion list: the typed
	// prefix, the found value, and the separator the grammar wants next — so
	// accepting one leaves the cursor exactly where the next segment starts.
	Items []string
	// Names are the bare values, for the one-line listing a screen shows when
	// the field is still empty and a ghost suggestion has nothing to attach to.
	Names []string
	// What was listed, e.g. "namespaces in homelab" — the subject of both the
	// "no X" sentence and the "8 X" one.
	What string
}

// kubeKinds is what the kind segment offers. parseKube deliberately accepts
// any non-empty kind — kubectl is the authority on what can be forwarded to —
// so this is a suggestion list, not a validation list: the four everybody
// means, in kubectl's own short spellings.
var kubeKinds = []string{"svc", "pod", "deploy", "sts"}

// CompleteKube completes the next segment of a partial coordinate
// (context/namespace/kind/name:port).
//
// The segment being completed is the one the cursor is in: everything before
// the last separator is taken as decided and pins the kubectl call —
// `--context` from the typed context, `--namespace` from the typed namespace,
// never the ambient current-context. Completing `prod-mirror` must not poke
// whatever `prod` currently points at.
func CompleteKube(ctx context.Context, partial string) (Completion, *view.Error) {
	segs := strings.Split(partial, "/")
	switch len(segs) {
	case 1:
		return kubeContexts(ctx)
	case 2:
		return kubeList(ctx, Completion{What: "namespaces in " + segs[0]},
			segs[0]+"/", "/", "--context", segs[0], "get", "namespaces", "-o", "name")
	case 3:
		prefix := segs[0] + "/" + segs[1] + "/"
		items := make([]string, 0, len(kubeKinds))
		for _, k := range kubeKinds {
			items = append(items, prefix+k+"/")
		}
		return Completion{Items: items, Names: kubeKinds, What: "kinds"}, nil
	case 4:
		kctx, ns, kind := segs[0], segs[1], segs[2]
		name, _, hasPort := strings.Cut(segs[3], ":")
		if !hasPort {
			return kubeList(ctx, Completion{What: kind + " names in " + ns},
				partial[:strings.LastIndexByte(partial, '/')+1], ":",
				"--context", kctx, "--namespace", ns, "get", kind, "-o", "name")
		}
		return kubePorts(ctx, partial, kctx, ns, kind, name)
	default:
		return Completion{}, view.Errorf("tunnel.target.malformed",
			"%q is not a kube coordinate: want four slash-separated segments", partial).
			WithHint("the form is context/namespace/kind/name:port, e.g. " +
				"homelab/databases/svc/postgres:5432")
	}
}

// CompleteSecretRef completes a `<secret>/<key>` reference against the
// namespace the connection's own coordinate names — the same rule the read at
// call time enforces (Secrets): a reference can never reach a namespace the
// coordinate did not already grant.
//
// Completing the key half is a real `get secret` in the cluster's audit log —
// there is no keys-only read, RBAC's `get` grants the values — which is why
// this runs only on the explicit keypress and why the kubectl call renders
// key names through a go-template: the values never enter rta's process at
// all, which is tighter than the call-time read that must decode what it
// delivers.
func CompleteSecretRef(ctx context.Context, coordinate, partial string) (Completion, *view.Error) {
	kctx, ns, _, _, _, verr := parseKube(coordinate)
	if verr != nil {
		return Completion{}, verr
	}
	name, _, hasKey := strings.Cut(partial, "/")
	if hasKey && strings.Contains(partial[strings.IndexByte(partial, '/')+1:], "/") {
		return Completion{}, view.Errorf("tunnel.secretref.malformed",
			"%q is not a secret reference: write it as <secret>/<key>", partial)
	}
	if !hasKey {
		return kubeList(ctx, Completion{What: "Secrets in " + ns},
			"", "/", "--context", kctx, "--namespace", ns, "get", "secrets", "-o", "name")
	}
	return kubeList(ctx, Completion{What: "keys of Secret " + name},
		name+"/", "", "--context", kctx, "--namespace", ns, "get", "secret", name,
		"-o", `go-template={{range $k, $v := .data}}{{$k}}{{"\n"}}{{end}}`)
}

// kubeContexts lists what kubeconfig holds. Local — `kubectl config` never
// touches a network — which is what makes this segment eligible for §8's
// automatic tier; it still runs from the same keypress as the rest, because
// one key with one meaning beats two rules an operator has to hold.
func kubeContexts(ctx context.Context) (Completion, *view.Error) {
	c := Completion{What: "kube contexts"}
	lines, verr := kubeLines(ctx, c.What, "config", "get-contexts", "-o", "name")
	if verr != nil {
		return c, verr
	}
	for _, name := range lines {
		// A context with a slash in its name — an EKS ARN, typically — cannot
		// appear in a coordinate at all: parseKube splits on slashes. Offering
		// it would complete to a value the very next validation refuses, so it
		// is left out; `kubectl config rename-context` is the way through.
		if strings.Contains(name, "/") {
			continue
		}
		c.Names = append(c.Names, name)
	}
	sort.Strings(c.Names)
	for _, name := range c.Names {
		c.Items = append(c.Items, name+"/")
	}
	return c, nil
}

// kubePorts lists what the named object declares, from the spec path its kind
// keeps ports under.
func kubePorts(ctx context.Context, partial, kctx, ns, kind, name string) (Completion, *view.Error) {
	var path string
	switch kind {
	case "svc", "service", "services":
		path = "{.spec.ports[*].port}"
	case "pod", "pods", "po":
		path = "{.spec.containers[*].ports[*].containerPort}"
	default:
		// Everything else forwardable — deploy, sts, rs, rc, long or short —
		// wraps a pod template.
		path = "{.spec.template.spec.containers[*].ports[*].containerPort}"
	}
	c := Completion{What: "declared ports of " + name}
	lines, verr := kubeLines(ctx, c.What,
		"--context", kctx, "--namespace", ns, "get", kind, name, "-o", "jsonpath="+path)
	if verr != nil {
		return c, verr
	}
	prefix := partial[:strings.LastIndexByte(partial, ':')+1]
	for _, line := range lines {
		c.Names = append(c.Names, strings.Fields(line)...)
	}
	sort.Strings(c.Names)
	for _, p := range c.Names {
		c.Items = append(c.Items, prefix+p)
	}
	return c, nil
}

// kubeList runs one listing and rewrites each found name as prefix+name+next.
// `-o name` output arrives as kind/name; the name is what completes.
func kubeList(ctx context.Context, c Completion, prefix, next string, args ...string) (Completion, *view.Error) {
	lines, verr := kubeLines(ctx, c.What, args...)
	if verr != nil {
		return c, verr
	}
	for _, line := range lines {
		if i := strings.LastIndexByte(line, '/'); i >= 0 {
			line = line[i+1:]
		}
		if line != "" {
			c.Names = append(c.Names, line)
		}
	}
	sort.Strings(c.Names)
	for _, name := range c.Names {
		c.Items = append(c.Items, prefix+name+next)
	}
	return c, nil
}

// waitDelay bounds how long a finished-or-killed kubectl may hold its pipes.
//
// A kubeconfig's exec credential helper is handed kubectl's own stderr, so a
// helper that outlives the kubectl rta started — kubelogin shelling out to a
// browser for an OIDC device flow is the concrete one — keeps that write end
// open after kubectl is gone. Cmd.Wait with WaitDelay unset ends in an
// unguarded receive on the copier's channel, so it blocks until every pipe
// sees EOF, which that helper is holding: not slow, *forever*, and past every
// deadline the caller set, because it is os/exec's copying goroutines that
// are stuck rather than the process. exec.CommandContext does not save it —
// cancelling kills kubectl and does not close the pipes.
//
// This applies to every kubectl rta starts, which is why it is not named
// after the listing that needed it first. It cannot cut a healthy child
// short: WaitDelay only starts counting once the process has exited or the
// context has killed it.
const waitDelay = 2 * time.Second

// kubeLines runs one read-only kubectl and returns its stdout lines.
//
// No harden/reap: those exist for the long-lived forward, whose kubectl must
// die with its whole process group. A listing runs to completion in
// milliseconds or is killed by the caller's context, and WaitDelay covers the
// one way that kill can hang.
func kubeLines(ctx context.Context, what string, args ...string) ([]string, *view.Error) {
	if _, err := exec.LookPath(kubectl); err != nil {
		return nil, view.Errorf("tunnel.kubectl.missing",
			"completing from a cluster needs kubectl and it is not on $PATH").
			WithHint("the field still takes typing — completion is an assist, not a gate")
	}
	cmd := exec.CommandContext(ctx, kubectl, args...)
	cmd.WaitDelay = waitDelay
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, view.Errorf("tunnel.list.timeout",
				"listing %s did not answer in time", what).
				WithHint("the field still takes typing")
		}
		return nil, listFailed(what, stderr.String())
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// listFailed classifies a refused listing. Fail open is the caller's half —
// every one of these lands beside a field that still takes typing.
func listFailed(what, stderr string) *view.Error {
	s := strings.TrimSpace(stderr)
	one := s
	if i := strings.IndexByte(one, '\n'); i >= 0 {
		one = one[:i]
	}
	switch {
	case strings.Contains(s, "forbidden") || strings.Contains(s, "Unauthorized"):
		return view.Errorf("tunnel.list.denied", "the cluster refused listing %s", what).
			WithHint("completion needs `list`, which the forward itself does not — type the value instead")
	case s == "":
		return view.Errorf("tunnel.list.failed", "kubectl could not list %s", what)
	default:
		return view.Errorf("tunnel.list.failed", "%s", one).
			WithHint("that message is kubectl's; the field still takes typing")
	}
}
