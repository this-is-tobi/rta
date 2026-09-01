package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Shelling out to `kubectl`, the same decision plugins/kube and internal/tunnel
// both took, and for a reason this plugin makes sharper rather than weaker.
//
// A CloudNativePG cluster's whole state is one custom resource. Reading it
// needs no typed client, no scheme registration and no informer — it is a GET
// on one object, and `kubectl get -o json` is a GET on one object. What
// linking client-go would add is +67 modules and +11 MB for the privilege of
// reimplementing authentication that already works on the operator's machine.
//
// **And that authentication is the whole point here.** This plugin exists
// because `kubectl cnpg status` does not work through every proxy people put
// in front of a cluster, while a plain API read does — so the one thing it
// must not do is stop using the credential path the operator already has
// working. Whatever kubectl can reach, including through an exec credential
// plugin, this can read.
//
// The wrapper below is deliberately not shared with plugins/kube, which has a
// larger one. Two callers is the repo's recorded bar for *considering* a seam
// and not for cutting one — builtin/keys says of an earlier duplication "two
// built-ins, ten lines, no third caller yet to justify the seam" — and these
// two need different slices: that one switches contexts, lists workloads and
// completes namespaces, this one does a single typed GET. A third operator
// plugin is what would make `pkg/kubectl` worth freezing as public API.

// kubectlBin is overridable in tests, which have no cluster and must not need
// one.
var kubectlBin = "kubectl"

// timeout bounds one kubectl invocation, and requestTimeout is what kubectl
// is told so its own error — which names the cluster and is actionable — is
// what an operator usually sees rather than this package's blunter one.
const (
	timeout        = 20 * time.Second
	requestTimeout = "15s"
)

// clusterCRD is the fully-qualified resource name, never the `cluster` short
// form. Short names are resolved through the cluster's discovery document and
// are ambiguous the moment anything else registers one; the group-qualified
// name means exactly one thing on every cluster.
const clusterCRD = "clusters.postgresql.cnpg.io"

// nameRe is what may be passed to kubectl as a context, namespace or object
// name.
//
// **Argument-injection defence, not validation for its own sake.** These
// values are interpolated into an argv slice and never into a shell string,
// so there is no shell to escape into — but a value beginning with `-` is
// still read by kubectl as a *flag*, and `--kubeconfig=/tmp/mine` arriving
// where a context name was expected would point the call at a different
// cluster entirely. The `--flag=value` form used throughout already prevents
// that by keeping the value in its flag's own argv element; this refuses it a
// second time and earlier, so a future caller reaching for the two-element
// form cannot reintroduce it. Copied deliberately from plugins/kube, which
// records the same reasoning: it is the half of that wrapper that must not
// drift, so it drifts by being identical rather than by being imported.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,252}$`)

func checkName(kind, v string) *view.Error {
	if v == "" {
		return nil
	}
	if !nameRe.MatchString(v) {
		return view.Errorf("cnpg.name.invalid", "%q is not a usable %s name", v, kind).
			WithHint("Kubernetes names are letters, digits, dots and dashes; a leading " +
				"dash would be read as a flag")
	}
	return nil
}

// selection is which cluster and which namespace a call reads.
type selection struct {
	context      string
	namespace    string
	allNamespace bool
}

func selectionOf(req plugin.Request) (selection, *view.Error) {
	s := selection{
		context:      strings.TrimSpace(req.String("context")),
		namespace:    strings.TrimSpace(req.String("namespace")),
		allNamespace: req.Bool("all-namespaces"),
	}
	if verr := checkName("context", s.context); verr != nil {
		return selection{}, verr
	}
	if verr := checkName("namespace", s.namespace); verr != nil {
		return selection{}, verr
	}
	// **Refused here rather than resolved in args(), which prefers
	// --all-namespaces when both are set.** That preference is safe only for
	// as long as these capabilities declare no Scope: rta derives the scope a
	// call is checked against from the value of the `namespace` field, and
	// knows nothing about `all-namespaces`. Give either capability below
	// `Scope: "namespace"` — the natural thing to do, since both already take
	// a namespace — and a caller granted one namespace could send it together
	// with --all-namespaces, pass the scope check on the first, and be
	// answered from every namespace in the cluster.
	//
	// So this is not fixing a live bug here; it is refusing to leave a trap
	// that springs on whoever adds the scope later and has no reason to look
	// at this function. plugins/kube carries the same refusal for the same
	// reason, where the scope *is* declared and the bypass was real.
	if s.allNamespace && s.namespace != "" {
		return selection{}, view.Errorf("cnpg.namespace.ambiguous",
			"--namespace and --all-namespaces ask for different things").
			WithHint("pass one or the other — a namespace to read that namespace, " +
				"--all-namespaces to read every one")
	}
	return s, nil
}

