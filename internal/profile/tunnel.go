package profile

import (
	"context"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/tunnel"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// This file is the half of a profile that does something rather than states
// something: opening the forward a `kube:` coordinate names, and reading the
// credentials a `kube:` secret reference names.
//
// It is the only place in internal/profile that spawns a process or touches a
// cluster, which is why it is a file of its own. Everything else here resolves
// a connection out of a config file and could run on a keystroke; nothing in
// this file may, and Bind's doc comment says so at length.

// endpointValues renders a resolved endpoint into the inputs c declared for
// it (plugin.Field.Endpoint).
//
// **Gated on ProfileFillable, not only on the declaration.** Endpoint is the
// first thing a *plugin* declares that the host acts on, and
// pkg/plugin/profile.go's whole argument is that a plugin must not be able to
// widen what a profile reaches — "a plugin that could mark its own inputs
// profile-fillable could mark the one that names a file". Registration already
// requires a Config key, which implies fillable; asking again here means the
// two rules cannot drift apart, and it is the same gate that refuses a Path
// and refuses the capability's Scope input.
//
// A role the host does not recognise fills nothing. That is the same answer
// wire.EndpointRoleFromProto gives for a role from a newer contract, and it
// leaves the input at whatever configuration or the plugin's own default says.
func endpointValues(c plugin.Capability, ep tunnel.Endpoint, tunnelTLS bool) map[string]any {
	out := map[string]any{}
	addr := net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port))
	for _, f := range c.Inputs {
		if f.Endpoint == plugin.EndpointNone || !plugin.ProfileFillable(c, f) {
			continue
		}
		switch f.Endpoint {
		case plugin.EndpointHost:
			out[f.Name] = ep.Host
		case plugin.EndpointPort:
			out[f.Name] = ep.Port
		case plugin.EndpointAddress:
			out[f.Name] = addr
		case plugin.EndpointURL:
			// http by default: the forward is loopback and the hop that
			// leaves the machine is already inside the API server's TLS.
			// https only when the connection's own `tunnelTLS: true` says
			// the far end terminates TLS itself — config.Connection.
			// TunnelTLS's doc comment has the reasoning; a port-forward
			// carries whatever the destination socket speaks unchanged, so
			// the host cannot infer this from the forward alone the way it
			// infers http.
			scheme := "http"
			if tunnelTLS {
				scheme = "https"
			}
			out[f.Name] = scheme + "://" + addr
		case plugin.EndpointTLS:
			// The host owns this, because only the host knows a forward is in
			// the path — see plugin.EndpointTLS. Nil when the declaration
			// offers no way to say "off", which registration refuses, so this
			// is belt and braces rather than a live branch.
			if v := plugin.TLSOffValue(f); v != nil {
				out[f.Name] = v
			}
		}
	}
	return out
}

// target is the tunnel a connection states — either scheme — in the shape
// internal/tunnel takes.
//
// Two shapes for one fact because the first design wrote `targets:` with a
// `secret:` and a `from:` mapping, and a profile replaced that block, whose
// `secrets:` maps each input onto a `<scheme>:<ref>` — one grammar for `kv:`
// and `kube:` rather than one per source. internal/tunnel still speaks the
// first, so the translation happens here and not by rewriting a package that
// has been driven against a real cluster.
func target(conn config.Connection) tunnel.Target {
	return tunnel.Target{Kube: conn.Kube, SSH: conn.SSH}
}

// kubeSecrets reads every credential this connection references with `kube:`,
// keyed by the input it fills.
//
// **One read per distinct Secret, not one per input.** A connection mapping
// `user`, `password` and `database` onto three keys of one Secret is the
// ordinary case, and three `kubectl get secret` calls would be three cluster
// round trips and three chances to see the Secret mid-rotation — assembling a
// username from before a rotation with a password from after. internal/tunnel
// already refuses to do that within one Secret; grouping here is the same rule
// one level up.
//
// Sorted by secret name, so a connection with two unreadable Secrets names the
// same one twice running and a rerun is about the cluster rather than about
// map order.
func kubeSecrets(ctx context.Context, name string, conn config.Connection) (map[string]string, *view.Error) {
	// input -> key, grouped by the secret holding it.
	from := map[string]map[string]string{}
	for _, ref := range conn.SecretRefs() {
		if ref.Scheme != "kube" {
			continue
		}
		secret, key, ok := strings.Cut(ref.Ref, "/")
		// A leading dash is refused here rather than only escaped in argv:
		// the Secret name is passed to kubectl as a positional, and the
		// message is better aimed at the profile than at kubectl.
		if strings.HasPrefix(secret, "-") || strings.HasPrefix(key, "-") {
			return nil, view.Errorf("core.profile.secret.malformed",
				"profile %q resolves %s through kube:%s, and a name may not begin with -",
				name, ref.Input, ref.Ref)
		}
		if !ok || secret == "" || key == "" {
			return nil, view.Errorf("core.profile.secret.malformed",
				"profile %q resolves %s through kube:%s, which is not <secret>/<key>",
				name, ref.Input, ref.Ref).
				WithHint("write it as `kube:postgres-creds/password` — the Secret is read from " +
					"the namespace this connection's `kube:` coordinate already names")
		}
		if from[secret] == nil {
			from[secret] = map[string]string{}
		}
		from[secret][ref.Input] = key
	}
	if len(from) == 0 {
		return nil, nil
	}
	// A credential in a cluster needs the coordinate that says which cluster.
	// Refused rather than guessed: there is no default namespace to fall back
	// to that would not be somebody else's.
	if conn.Kube == "" && conn.SecretsFrom == "" {
		hint := "name where to read it: `secrets-from: <context>/<namespace>` for a connection " +
			"that reaches its service directly, or `kube: <context>/<namespace>/svc/<name>:<port>` " +
			"when the call also needs a forward — or use `kv:` for a local entry"
		if conn.SSH != "" {
			// checkSecretRefs' words for the same case, kept identical: an
			// `ssh:` tunnel reaches a TCP port, not an apiserver. `secrets-from:`
			// is still open to it, because reading a Secret is a separate
			// cluster call that has nothing to do with the forward.
			hint = "an `ssh:` tunnel cannot read a Kubernetes Secret — add " +
				"`secrets-from: <context>/<namespace>` to say which cluster holds it, " +
				"or use `kv:` for a local entry"
		}
		return nil, view.Errorf("core.profile.secret.nocluster",
			"profile %q reads a credential from a cluster and does not say which", name).
			WithHint(hint)
	}
	secrets := make([]string, 0, len(from))
	for s := range from {
		secrets = append(secrets, s)
	}
	sort.Strings(secrets)

	out := map[string]string{}
	for _, secret := range secrets {
		// The read halves alone, never target(conn): the Secret is read
		// through a cluster reference, and by the time this runs the nocluster
		// refusal above has guaranteed one of the two is set. Carrying both is
		// not carrying the forward — Secrets opens nothing, it uses whichever
		// of these states the context and namespace.
		t := tunnel.Target{Kube: conn.Kube, SecretsFrom: conn.SecretsFrom}
		t.Secret, t.From = secret, from[secret]
		filled, verr := tunnel.Secrets(ctx, name, t)
		if verr != nil {
			return nil, verr
		}
		for input, value := range filled {
			out[input] = value
		}
	}
	return out, nil
}
