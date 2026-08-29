package profile

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/tunnel"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// fakeKubectl puts a script called kubectl at the front of $PATH.
//
// internal/tunnel resolves the binary by name at call time, so this needs no
// hook in the code under test — which is the point. A test-only exported
// setter in a package that spawns processes is a seam somebody eventually
// uses in production, and this reaches the same code through the same
// exec.LookPath every real run does.
//
// The script appends its arguments to a log file, so a test can assert
// how many times the cluster was reached and with what — "one call per Secret,
// not one per key" is a claim about invocations that no return value shows.
func fakeKubectl(t *testing.T, body string) (calls func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" + body
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		raw, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(raw)), "\n")
	}
}

// secretJSON is what `kubectl get secret -o json` prints, values base64 as the
// API returns them.
func secretJSON(data map[string]string) string {
	var b strings.Builder
	b.WriteString(`{"data":{`)
	first := true
	for k, v := range data {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, "%q:%q", k, base64.StdEncoding.EncodeToString([]byte(v)))
	}
	b.WriteString("}}")
	return b.String()
}

const kubeCoord = "homelab/databases/svc/postgres:5432"

// tunnelCap declares one input per endpoint role, so a single fixture proves
// every role fills the input that claimed it and no other.
//
// Not a realistic plugin — no plugin wants a host, a port, an address and a
// URL at once — and that is deliberate: the alternative is four fixtures whose
// differences are what the assertions are really about.
func tunnelCap() plugin.Capability {
	return plugin.Capability{
		ID: "pg.query", Summary: "query", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Config: "host", Local: true,
				Endpoint: plugin.EndpointHost},
			{Name: "port", Type: plugin.Int, Config: "port", Local: true,
				Endpoint: plugin.EndpointPort},
			{Name: "addr", Type: plugin.String, Config: "addr", Local: true,
				Endpoint: plugin.EndpointAddress},
			{Name: "url", Type: plugin.String, Config: "url", Local: true,
				Endpoint: plugin.EndpointURL},
			{Name: "sslmode", Type: plugin.String, Config: "sslmode", Local: true,
				Options:  []string{"disable", "prefer", "require"},
				Endpoint: plugin.EndpointTLS},
			// One config key with no endpoint role, because a `set:` beside a
			// coordinate is only legal for keys the forward does not fill
			// (checkSet), and fixtures that pair them need something to state.
			{Name: "database", Type: plugin.String, Config: "database", Local: true},
			// And a credential, so a fixture pairing the coordinate with a
			// `secrets:` mapping has a legal target — checkSecretRefs refuses
			// one aimed at an input nobody declares.
			{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
			{Name: "sql", Type: plugin.String},
		},
	}
}

// tunnelledRegistry registers tunnelCap under pg, for the tests that state a
// coordinate and need it to mean something: a `kube:` line against a plugin
// with no endpoint role is itself a refusal now, so a fixture without roles
// can no longer stand in for "a valid cluster connection".
func tunnelledRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "pg", Summary: "pg", Capabilities: []plugin.Capability{tunnelCap()},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// Every role fills the input that declared it, rendered the way that role says.
//
// The renderings are the whole contract between the host and a plugin that
// opted in: a port that arrived as the string "54321" is a port a handler's
// req.Int reads as 0, and a URL that arrived without a scheme is a URL nothing
// dials.
func TestEachEndpointRoleFillsTheInputThatDeclaredIt(t *testing.T) {
	got := endpointValues(tunnelCap(), endpointAt("127.0.0.1", 54321))
	want := map[string]any{
		"host":    "127.0.0.1",
		"port":    54321,
		"addr":    "127.0.0.1:54321",
		"url":     "http://127.0.0.1:54321",
		"sslmode": "disable",
	}
	for input, expect := range want {
		if got[input] != expect {
			t.Errorf("%s = %#v, want %#v", input, got[input], expect)
		}
	}
	// And nothing else. An input the plugin did not mark is an input the
	// operator's own configuration or the caller answers, and a tunnel that
	// wrote into one would be overriding a value nobody asked it to.
	if _, wrote := got["sql"]; wrote {
		t.Error("a tunnel filled an input that declared no endpoint role")
	}
	if len(got) != len(want) {
		t.Errorf("filled %d inputs, want %d: %v", len(got), len(want), got)
	}
}