// args puts the subcommand first and the selection flags after it.
//
// **That order is not cosmetic, and getting it wrong made every
// `--all-namespaces` call fail.** `--context` and `--namespace` are kubectl
// global flags and are accepted on either side of the verb; `--all-namespaces`
// is a flag of `get` and is not. kubectl reading an unrecognised flag before
// the verb concludes it is being asked for a *plugin* and answers "flags
// cannot be placed before plugin name: --request-timeout=15s" — naming the
// first flag it saw rather than the one it could not place, which is why this
// read as a timeout problem and was not one.
//
// Everything after the subcommand is the shape that works for all four flags,
// so the class is gone rather than the instance.
//
// `--flag=value` rather than `--flag value`, everywhere and without exception:
// it keeps the value inside its own argv element, so a value that begins with
// a dash is a malformed value rather than a new flag. checkName refuses those
// anyway; this is the second of the two.
func (s selection) args(rest ...string) []string {
	out := append([]string{}, rest...)
	out = append(out, "--request-timeout="+requestTimeout)
	if s.context != "" {
		out = append(out, "--context="+s.context)
	}
	switch {
	case s.allNamespace:
		out = append(out, "--all-namespaces")
	case s.namespace != "":
		out = append(out, "--namespace="+s.namespace)
	}
	return out
}

// where names what was read, for a message that has to say which cluster.
func (s selection) where() string {
	parts := []string{}
	if s.context != "" {
		parts = append(parts, "context "+s.context)
	}
	switch {
	case s.allNamespace:
		parts = append(parts, "every namespace")
	case s.namespace != "":
		parts = append(parts, "namespace "+s.namespace)
	}
	if len(parts) == 0 {
		return "the current context"
	}
	return strings.Join(parts, ", ")
}

func run(ctx context.Context, args ...string) ([]byte, *view.Error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, kubectlBin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, classify(cctx, err, stderr.String(), args)
	}
	return out, nil
}

// classify turns kubectl's stderr into something actionable.
//
// **Authentication and permission are told apart**, which is the distinction
// internal/tunnel had wrong until recently and for the same reason it matters
// more here than anywhere: this plugin's whole purpose is to be usable behind
// a proxy that hands out short-lived credentials, so "your login expired" is
// the ordinary failure and reporting it as an RBAC problem sends people to
// argue with the wrong team.
func classify(ctx context.Context, err error, stderr string, args []string) *view.Error {
	s := strings.TrimSpace(stderr)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return view.Errorf("cnpg.timeout", "kubectl did not answer within %s", timeout).
			WithHint("the cluster may be unreachable — `kubectl cluster-info` is the same " +
				"question without this plugin in the way")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		if errors.Is(err, exec.ErrNotFound) {
			return view.Errorf("cnpg.kubectl.missing", "kubectl is not on $PATH").
				WithHint("this plugin shells out to kubectl so your existing cluster " +
					"credentials keep working — install kubectl")
		}
		return view.Errorf("cnpg.kubectl.failed", "running kubectl: %v", err)
	}
	switch {
	case notAuthenticated(s):
		return view.Errorf("cnpg.unauthenticated", "this cluster does not know who you are").
			WithHint("nothing was refused — the request never got that far. Whatever " +
				"issues your cluster credentials (`tsh kube login`, `aws eks get-token`, " +
				"`gcloud`, an OIDC refresh) needs running again")
	case strings.Contains(s, "forbidden"):
		return view.Errorf("cnpg.denied", "not allowed to read %s here", clusterCRD).
			WithHint("this is the cluster refusing, not rta — you are authenticated, and the " +
				"verb is `get` on `clusters.postgresql.cnpg.io`")
	case strings.Contains(s, "server doesn't have a resource type"),
		strings.Contains(s, "the server could not find the requested resource"):
		return view.Errorf("cnpg.notinstalled", "this cluster has no CloudNativePG operator").
			WithHint("the CRD `" + clusterCRD + "` is not registered — `kubectl get crd | " +
				"grep cnpg` confirms it, and this plugin reads nothing else")
	case strings.Contains(s, "not found"):
		return view.Errorf("cnpg.cluster.missing", "%s", firstLine(s)).
			WithHint("`rta cnpg list --all-namespaces` shows what is there")
	case s == "":
		return view.Errorf("cnpg.kubectl.failed", "kubectl exited %d without saying why",
			exitErr.ExitCode())
	}
	return view.Errorf("cnpg.kubectl.failed", "%s", firstLine(s)).
		WithHint("that message is kubectl's; this plugin shells out to it so your cluster " +
			"credentials keep working")
}

// notAuthenticated reports whether stderr is about identity rather than
// permission — the API server's 401, or the credential plugin failing before
// there was a request to make at all.
func notAuthenticated(stderr string) bool {
	for _, s := range []string{
		"Unauthorized",
		"You must be logged in to the server",
		"getting credentials",
		"exec plugin",
		"credential plugin",
		"no Auth Provider found",
	} {
		if strings.Contains(stderr, s) {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// getJSON reads one or many clusters and decodes into out.
func getJSON(ctx context.Context, s selection, name string, out any) *view.Error {
	args := []string{"get", clusterCRD}
	if name != "" {
		args = append(args, name)
	}
	args = append(args, "-o", "json")
	raw, verr := run(ctx, s.args(args...)...)
	if verr != nil {
		return verr
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return view.Errorf("cnpg.decode", "kubectl's JSON did not parse: %v", err).
			WithHint("that usually means a kubectl old enough to word its output differently")
	}
	return nil
}

// age renders a timestamp the way kubectl does, and is the form the question
// is actually asked in: "how long since the last backup" rather than "at what
// instant".
func age(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
