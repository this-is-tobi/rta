package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The kubeconfig-assembly half of kube.serviceaccount.provision, and the one
// piece of this feature that reads a working credential rather than just
// naming one.
//
// context.go's own readConfig deliberately never passes --raw, because
// without it kubectl redacts every credential field — client certificates,
// bearer tokens, exec plugin output — and every other capability in this
// plugin has no business seeing them. provision is the one exception: it
// needs the target cluster's real certificate-authority-data, which only
// --raw returns. That same raw output also contains the operator's own
// ambient credentials, in the kubeconfig's `users` section — so the type
// this file decodes into declares no `users` field at all, on purpose, so
// there is nothing for a bug here to accidentally forward. See
// kubeconfig_test.go, which feeds a fixture containing a `users` section and
// asserts none of it survives into the assembled result.

// rawClusterConfig is `kubectl config view --raw -o json`, cut down to
// exactly the cluster coordinates a minted kubeconfig needs. No `users`
// field — see the package comment above.
type rawClusterConfig struct {
	CurrentContext string `json:"current-context"`
	Contexts       []struct {
		Name    string `json:"name"`
		Context struct {
			Cluster string `json:"cluster"`
		} `json:"context"`
	} `json:"contexts"`
	Clusters []struct {
		Name    string `json:"name"`
		Cluster struct {
			Server                   string `json:"server"`
			CertificateAuthorityData string `json:"certificate-authority-data"`
			InsecureSkipTLSVerify    bool   `json:"insecure-skip-tls-verify,omitempty"`
		} `json:"cluster"`
	} `json:"clusters"`
}

// clusterCoordinates is what a minted kubeconfig needs to name the cluster:
// no credential of the operator's own, just where it is and how to trust it.
type clusterCoordinates struct {
	name     string
	server   string
	caData   string // base64-encoded, already in the form a kubeconfig's certificate-authority-data expects
	insecure bool
}

// readRawClusterConfig reads the whole kubeconfig with --raw, once, the same
// way readConfig reads it without --raw: no --context is passed to the
// `config view` call itself, since the answer is the whole file and the
// caller looks up one context by name afterward.
func readRawClusterConfig(ctx context.Context) (rawClusterConfig, *view.Error) {
	raw, verr := run(ctx, "config", "view", "--raw", "-o", "json")
	if verr != nil {
		return rawClusterConfig{}, verr
	}
	var cfg rawClusterConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return rawClusterConfig{}, view.Errorf("kube.unreadable",
			"this machine's kubeconfig could not be read: %v", err)
	}
	return cfg, nil
}

// coordinatesFor resolves one context (s.Context, or the file's current one
// when empty) to the cluster it points at.
func coordinatesFor(cfg rawClusterConfig, s selection) (clusterCoordinates, *view.Error) {
	name := s.Context
	if name == "" {
		name = cfg.CurrentContext
	}
	if name == "" {
		return clusterCoordinates{}, view.Errorf("kube.context.none",
			"this machine's kubeconfig names no current context").
			WithHint("pass --context, or `rta kube context set <name>` picks one")
	}
	var clusterName string
	found := false
	for _, c := range cfg.Contexts {
		if c.Name == name {
			clusterName = c.Context.Cluster
			found = true
			break
		}
	}
	if !found {
		return clusterCoordinates{}, view.Errorf("kube.context.unknown", "no context named %q", name).
			WithHint("`rta kube context list` shows the contexts this machine has")
	}
	for _, c := range cfg.Clusters {
		if c.Name == clusterName {
			return clusterCoordinates{
				name:     clusterName,
				server:   c.Cluster.Server,
				caData:   c.Cluster.CertificateAuthorityData,
				insecure: c.Cluster.InsecureSkipTLSVerify,
			}, nil
		}
	}
	return clusterCoordinates{}, view.Errorf("kube.unreadable",
		"context %q names cluster %q, which is not in this kubeconfig", name, clusterName)
}

// mintedKubeconfig is the shape assembled for the operator: a working
// kubeconfig for exactly one identity, in exactly one namespace, on exactly
// one cluster. Plain, concrete types throughout rather than something
// generic or clever — this is the one file in this feature that assembles a
// working credential, and it should be obviously correct at a glance, not
// merely correct.
type mintedKubeconfig struct {
	APIVersion     string          `yaml:"apiVersion"`
	Kind           string          `yaml:"kind"`
	CurrentContext string          `yaml:"current-context"`
	Clusters       []kcClusterItem `yaml:"clusters"`
	Contexts       []kcContextItem `yaml:"contexts"`
	Users          []kcUserItem    `yaml:"users"`
}

type kcClusterItem struct {
	Name    string    `yaml:"name"`
	Cluster kcCluster `yaml:"cluster"`
}

type kcCluster struct {
	Server                   string `yaml:"server"`
	CertificateAuthorityData string `yaml:"certificate-authority-data,omitempty"`
	InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify,omitempty"`
}

type kcContextItem struct {
	Name    string    `yaml:"name"`
	Context kcContext `yaml:"context"`
}

type kcContext struct {
	Cluster   string `yaml:"cluster"`
	User      string `yaml:"user"`
	Namespace string `yaml:"namespace"`
}

type kcUserItem struct {
	Name string `yaml:"name"`
	User kcUser `yaml:"user"`
}

type kcUser struct {
	Token string `yaml:"token"`
}

// assembleKubeconfig builds the minted identity's own kubeconfig: coords
// names where and how to trust the cluster, name/namespace/token are the
// identity itself. The context's namespace is set to the ServiceAccount's
// own, so the agent this is handed to never needs -n on a call.
func assembleKubeconfig(coords clusterCoordinates, name, namespace, token string) (string, *view.Error) {
	cfg := mintedKubeconfig{
		APIVersion:     "v1",
		Kind:           "Config",
		CurrentContext: name,
		Clusters: []kcClusterItem{{Name: coords.name, Cluster: kcCluster{
			Server:                   coords.server,
			CertificateAuthorityData: coords.caData,
			InsecureSkipTLSVerify:    coords.insecure,
		}}},
		Contexts: []kcContextItem{{Name: name, Context: kcContext{
			Cluster: coords.name, User: name, Namespace: namespace,
		}}},
		Users: []kcUserItem{{Name: name, User: kcUser{Token: token}}},
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return "", view.Errorf("kube.serviceaccount.assemble", "assembling the kubeconfig: %v", err)
	}
	return string(out), nil
}

// tokenExpiry reads a TokenRequest JWT's own exp claim, so provision can
// report what the API server actually granted rather than echoing back
// --ttl: a cluster's service-account-max-token-expiration silently clamps a
// longer request to its own ceiling, with no error — the token itself is the
// only place that ceiling shows up. No signature verification: this reads
// its own freshly-minted token back, not one from an untrusted source, and
// the claim is being displayed, not trusted for an authorization decision.
func tokenExpiry(token string) (time.Time, bool) {
	parts := splitJWT(token)
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

func splitJWT(token string) []string {
	var parts []string
	start := 0
	for i, c := range token {
		if c == '.' {
			parts = append(parts, token[start:i])
			start = i + 1
		}
	}
	parts = append(parts, token[start:])
	return parts
}
