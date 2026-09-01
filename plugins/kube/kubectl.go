package main

import (
	"bytes"
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

// This plugin shells out to `kubectl` rather than linking client-go, which is
// the same decision internal/tunnel took for port-forwards. Two reasons, and the second is the bigger one.
//
// Size: measured for the surface a resolver needs — clientcmd, spdy,
// portforward — client-go is +67 modules and +11 MB. This plugin needs
// strictly more of it (typed clients for pods, deployments, namespaces), so
// the number is worse here, not better.
//
// Authentication: OIDC refresh, `aws eks get-token`,
// `gke-gcloud-auth-plugin` and whatever exec credential plugin an
// organisation runs are all solved on the operator's machine already, by the
// binary they already keep working. Linking client-go means adopting that
// maintenance and getting it subtly wrong for somebody. Shelling out means a
// cluster that `kubectl` can reach is a cluster this can read, on the day
// they configure it, with no code here at all.

// kubectlBin is overridable in tests, which have no cluster and must not need
// one. Nothing outside this package writes to it.
var kubectlBin = "kubectl"

// timeout bounds one kubectl invocation.
//
// A cluster that is unreachable does not fail fast: the client retries a TLS
// handshake against an endpoint nobody answers for as long as it is allowed
// to. Over MCP the caller's own client has usually given up well before
// then, and a call nobody is waiting for is a subprocess holding a slot in
// the plugin host. `--request-timeout` is passed as well so kubectl gives up
// on its own terms first, and this is the backstop for the case where it
// does not.
const timeout = 20 * time.Second

// requestTimeout is what kubectl is told, comfortably inside timeout so its
// own error — which names the cluster and is actionable — is what an operator
// usually sees, rather than this package's blunter "did not answer".
const requestTimeout = "15s"

// nameRe is what may be passed to kubectl as a context, namespace or resource
// name.
//
// **This is argument-injection defence, not input validation for its own
// sake.** Every value below is interpolated into an argv slice, never into a
// shell string, so there is no shell to escape into — but a value beginning
// with `-` is still read by kubectl as a *flag*, and `--kubeconfig=/tmp/mine`
// arriving where a context name was expected would point the call at a
// different cluster entirely. The `--flag=value` form used throughout keeps
// the value in the same argv element as its flag, which already prevents
// that; this refuses it a second time and earlier, so a future caller that
// reaches for the two-element form cannot reintroduce it.
//
// The character set is Kubernetes' own for object names (RFC 1123 labels,
// plus the dots, underscores and slashes that appear in real context names —
// `arn:aws:eks:...` clusters and `kind-rta-lab` alike), and deliberately
// excludes whitespace, quotes and everything else.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,252}$`)

// checkName refuses a value kubectl would not read as the thing it was given
// as.
func checkName(kind, v string) *view.Error {
	if v == "" {
		return nil
	}
	if !nameRe.MatchString(v) {
		return view.Errorf("kube.name.invalid",
			"%q is not a usable %s name", v, kind).
			WithHint("names are letters, digits and ._:/@- and may not begin with a dash")
	}
	return nil
}

// selection is the connection half of every call: which cluster, which
// namespace.
//
// Held together rather than passed as two strings because they are read
// together on every path and because the *rule* about them differs — see
// connFields for why one of them a remote caller may never supply.
type selection struct {
	Context   string
	Namespace string
	AllNS     bool
}

func selectionOf(req plugin.Request) (selection, *view.Error) {
	s := selection{
		Context:   strings.TrimSpace(req.String("context")),
		Namespace: strings.TrimSpace(req.String("namespace")),
		AllNS:     req.Bool("all-namespaces"),
	}
	if verr := checkName("context", s.Context); verr != nil {
		return selection{}, verr
	}
	if verr := checkName("namespace", s.Namespace); verr != nil {
		return selection{}, verr
	}
	// **Refused rather than resolved, because a grant is scoped on one of
	// these two and not the other.** args() below picks --all-namespaces when
	// both are set, which reads as a harmless precedence rule and is not one:
	// every capability here declares Scope: "namespace", and internal/grant's
	// scopes() derives the scope a call is checked against from the value of
	// the `namespace` field alone — it has no idea `all-namespaces` exists. So
	// a caller holding a grant for one namespace could send that namespace
	// *and* all-namespaces together, satisfy the scope check on the first, and
	// have the request answered from every namespace in the cluster.
	//
	// Latent rather than live today, only because no *namespace-scoped* kube
	// capability sets NeedsGrant or is Destructive, so grant.Required() is
	// false for every capability that carries these two fields and the check
	// never runs. (kube.context.set and kube.serviceaccount.revoke do set
	// them, and neither takes a namespace.) A profile makes Required() true
	// for everything, and so would adding NeedsGrant to any of these later.
	// Closing it at the point where the two fields are read means the bypass
	// cannot come back by way of a decision somewhere else that looks
	// unrelated.
	//
	// Fail closed, not "narrowest wins": silently choosing the namespace would
	// answer a question nobody asked, and a caller that sent both does not
	// know what it wants.
	if s.AllNS && s.Namespace != "" {
		return selection{}, view.Errorf("kube.namespace.ambiguous",
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
// `--flag=value` rather than `--flag value`, everywhere and without
// exception: it keeps the value inside its own argv element, so a value that
// begins with a dash is a malformed value rather than a new flag. checkName
// refuses those anyway; this is the second of the two.
func (s selection) args(rest ...string) []string {
	out := append([]string{}, rest...)
	out = append(out, "--request-timeout="+requestTimeout)
	if s.Context != "" {
		out = append(out, "--context="+s.Context)
	}
	switch {
	case s.AllNS:
		out = append(out, "--all-namespaces")
	case s.Namespace != "":
		out = append(out, "--namespace="+s.Namespace)
	}
	return out
}

// where names the cluster a message is about, for error text.
func (s selection) where() string {
	if s.Context == "" {
		return "the current context"
	}
	return "context " + s.Context
}

// run executes kubectl and returns its stdout.
//
// stderr is captured separately and never merged into stdout: stdout is
// parsed as JSON, and a warning kubectl decides to print — a deprecated API
// version, a missing auth plugin's advice — would otherwise turn a good
// answer into a parse error.
func run(ctx context.Context, args ...string) ([]byte, *view.Error) {
	return runStdin(ctx, nil, args...)
}

// runStdin is run with one difference: stdin is a byte slice this package
// built itself (a manifest for `create -f -`), never the plugin host's own
// stdin. That distinction is the reason run doesn't just set cmd.Stdin to
// the caller's os.Stdin-equivalent unconditionally — inheriting it would
// hand a kubectl subprocess the plugin host's own gRPC channel; a manifest
// this package assembled and immediately hands to a subprocess it also
// waits on has none of that risk.
func runStdin(ctx context.Context, stdin []byte, args ...string) ([]byte, *view.Error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, kubectlBin, args...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	return nil, classify(ctx, err, errBuf.String(), args)
}

// classify turns a kubectl failure into something an operator can act on.
//
// The same job plugins/pg and plugins/vault do for their own clients. What
// makes it worth the length here is that kubectl reports very different
// problems through the same exit code, and "exit status 1" is the least
// useful thing this plugin could say.
func classify(ctx context.Context, err error, stderr string, args []string) *view.Error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return view.Errorf("kube.unreachable",
			"kubectl did not answer within %s", timeout).
			WithHint("the cluster may be unreachable, or its endpoint may be behind a VPN that is not up")
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return view.Errorf("kube.cancelled", "the call was cancelled")
	}
	var notFound *exec.Error
	if errors.As(err, &notFound) {
		return view.Errorf("kube.kubectl.missing",
			"kubectl is not on this machine's PATH").
			WithHint("this plugin drives kubectl rather than linking a Kubernetes client, so it " +
				"needs the binary the operator already uses — install it, or put it on PATH")
	}
	msg := firstLine(stderr)
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "no configuration has been provided"),
		strings.Contains(low, "no such file or directory") && strings.Contains(low, "kube"):
		return view.Errorf("kube.noconfig", "there is no kubeconfig on this machine").
			WithHint("`kubectl config get-contexts` is the same question; this reads what it reads")
	case strings.Contains(low, "context") && strings.Contains(low, "does not exist"),
		strings.Contains(low, "context was not found"):
		return view.Errorf("kube.context.unknown", "%s", msg).
			WithHint("`rta kube context list` shows the contexts this machine has")
	case strings.Contains(low, "forbidden"), strings.Contains(low, "cannot list"),
		strings.Contains(low, "is not allowed"):
		// Left as the API server phrased it. An RBAC refusal names the verb,
		// the resource and the identity, and every one of those is what the
		// operator needs to fix it.
		return view.Errorf("kube.forbidden", "%s", msg).
			WithHint("the credential this context uses does not have that permission")
	case strings.Contains(low, "unauthorized"), strings.Contains(low, "invalid bearer token"),
		strings.Contains(low, "credentials"), strings.Contains(low, "exec plugin"):
		return view.Errorf("kube.unauthorized", "%s", msg).
			WithHint("the context's credential is missing or expired — whatever refreshes it " +
				"(a login, an SSO session, an exec credential plugin) has to run first")
	case strings.Contains(low, "connection refused"), strings.Contains(low, "no route to host"),
		strings.Contains(low, "i/o timeout"), strings.Contains(low, "dial tcp"),
		strings.Contains(low, "server could not find"), strings.Contains(low, "connect:"):
		return view.Errorf("kube.unreachable", "%s", msg).
			WithHint("the API server did not answer — check the VPN, the context, and that the cluster is up")
	case strings.Contains(low, "not found"):
		return view.Errorf("kube.notfound", "%s", msg)
	case msg != "":
		return view.Errorf("kube.failed", "%s", msg)
	}
	return view.Errorf("kube.failed", "kubectl %s failed: %v", strings.Join(args, " "), err)
}

// firstLine trims kubectl's stderr to the sentence worth showing.
//
// kubectl prefixes many errors with "error: " and prints multi-line advice
// after them; the first line is the diagnosis and the rest is a tutorial.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "error:"))
	s = strings.TrimSpace(strings.TrimPrefix(s, "Error from server"))
	return strings.TrimSpace(strings.TrimPrefix(s, ":"))
}

// getJSON runs a `kubectl get -o json` and decodes the list it returns.
func getJSON(ctx context.Context, s selection, kind string, out any) *view.Error {
	raw, verr := run(ctx, s.args("get", kind, "-o", "json")...)
	if verr != nil {
		return verr
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return view.Errorf("kube.unreadable",
			"kubectl's answer for %s could not be read: %v", kind, err)
	}
	return nil
}

// list is the shape every `kubectl get -o json` returns.
type list[T any] struct {
	Items []T `json:"items"`
}

// meta is the part of every object this plugin reads.
type meta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	CreationTimestamp time.Time         `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels,omitempty"`
}

// age renders a creation timestamp the way kubectl does.
func age(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