// The TLS role turns transport security off, in the plugin's own vocabulary.
//
// Measured rather than argued: PostgreSQL's `prefer` kills a
// port-forward on the clean disconnect, so a run through a forward that left
// sslmode at its declared default would work once and leave the next call
// staring at "connection refused" on a local port.
func TestTheTLSRoleIsRenderedInThePluginsOwnVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field plugin.Field
		want  any
	}{
		{"libpq spelling", plugin.Field{Name: "sslmode", Type: plugin.String, Config: "sslmode",
			Local: true, Endpoint: plugin.EndpointTLS,
			Options: []string{"disable", "require"}}, "disable"},
		{"another library's spelling", plugin.Field{Name: "tls", Type: plugin.String, Config: "tls",
			Local: true, Endpoint: plugin.EndpointTLS,
			Options: []string{"on", "off"}}, "off"},
		{"a bool", plugin.Field{Name: "secure", Type: plugin.Bool, Config: "secure",
			Local: true, Endpoint: plugin.EndpointTLS}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := plugin.Capability{ID: "x.y", Summary: "y", Safety: plugin.Read, Run: run,
				Inputs: []plugin.Field{tc.field}}
			if got := endpointValues(c, endpointAt("127.0.0.1", 1)); got[tc.field.Name] != tc.want {
				t.Errorf("%s = %#v, want %#v", tc.field.Name, got[tc.field.Name], tc.want)
			}
		})
	}
}

// A declaration the host acts on must not be able to reach further than a
// profile already could.
//
// Registration requires a Config key, which implies ProfileFillable, so this
// combination cannot be loaded — it is built by hand here precisely because the
// gate must hold even if the registration rule is ever relaxed. The rule it
// protects is pkg/plugin/profile.go's: a plugin that could mark its own inputs
// profile-fillable could mark the one that names a file, and Path is refused
// there for exactly that reason.
func TestAnEndpointRoleCannotReachAnInputAProfileMayNotFill(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "y", Safety: plugin.Read, Run: run,
		// A Path: refused by ProfileFillable whatever else it declares.
		Inputs: []plugin.Field{{Name: "out", Type: plugin.Path, Config: "out", Local: true,
			Endpoint: plugin.EndpointAddress}},
	}
	if got := endpointValues(c, endpointAt("127.0.0.1", 1)); len(got) != 0 {
		t.Errorf("a tunnel filled an input a profile may not fill: %v", got)
	}
}

// One cluster read per Secret, not one per input it fills.
//
// N reads is N round trips and N chances to see a Secret mid-rotation, which
// assembles a username from before it with a password from after. The
// invocation log is the only place that shows it: three inputs filled from one
// Secret and three filled from one Secret look identical in the returned map.
func TestKubeSecretsReadsEachSecretOnce(t *testing.T) {
	calls := fakeKubectl(t, "echo '"+secretJSON(map[string]string{
		"username": "app", "password": "hunter2", "dbname": "orders",
	})+"'\n")

	conn := config.Connection{
		Kube: kubeCoord,
		Secrets: map[string]string{
			"user":     "kube:pg-creds/username",
			"password": "kube:pg-creds/password",
			"database": "kube:pg-creds/dbname",
		},
	}
	got, verr := kubeSecrets(context.Background(), "homelab", conn)
	if verr != nil {
		t.Fatalf("kubeSecrets: %v", verr)
	}
	want := map[string]string{"user": "app", "password": "hunter2", "database": "orders"}
	for input, expect := range want {
		if got[input] != expect {
			t.Errorf("%s = %q, want %q", input, got[input], expect)
		}
	}
	if n := len(calls()); n != 1 {
		t.Errorf("read the cluster %d times for one Secret: %v", n, calls())
	}
}

