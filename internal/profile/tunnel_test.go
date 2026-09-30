package profile

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/tunnel"
	"github.com/this-is-tobi/rta/pkg/plugin"
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
	got := endpointValues(tunnelCap(), endpointAt("127.0.0.1", 54321), false)
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

// EndpointURL answers http by default and https only when the connection
// says the far side of the forward speaks TLS on its own — config.
// Connection.TunnelTLS's doc comment has the reasoning; this is the
// resulting choice made concrete.
func TestEndpointURLIsHTTPSOnlyWhenTheConnectionSaysTheFarSideSpeaksTLS(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tunnelTLS bool
		want      string
	}{
		{"default: http, the tunnel's own hop", false, "http://127.0.0.1:54321"},
		{"tunnelTLS: true, the destination terminates TLS itself", true, "https://127.0.0.1:54321"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := endpointValues(tunnelCap(), endpointAt("127.0.0.1", 54321), tc.tunnelTLS)
			if got["url"] != tc.want {
				t.Errorf("url = %v, want %q", got["url"], tc.want)
			}
		})
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
			if got := endpointValues(c, endpointAt("127.0.0.1", 1), false); got[tc.field.Name] != tc.want {
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
	if got := endpointValues(c, endpointAt("127.0.0.1", 1), false); len(got) != 0 {
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
	// Matched at the end of the argv rather than surrounded by spaces: the
	// name is the last argument because a `--` separates it from the flags.
	if !strings.HasSuffix(got[0], " one") || !strings.HasSuffix(got[1], " two") {
		t.Errorf("Secrets were not read in a stable order: %v", got)
	}
	// The separator itself, which is what stops a Secret named like a flag
	// from being read as one.
	for _, call := range got {
		if !strings.Contains(call, " -- ") {
			t.Errorf("the Secret name was not separated from the flags: %q", call)
		}
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
	got, via, closeTunnel, verr := Dial(context.Background(), "homelab", conn, tunnelCap(), nil, plugin.SurfaceCLI)
	if verr != nil {
		t.Fatalf("dial: %v", verr)
	}
	defer closeTunnel()
	if via != plugin.TunnelKube {
		t.Errorf("a kube forward reports tunnel %q", via)
	}

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

	got, via, closeTunnel, verr := Dial(context.Background(), "base", config.Connection{}, tunnelCap(), nil, plugin.SurfaceCLI)
	if via != plugin.TunnelNone {
		t.Errorf("a connection with no coordinate reports tunnel %q", via)
	}
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

	got, via, closeTunnel, verr := Dial(context.Background(), "homelab", config.Connection{Kube: kubeCoord}, tunnelCap(), nil, plugin.SurfaceCLI)
	if via != plugin.TunnelNone {
		t.Errorf("a forward that never opened reports tunnel %q", via)
	}
	defer closeTunnel()
	if verr == nil {
		t.Fatalf("a failed forward resolved, filling %v", got)
	}
	if got != nil {
		t.Errorf("a failed forward filled inputs anyway: %v", got)
	}
}

// `tunnelTLS: true` states something about the far side of a forward that
// does not exist, caught before the call that needs it and by both the
// report and the resolver — the same twin-hunt
// TestBothTunnelsAtOnceAreRefusedEverywhere runs for the kube/ssh rule.
func TestTLSWithoutATunnelIsRefusedByBothCheckAndLookup(t *testing.T) {
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        tunnelTLS: true
`)
	reg := tunnelledRegistry(t)
	_, verr := Lookup(cfg, tunnelCap(), "homelab", reg)
	if verr == nil {
		t.Fatal("tunnelTLS: true with no kube: or ssh: resolved — TLS to what forward?")
	}
	if verr.Code != "core.profile.tunnel" {
		t.Errorf("code = %s, want core.profile.tunnel", verr.Code)
	}
	if !strings.Contains(verr.Message, "states `tunnelTLS: true`") {
		t.Errorf("message = %q, want it to name the statement", verr.Message)
	}
	found := false
	for _, p := range Check(cfg, reg) {
		if strings.Contains(p.Reason, "states `tunnelTLS: true`") {
			found = true
		}
	}
	if !found {
		t.Error("Check does not report what Lookup refuses")
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

// A caller who names the endpoint connects directly, and no forward is
// opened: the coordinate may be wrong, and typing a host is the way past it.
func TestDialSkipsTheForwardWhenTheCallerNamedTheEndpoint(t *testing.T) {
	conn := config.Connection{Kube: "homelab/databases/svc/postgres:5432"}
	got, via, closeTunnel, verr := Dial(context.Background(), "homelab", conn, tunnelCap(), map[string]any{"host": "db.direct.internal"}, plugin.SurfaceCLI)
	if via != plugin.TunnelNone {
		t.Errorf("a caller-named endpoint reports tunnel %q, and no forward was opened", via)
	}
	closeTunnel()
	if verr != nil {
		t.Fatalf("a named endpoint must not try the forward: %s", verr.Message)
	}
	if len(got) != 0 {
		t.Errorf("Dial filled %v under a caller-named endpoint; it must fill nothing", got)
	}
}

// tlsShaped is a connection plugin's declaration in the shape of a real one:
// the endpoint inputs it names its server with, and its TLS-role input as that
// plugin spells it.
type tlsShaped struct {
	name  string
	cap   plugin.Capability
	tls   string // the TLS-role input
	off   any    // what the forward fills it with
	where string // the endpoint inputs, as the CLI names them
}

func tlsShapes() []tlsShaped {
	hostPort := []plugin.Field{
		{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true,
			Endpoint: plugin.EndpointHost},
		{Name: "port", Type: plugin.Int, Default: 5432, Config: "port", Local: true,
			Endpoint: plugin.EndpointPort},
	}
	pg := plugin.Capability{ID: "pg.status", Summary: "status", Safety: plugin.Read, Run: run,
		Inputs: append(slices.Clone(hostPort), plugin.Field{Name: "sslmode", Type: plugin.String,
			Default: "prefer", Config: "sslmode", Local: true, Endpoint: plugin.EndpointTLS,
			Options: []string{"disable", "prefer", "require", "verify-ca", "verify-full"}})}
	mysql := plugin.Capability{ID: "mysql.status", Summary: "status", Safety: plugin.Read, Run: run,
		Inputs: append(slices.Clone(hostPort), plugin.Field{Name: "tls", Type: plugin.String,
			Default: "preferred", Config: "tls", Local: true, Endpoint: plugin.EndpointTLS,
			Options: []string{"false", "preferred", "true", "skip-verify", "verify-ca"}})}
	redis := plugin.Capability{ID: "redis.overview", Summary: "overview", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "address", Type: plugin.String, Default: "127.0.0.1:6379", Config: "address",
				Local: true, Endpoint: plugin.EndpointAddress},
			{Name: "tls", Type: plugin.Bool, Default: false, Config: "tls", Local: true,
				Endpoint: plugin.EndpointTLS},
		}}
	return []tlsShaped{
		{"pg", pg, "sslmode", "disable", "--host and --port"},
		{"mysql", mysql, "tls", "false", "--host and --port"},
		{"redis", redis, "tls", false, "--address"},
	}
}

// A TLS switch names no destination: given as the value the forward fills it
// with, the call still goes through the forward, where it used to open none
// and go with the profile's credential to the config's host or localhost.
func TestATLSSwitchTheForwardAgreesWithStillGoesThroughIt(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	fakeKubectl(t, fmt.Sprintf(
		"echo 'Forwarding from 127.0.0.1:%d -> 5432'\nwhile true; do sleep 1; done\n", port))

	for _, sh := range tlsShapes() {
		given := []any{sh.off}
		if s, ok := sh.off.(string); ok {
			// Resolve takes an option in any case, so the forward does too.
			given = append(given, strings.ToUpper(s))
		}
		for _, v := range given {
			got, via, closeTunnel, verr := Dial(context.Background(), "homelab",
				config.Connection{Kube: kubeCoord}, sh.cap, map[string]any{sh.tls: v}, plugin.SurfaceCLI)
			closeTunnel()
			if verr != nil {
				t.Errorf("%s --%s %v: refused: %s", sh.name, sh.tls, v, verr.Message)
				continue
			}
			if via != plugin.TunnelKube {
				t.Errorf("%s --%s %v: tunnel %q, want the forward", sh.name, sh.tls, v, via)
			}
			if got["host"] != "127.0.0.1" && got["address"] != fmt.Sprintf("127.0.0.1:%d", port) {
				t.Errorf("%s --%s %v: filled %v, want the forward's address", sh.name, sh.tls, v, got)
			}
		}
	}
}

// A TLS switch asking for what a forward does not carry is refused, before
// any forward is opened, and never taken as the way to somewhere else — it
// used to open none and go with the profile's password to localhost, where
// under a mode that verifies nothing whatever listens receives it. Nor is it
// dropped: turning off TLS somebody asked for is the downgrade the input is
// Local to prevent. The refusal names the forward and both ways on.
func TestATLSSwitchTheForwardCannotCarryIsRefusedBeforeItOpens(t *testing.T) {
	asked := map[string][]any{
		"pg":    {"require", "prefer", "verify-full", ""},
		"mysql": {"true", "preferred", "skip-verify"},
		"redis": {true},
	}
	for _, sh := range tlsShapes() {
		for _, v := range asked[sh.name] {
			for _, conn := range []config.Connection{{Kube: kubeCoord}, {SSH: sshTarget}} {
				calls := fakeKubectl(t, "echo 'a forward that must not be opened' >&2\nexit 1\n")
				got, via, closeTunnel, verr := Dial(context.Background(), "homelab", conn, sh.cap,
					map[string]any{sh.tls: v}, plugin.SurfaceCLI)
				closeTunnel()
				label := fmt.Sprintf("%s %s: %s=%v", sh.name, conn.TunnelKey(), sh.tls, v)
				if verr == nil {
					t.Errorf("%s: resolved (tunnel %q, filled %v), want a refusal", label, via, got)
					continue
				}
				if verr.Code != "core.profile.tls.forward" {
					t.Errorf("%s: code %s, want core.profile.tls.forward (%s)", label, verr.Code, verr.Message)
				}
				if len(got) != 0 || via != plugin.TunnelNone {
					t.Errorf("%s: a refused call filled %v through tunnel %q", label, got, via)
				}
				if c := calls(); len(c) != 0 {
					t.Errorf("%s: kubectl ran before the refusal: %v", label, c)
				}
				if !strings.Contains(verr.Message, "`"+conn.TunnelKey()+":`") ||
					!strings.Contains(verr.Message, `"homelab"`) {
					t.Errorf("%s: message %q does not name the forward", label, verr.Message)
				}
				if !strings.Contains(verr.Hint, sh.where+" as well to connect directly") {
					t.Errorf("%s: hint %q does not give %s as the way to the server itself",
						label, verr.Hint, sh.where)
				}
				if !strings.Contains(verr.Hint, "without --"+sh.tls+" goes through the forward") {
					t.Errorf("%s: hint %q does not give the way through the forward", label, verr.Hint)
				}
			}
		}
	}
}

// The refusal is spelled for the surface the call came from: a TUI picker
// always holds a choice, so its way through is the value the forward sets,
// and its way to the server is boxes, not flags.
func TestATLSSwitchRefusalIsSpelledForItsSurface(t *testing.T) {
	fakeKubectl(t, "exit 1\n")
	for _, sh := range tlsShapes() {
		v := any("require")
		if sh.name != "pg" {
			v = true
		}
		_, _, closeTunnel, verr := Dial(context.Background(), "homelab", config.Connection{Kube: kubeCoord},
			sh.cap, map[string]any{sh.tls: v}, plugin.SurfaceTUI)
		closeTunnel()
		if verr == nil {
			t.Fatalf("%s: resolved, want a refusal", sh.name)
		}
		box := "the " + sh.tls + " box set to "
		if !strings.HasPrefix(verr.Message, box) {
			t.Errorf("%s: message %q does not name the box", sh.name, verr.Message)
		}
		if !strings.Contains(verr.Hint, "a call with "+box+fmt.Sprint(sh.off)+" goes through the forward") {
			t.Errorf("%s: hint %q does not give the picker's value the forward takes", sh.name, verr.Hint)
		}
		boxes := "the host and port boxes"
		if sh.name == "redis" {
			boxes = "the address box"
		}
		if !strings.Contains(verr.Hint, "fill "+boxes+" as well to connect directly") || strings.Contains(verr.Hint, "--") {
			t.Errorf("%s: hint %q is not spelled for a form", sh.name, verr.Hint)
		}
	}
}
