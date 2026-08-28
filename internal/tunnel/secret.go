package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Secrets reads the credentials a target names and returns them keyed by the
// *input* they fill (ADR 0018 §6).
//
// The mapping is the operator's, never the plugin's. That is the same rule
// that made the local-credential environment variable derived rather than
// declared (ADR 0016, D50): a plugin that could name the secret it wanted
// would be a plugin that could name any secret in the namespace and have the
// host fetch it. Here a plugin declares only that it has an input; which
// secret key fills it is written in the operator's own file.
//
// The value reaches the plugin the way every other credential does — over the
// AutoMTLS socket, resolved by the host — so nothing here is written to
// configuration, exported into an environment, or visible in an argv.
func Secrets(ctx context.Context, name string, t Target) (map[string]string, *view.Error) {
	if t.Secret == "" || len(t.From) == 0 {
		return nil, nil
	}
	kctx, ns, _, _, _, verr := parseKube(t.Kube)
	if verr != nil {
		return nil, verr
	}
	if _, err := exec.LookPath(kubectl); err != nil {
		return nil, view.Errorf("tunnel.kubectl.missing",
			"profile %q reads its credentials from a cluster secret and kubectl is not on $PATH", name).
			WithHint("rta shells out rather than linking client-go, so the cluster " +
				"credentials you already have keep working — install kubectl, or " +
				"drop `secret:` and export the credential yourself")
	}

	// One call, not one per key: N invocations is N cluster round trips and N
	// chances to see a secret mid-rotation and assemble a username from
	// before it with a password from after.
	//
	// The namespace is the coordinate's own. A coordinate already names
	// context/namespace, and letting a reference reach into a different one
	// would turn a coordinate for one service into a general-purpose cluster
	// reader — a secret somewhere else is a different connection.
	cmd := exec.CommandContext(ctx, kubectl,
		"--context", kctx, "--namespace", ns,
		"get", "secret", t.Secret, "-o", "json")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, secretFailed(name, ns, t.Secret, stderr.String())
	}

	var payload struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, view.Errorf("tunnel.secret.unreadable",
			"profile %q: could not read secret %q: %v", name, t.Secret, err)
	}

	// Sorted, so a connection that maps three missing keys names the same one
	// twice running and a rerun is about the cluster.
	inputs := make([]string, 0, len(t.From))
	for input := range t.From {
		inputs = append(inputs, input)
	}
	sort.Strings(inputs)

	filled := make(map[string]string, len(t.From))
	for _, input := range inputs {
		key := t.From[input]
		encoded, ok := payload.Data[key]
		if !ok {
			return nil, view.Errorf("tunnel.secret.key.missing",
				"profile %q maps %q to key %q, which secret %q does not have",
				name, input, key, t.Secret).
				WithHint("it has: " + strings.Join(keysOf(payload.Data), ", "))
		}
		// Only the mapped keys are decoded. The rest stay base64 in a buffer
		// that goes out of scope — not a security boundary, but there is no
		// reason to render a credential nobody asked for.
		value, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, view.Errorf("tunnel.secret.undecodable",
				"profile %q: key %q of secret %q is not base64", name, key, t.Secret)
		}
		filled[input] = string(value)
	}
	return filled, nil
}

// secretFailed classifies kubectl's refusal.
//
// Reading a secret and opening a port-forward are different permissions —
// `get` on `secrets` against `create` on `pods/portforward` — and an operator
// commonly has one and not the other. A message that does not say which was
// refused sends them to check the wrong thing.
func secretFailed(name, ns, secret, stderr string) *view.Error {
	s := strings.TrimSpace(stderr)
	one := s
	if i := strings.IndexByte(one, '\n'); i >= 0 {
		one = one[:i]
	}
	switch {
	case strings.Contains(s, "not found"):
		return view.Errorf("tunnel.secret.missing",
			"profile %q names secret %q, which does not exist in namespace %q", name, secret, ns).
			WithHint("`kubectl -n " + ns + " get secrets` lists them")
	case strings.Contains(s, "forbidden") || strings.Contains(s, "Unauthorized"):
		return view.Errorf("tunnel.secret.denied",
			"profile %q: not allowed to read secret %q in namespace %q", name, secret, ns).
			WithHint("this is the cluster refusing, not rta — the verb is `get` on " +
				"`secrets`, which is a different permission from the one port-forward needs")
	case s == "":
		return view.Errorf("tunnel.secret.unreadable",
			"profile %q: kubectl could not read secret %q", name, secret)
	default:
		return view.Errorf("tunnel.secret.unreadable", "%s: %s", name, one).
			WithHint("that message is kubectl's; rta shells out to it so your cluster " +
				"credentials keep working")
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"(no keys)"}
	}
	return out
}