// Two Secrets are two reads, and the namespace is the coordinate's own.
//
// The namespace matters more than it looks: letting a reference reach into
// another one would turn a coordinate for one service into a general-purpose
// cluster reader, and a Secret somewhere else is a different connection.
func TestKubeSecretsReadsEachDistinctSecretInTheCoordinatesNamespace(t *testing.T) {
	calls := fakeKubectl(t, "echo '"+secretJSON(map[string]string{"k": "v"})+"'\n")

	conn := config.Connection{
		Kube: kubeCoord,
		Secrets: map[string]string{
			"user":     "kube:one/k",
			"password": "kube:two/k",
		},
	}
	if _, verr := kubeSecrets(context.Background(), "homelab", conn); verr != nil {
		t.Fatalf("kubeSecrets: %v", verr)
	}
	got := calls()
	if len(got) != 2 {
		t.Fatalf("read the cluster %d times for two Secrets: %v", len(got), got)
	}
	for _, call := range got {
		if !strings.Contains(call, "--namespace databases") {
			t.Errorf("a Secret was read outside the coordinate's namespace: %q", call)
		}
	}
	// Sorted, so a connection with two unreadable Secrets names the same one
	// twice running and a rerun is about the cluster rather than map order.
	if !strings.Contains(got[0], " one ") || !strings.Contains(got[1], " two ") {
		t.Errorf("Secrets were not read in a stable order: %v", got)
	}
}

// A credential in a cluster needs the coordinate that says which cluster, and
// there is no default namespace to fall back to that would not be somebody
// else's.
func TestAKubeSecretWithoutACoordinateIsRefused(t *testing.T) {
	conn := config.Connection{Secrets: map[string]string{"password": "kube:creds/password"}}
	_, verr := kubeSecrets(context.Background(), "homelab", conn)
	if verr == nil {
		t.Fatal("a kube: credential resolved with no cluster named")
	}
	if verr.Code != "core.profile.secret.nocluster" {
		t.Errorf("code = %s", verr.Code)
	}
}

// `kube:` takes <secret>/<key>. A reference missing either half names nothing,
// and guessing which half was meant is not a guess to make about a credential.
func TestAMalformedKubeReferenceIsRefused(t *testing.T) {
	for _, ref := range []string{"kube:creds", "kube:/password", "kube:creds/"} {
		conn := config.Connection{Kube: kubeCoord, Secrets: map[string]string{"password": ref}}
		_, verr := kubeSecrets(context.Background(), "homelab", conn)
		if verr == nil {
			t.Errorf("%q resolved", ref)
			continue
		}
		if verr.Code != "core.profile.secret.malformed" {
			t.Errorf("%q: code = %s", ref, verr.Code)
		}
	}
}

// Dial opens a forward, fills the inputs from it, and the address it reports
// is one that actually answers.
//
// Dialled rather than parsed, which is the same assertion internal/tunnel makes
// about itself: a resolver that returned a number nothing was listening on
// would pass every check made against the string.
func TestDialFillsFromAForwardThatAnswers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	fakeKubectl(t, fmt.Sprintf(
		"echo 'Forwarding from 127.0.0.1:%d -> 5432'\nwhile true; do sleep 1; done\n", port))

	conn := config.Connection{Kube: kubeCoord}
	got, closeTunnel, verr := Dial(context.Background(), "homelab", conn, tunnelCap())
	if verr != nil {
		t.Fatalf("dial: %v", verr)
	}
	defer closeTunnel()

	if got["host"] != "127.0.0.1" || got["port"] != port {
		t.Fatalf("filled %v:%v, want 127.0.0.1:%d", got["host"], got["port"], port)
	}
	conn2, err := net.Dial("tcp", fmt.Sprint(got["addr"]))
	if err != nil {
		t.Fatalf("the address Dial filled in does not answer: %v", err)
	}
	_ = conn2.Close()
}

