package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A fixture shaped like `kubectl config view --raw -o json` really is: two
// contexts on two clusters, one with a real CA, one insecure (a kind/local
// cluster's own common shape) — and, critically, a `users` section carrying
// what stands in for the operator's own ambient client certificate. Nothing
// in this file's assertions ever names that value directly; they assert its
// absence instead, so a future change to rawClusterConfig that adds a users
// field and starts reading it would have to also start failing this test to
// leak it — not the other way around.
const rawConfigFixture = `{
  "current-context": "prod",
  "contexts": [
    {"name": "prod", "context": {"cluster": "prod-cluster", "user": "operator"}},
    {"name": "kind", "context": {"cluster": "kind-cluster", "user": "kind-operator"}}
  ],
  "clusters": [
    {"name": "prod-cluster", "cluster": {"server": "https://prod.example.com:6443", "certificate-authority-data": "LS0tLS1CRUdJTi1DQS1EQVRB"}},
    {"name": "kind-cluster", "cluster": {"server": "https://127.0.0.1:6443", "insecure-skip-tls-verify": true}}
  ],
  "users": [
    {"name": "operator", "user": {"client-certificate-data": "THE-OPERATORS-OWN-AMBIENT-CREDENTIAL", "client-key-data": "THE-OPERATORS-OWN-PRIVATE-KEY"}},
    {"name": "kind-operator", "user": {"token": "the-operators-kind-cluster-token"}}
  ]
}`

// The values above that must never appear in anything this file assembles.
var operatorOwnCredentials = []string{
	"THE-OPERATORS-OWN-AMBIENT-CREDENTIAL",
	"THE-OPERATORS-OWN-PRIVATE-KEY",
	"the-operators-kind-cluster-token",
	"client-certificate-data",
	"client-key-data",
}

// withFixtureKubectl points kubectlBin at a tiny shell script that ignores
// its arguments and prints stdout — enough to exercise the run()/runStdin()
// plumbing without a real cluster, matching how a `kind`-backed test would
// see kubectl behave for a `config view --raw` call.
func withFixtureKubectl(t *testing.T, stdout string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "kubectl")
	body := "#!/bin/sh\ncat <<'RTA_FIXTURE_EOF'\n" + stdout + "\nRTA_FIXTURE_EOF\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kubectlBin
	kubectlBin = script
	t.Cleanup(func() { kubectlBin = orig })
}

func TestRawConfigNeverExposesTheUsersSection(t *testing.T) {
	// Decoded directly, not through the fake-kubectl plumbing above: the
	// property under test is about the *type*, not the process boundary —
	// rawClusterConfig must be structurally incapable of holding a `users`
	// value, so nothing downstream of it ever could either.
	var cfg rawClusterConfig
	if err := json.Unmarshal([]byte(rawConfigFixture), &cfg); err != nil {
		t.Fatal(err)
	}
	reencoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, cred := range operatorOwnCredentials {
		if strings.Contains(string(reencoded), cred) {
			t.Errorf("rawClusterConfig retained operator credential material: %q found in %s", cred, reencoded)
		}
	}
}

func TestAssembleKubeconfigContainsOnlyTheMintedIdentity(t *testing.T) {
	var cfg rawClusterConfig
	if err := json.Unmarshal([]byte(rawConfigFixture), &cfg); err != nil {
		t.Fatal(err)
	}
	coords, verr := coordinatesFor(cfg, selection{Context: "prod"})
	if verr != nil {
		t.Fatal(verr)
	}
	out, verr := assembleKubeconfig(coords, "agent-a1b2", "team-prod", "minted-bearer-token")
	if verr != nil {
		t.Fatal(verr)
	}

	for _, cred := range operatorOwnCredentials {
		if strings.Contains(out, cred) {
			t.Errorf("assembled kubeconfig leaked the operator's own credential material (%q):\n%s", cred, out)
		}
	}
	for _, want := range []string{
		"https://prod.example.com:6443",
		"LS0tLS1CRUdJTi1DQS1EQVRB",
		"minted-bearer-token",
		"team-prod",
		"agent-a1b2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("assembled kubeconfig missing %q:\n%s", want, out)
		}
	}
}

// The insecure/kind case: no certificate-authority-data to carry, and the
// assembled config must say so explicitly rather than embed an empty CA
// field a client would try and fail to parse as one.
func TestAssembleKubeconfigCarriesInsecureSkipVerifyWhenThereIsNoCA(t *testing.T) {
	var cfg rawClusterConfig
	if err := json.Unmarshal([]byte(rawConfigFixture), &cfg); err != nil {
		t.Fatal(err)
	}
	coords, verr := coordinatesFor(cfg, selection{Context: "kind"})
	if verr != nil {
		t.Fatal(verr)
	}
	out, verr := assembleKubeconfig(coords, "agent-kind", "default", "tok")
	if verr != nil {
		t.Fatal(verr)
	}
	if !strings.Contains(out, "insecure-skip-tls-verify: true") {
		t.Errorf("expected insecure-skip-tls-verify: true for a CA-less cluster:\n%s", out)
	}
	if strings.Contains(out, "certificate-authority-data") {
		t.Errorf("no CA was available, but the assembled config names one anyway:\n%s", out)
	}
}

func TestCoordinatesForFallsBackToCurrentContext(t *testing.T) {
	var cfg rawClusterConfig
	if err := json.Unmarshal([]byte(rawConfigFixture), &cfg); err != nil {
		t.Fatal(err)
	}
	coords, verr := coordinatesFor(cfg, selection{})
	if verr != nil {
		t.Fatal(verr)
	}
	if coords.server != "https://prod.example.com:6443" {
		t.Errorf("coordinates = %+v, want the current-context's cluster (prod)", coords)
	}
}

func TestCoordinatesForUnknownContextIsCoded(t *testing.T) {
	var cfg rawClusterConfig
	if err := json.Unmarshal([]byte(rawConfigFixture), &cfg); err != nil {
		t.Fatal(err)
	}
	_, verr := coordinatesFor(cfg, selection{Context: "does-not-exist"})
	if verr == nil || verr.Code != "kube.context.unknown" {
		t.Errorf("want kube.context.unknown, got %+v", verr)
	}
}

// tokenExpiry reads a JWT's exp claim without verifying its signature — this
// is provision reading back its own freshly-minted token for display, not
// trusting an untrusted one for an authorization decision.
func TestTokenExpiryReadsTheExpClaim(t *testing.T) {
	exp := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	payload, _ := json.Marshal(map[string]int64{"exp": exp.Unix()})
	token := "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	got, ok := tokenExpiry(token)
	if !ok {
		t.Fatal("tokenExpiry did not decode a well-formed JWT")
	}
	if !got.Equal(exp) {
		t.Errorf("tokenExpiry = %v, want %v", got, exp)
	}
}

func TestTokenExpiryRejectsAMalformedToken(t *testing.T) {
	for _, bad := range []string{"", "not-a-jwt", "a.b", "a.b.c.d", "a." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".c"} {
		if _, ok := tokenExpiry(bad); ok {
			t.Errorf("tokenExpiry(%q) reported success on malformed input", bad)
		}
	}
}

func TestReadRawClusterConfigUsesRawAndNoContextFilter(t *testing.T) {
	withFixtureKubectl(t, rawConfigFixture)
	cfg, verr := readRawClusterConfig(context.Background())
	if verr != nil {
		t.Fatal(verr)
	}
	if len(cfg.Clusters) != 2 || len(cfg.Contexts) != 2 {
		t.Errorf("readRawClusterConfig returned %+v, want the whole file's contexts/clusters", cfg)
	}
}