// A connection naming no cluster opens nothing, fills nothing, and still
// returns a closer.
//
// The closer is the half worth pinning. Every call site defers it on the line
// after the call, before checking the error, so a nil here would panic every
// ordinary run — which is to say every run, since the overwhelming majority of
// connections name no cluster at all.
func TestDialWithoutACoordinateOpensNothingAndStillReturnsACloser(t *testing.T) {
	fakeKubectl(t, "echo 'a kubectl that must never be run' >&2\nexit 1\n")

	got, closeTunnel, verr := Dial(context.Background(), "base", config.Connection{}, tunnelCap())
	if verr != nil {
		t.Fatalf("dial: %v", verr)
	}
	if closeTunnel == nil {
		t.Fatal("the closer is nil, so every call site's deferred close panics")
	}
	closeTunnel()
	if len(got) != 0 {
		t.Errorf("a connection naming no cluster filled %v", got)
	}
}

// A forward that cannot be opened is a refusal, not a call that quietly runs
// against the plugin's own default host.
//
// This is the failure the whole feature exists to remove. An operator who
// wrote a coordinate is asking for a forward; reaching localhost instead —
// which is a live PostgreSQL on plenty of developer machines — is the wrong
// answer delivered confidently.
func TestAForwardThatCannotBeOpenedRefusesRatherThanFallingBack(t *testing.T) {
	fakeKubectl(t, "echo 'Error from server (NotFound): services \"postgres\" not found' >&2\nexit 1\n")

	got, closeTunnel, verr := Dial(context.Background(), "homelab", config.Connection{Kube: kubeCoord}, tunnelCap())
	defer closeTunnel()
	if verr == nil {
		t.Fatalf("a failed forward resolved, filling %v", got)
	}
	if got != nil {
		t.Errorf("a failed forward filled inputs anyway: %v", got)
	}
}

// endpointAt is a tunnel endpoint at a known address.
func endpointAt(host string, port int) tunnel.Endpoint {
	return tunnel.Endpoint{Host: host, Port: port}
}

// A coordinate that is not a coordinate is caught before the call that needs
// it, and by both the report and the path that resolves.
//
// Two copies of a rule is how `rta profile list` once printed "invalid" for a
// profile that connected perfectly well, so the assertion is that Check and
// Lookup agree — not merely that each says something.
func TestAMalformedCoordinateIsCaughtByBothCheckAndLookup(t *testing.T) {
	for _, spec := range []string{
		"homelab/databases/svc/postgres",  // no :port
		"homelab/databases/postgres:5432", // three segments
		"homelab/databases/svc/postgres:0",
		"homelab//svc/postgres:5432",
	} {
		cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        kube: `+spec+`
`)
		problems := Check(cfg, pgRegistry(t))
		_, verr := Lookup(cfg, pgCap(), "homelab", pgRegistry(t))
		switch {
		case len(problems) == 0 && verr == nil:
			t.Errorf("%q was accepted by both, and it is not a coordinate", spec)
		case len(problems) == 0:
			t.Errorf("%q: Lookup refused it and the report calls the profile fine", spec)
		case verr == nil:
			t.Errorf("%q: the report calls it invalid and Lookup resolved it anyway", spec)
		}
	}
	// And one that is a coordinate passes both — against a plugin whose
	// inputs a forward can fill — so the rule rejects typos rather than
	// narrowing what an operator may write.
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        kube: homelab/databases/svc/postgres:5432
`)
	if problems := Check(cfg, tunnelledRegistry(t)); len(problems) != 0 {
		t.Errorf("a well-formed coordinate was reported as a problem: %v", problems)
	}
	if _, verr := Lookup(cfg, tunnelCap(), "homelab", tunnelledRegistry(t)); verr != nil {
		t.Errorf("a well-formed coordinate was refused: %v", verr)
	}
}
