package main

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk/sdktest"
)

// sdktest is the definition of "a correct plugin" and pg gets no exemption
// from it.
//
// The declaration half needs no database. The dry-run half needs inputs, and
// without them two of pg's four mutating capabilities were never driven at
// all — the suite reported a pass for capabilities it had not run.
func TestConformance(t *testing.T) {
	sdktest.Check(t, Plugin(), sdktest.WithInputs(conformanceInputs))
}

// conformanceInputs points the mutating capabilities at a port nothing is
// listening on, so a dry run that stops being dry fails as a refused
// connection rather than as a query against somebody's database.
func conformanceInputs(dir string) map[string]map[string]any {
	conn := func(m map[string]any) map[string]any {
		m["host"], m["port"] = "127.0.0.1", 1
		m["user"], m["database"] = "conformance", "conformance"
		return m
	}
	// A real plain-SQL fixture, because pg.restore reads the format off the
	// bytes before its dry run says anything — the supported shape per
	// sdktest.WithInputs' own doc. The suite's snapshot is taken after this
	// runs, so a dry run that touched the fixture would still be caught.
	fixture := filepath.Join(dir, "conformance.sql")
	_ = os.WriteFile(fixture, []byte("select 1;\n"), 0o600)
	return map[string]map[string]any{
		"pg.query":      conn(map[string]any{"sql": "select 1"}),
		"pg.table.dump": conn(map[string]any{"table": "public.conformance"}),
		"pg.dump":       conn(map[string]any{"out": filepath.Join(dir, "pg.dump")}),
		"pg.restore":    conn(map[string]any{"file": fixture}),
	}
}

// req builds a resolved request the way the host would, so these test the
// values a handler actually sees rather than a hand-made map.
func req(t *testing.T, values map[string]any) plugin.Request {
	t.Helper()
	c := Plugin().Capabilities[0]
	return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
}

// A password containing '@' or '/' produces a different connection string
// under URL parsing, and the failure is an authentication error naming
// nothing. Key=value with quoting is why this is not built as a URL.
func TestDSNSurvivesAwkwardPasswords(t *testing.T) {
	for _, pw := range []string{"p@ss/word", "with 'quote'", `back\slash`, "sp ace"} {
		got := dsn(req(t, map[string]any{"password": pw, "host": "db.internal"}))
		if !strings.Contains(got, "password=") {
			t.Errorf("password missing for %q: %s", pw, got)
		}
		// The host must still parse as its own field: an unescaped quote
		// would run the password into the next key.
		if !strings.Contains(got, "host='db.internal'") {
			t.Errorf("password %q corrupted the rest of the DSN: %s", pw, got)
		}
	}
}

// An empty password is absent rather than empty: libpq treats `password=”`
// as "authenticate with the empty string", which fails differently from
// "no password offered" on a trust-configured server.
func TestAnEmptyPasswordIsOmitted(t *testing.T) {
	if got := dsn(req(t, map[string]any{})); strings.Contains(got, "password=") {
		t.Errorf("an unset password was sent as empty: %s", got)
	}
}

// sslrootcert rides the DSN exactly where libpq expects to find it — pgx's
// own ParseConfig reads this key directly, which is the whole reason it is
// spelled this and not something rta invented.
func TestDSNCarriesSSLRootCert(t *testing.T) {
	got := dsn(req(t, map[string]any{"sslrootcert": "/etc/rta/pg-ca.crt"}))
	if !strings.Contains(got, "sslrootcert='/etc/rta/pg-ca.crt'") {
		t.Errorf("sslrootcert missing or unquoted: %s", got)
	}
}

// Unset, it is absent rather than empty — the same reason password is: an
// empty sslrootcert is not "no CA named", it is libpq trying to read a file
// called "" and failing in a way that names nothing.
// pgx's own defaults fill passfile with $HOME/.pgpass and load a password
// from it whenever the connection string names none — so omitting the key
// does not mean "no passfile", it means "the operator's own". An operator who
// configured a host and a user and deliberately no password was being
// authenticated with whatever credential their interactive psql keeps for
// that host, and the auth-failure hint told them this could not happen.
//
// Empty rather than absent, and always rather than only when the password is
// blank: an empty passfile fails the open, which is what makes pgconn skip
// the lookup.
func TestTheDSNAlwaysRefusesTheAmbientPassfile(t *testing.T) {
	for _, password := range []string{"", "hunter2"} {
		got := dsn(req(t, map[string]any{
			"host": "db.internal", "port": 5432, "user": "postgres",
			"database": "postgres", "sslmode": "prefer", "password": password,
		}))
		if !strings.Contains(got, "passfile=''") {
			t.Errorf("password=%q: dsn does not refuse the ambient passfile: %s", password, got)
		}
	}
}

func TestAnEmptySSLRootCertIsOmitted(t *testing.T) {
	if got := dsn(req(t, map[string]any{})); strings.Contains(got, "sslrootcert=") {
		t.Errorf("an unset sslrootcert was sent as empty: %s", got)
	}
}

// Every classified failure has to say what to do next. These are the errors
// people stare at without knowing the next move, which is the whole reason
// pg is the plugin that proves the contract.
func TestEveryClassifiedFailureNamesTheNextStep(t *testing.T) {
	r := req(t, map[string]any{"host": "db.internal", "port": 5432, "database": "app"})
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"bad password", &pgconn.PgError{Code: "28P01"}, "pg.auth.failed"},
		{"no such database", &pgconn.PgError{Code: "3D000"}, "pg.database.missing"},
		{"not permitted", &pgconn.PgError{Code: "42501"}, "pg.denied"},
		{"refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "pg.conn.refused"},
		{"unknown host", &net.DNSError{Err: "no such host", Name: "db.internal"}, "pg.host.unknown"},
		{"timed out", context.DeadlineExceeded, "pg.conn.timeout"},
		{"no TLS", errors.New("server does not support SSL"), "pg.tls.unsupported"},
		{"untrusted CA", x509.UnknownAuthorityError{}, "pg.tls.untrusted"},
		{"anything else", errors.New("something unexpected"), "pg.conn.failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verr := classify(tc.err, r)
			if verr.Code != tc.code {
				t.Errorf("code = %q, want %q", verr.Code, tc.code)
			}
			if verr.Hint == "" {
				t.Error("no hint: this is exactly the error somebody is stuck on")
			}
			if verr.Message == "" {
				t.Error("no message")
			}
		})
	}
}

// The auth hint has to name the variable the host actually reads, and that
// name is derived rather than written down — so hardcoding it here would let
// the two drift apart silently.
func TestTheAuthHintNamesTheRealEnvironmentVariable(t *testing.T) {
	verr := classify(&pgconn.PgError{Code: "28P01"}, req(t, nil))
	want := plugin.LocalEnvVar("pg.status", "password")
	if !strings.Contains(verr.Hint, want) {
		t.Errorf("hint %q does not name %s", verr.Hint, want)
	}
}

// A hint that names an rta command must name one this plugin declares.
// pg.status's "no database named X" hint pointed at `rta pg database list`
// before that capability existed, which sends somebody to type something that
// fails differently.
func TestHintsOnlyNameCapabilitiesThatExist(t *testing.T) {
	declared := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		declared[c.ID] = true
	}
	r := req(t, map[string]any{"host": "db.internal", "database": "app"})
	for _, err := range []error{
		&pgconn.PgError{Code: "28P01"}, &pgconn.PgError{Code: "3D000"},
		&pgconn.PgError{Code: "42501"}, context.DeadlineExceeded,
	} {
		hint := classify(err, r).Hint
		// Any `rta pg <words>` in a hint has to name a capability this plugin
		// declares. Other namespaces are deliberately not checked here: a
		// hint may legitimately point at `rta net dns`, and this module cannot
		// see the built-in catalogue.
		if i := strings.Index(hint, "rta pg "); i >= 0 {
			tail := hint[i+len("rta pg "):]
			if j := strings.IndexAny(tail, "`\n"); j >= 0 {
				tail = tail[:j]
			}
			id := "pg." + strings.Join(strings.Fields(tail), ".")
			if !declared[id] {
				t.Errorf("a hint names `rta pg %s`, which would be %s — this plugin declares no such capability:\n  %s",
					tail, id, hint)
			}
		}
	}
}

// The doc comment at the top of main.go is an instruction somebody will
// follow, and the first person to follow it hit "main module does not contain
// package": this is its own module, so the build has to cd into it, and the
// comment said `go build ./plugins/pg` from the root — which cannot work.
//
// Checked rather than proof-read, because a command in a comment is a claim
// like any other and this one was wrong for as long as it existed.
func TestTheBuildInstructionIsRunnable(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	doc, _, _ := strings.Cut(string(src), "\npackage main")
	if !strings.Contains(doc, "cd plugins/pg && go build") {
		t.Error("the build instruction no longer cds into this module, so it cannot work " +
			"from the repository root")
	}
	if strings.Contains(doc, "go build -o ~/.local/bin/rta-plugin-pg ./plugins/pg") {
		t.Error("the build instruction is the one that fails: `go build ./plugins/pg` from the " +
			"root cannot see a separate module")
	}
}

// A dead port-forward is the commonest way a developer's `pg` call fails, and
// the general hint asks the wrong question about it: "is the server up" is
// not a question about a port on this machine. Measured against a real
// CloudNativePG cluster through `kubectl port-forward` — PostgreSQL TLS kills
// the forward on the first clean disconnect, and `psql --sslmode=require`
// kills it identically, so this is the transport rather than rta. What is
// rta's is that `prefer` is the default, so it happens on the first call.
func TestARefusedLoopbackPortBlamesTheForwardNotTheServer(t *testing.T) {
	refused := &net.OpError{Op: "dial", Err: errors.New("connect: connection refused")}

	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		req := plugin.NewRequest(map[string]any{"host": host, "port": 15433}, false, false)
		verr := classify(refused, req)
		if verr.Code != "pg.conn.refused" {
			t.Fatalf("%s: code = %s", host, verr.Code)
		}
		if !strings.Contains(verr.Hint, "port-forward") {
			t.Errorf("%s: hint does not mention a port-forward: %s", host, verr.Hint)
		}
		if !strings.Contains(verr.Hint, "sslmode") {
			t.Errorf("%s: hint does not name the setting that survives: %s", host, verr.Hint)
		}
	}

	// A real host keeps the general hint: "check your port-forward" is noise
	// when the operator typed db.internal.
	req := plugin.NewRequest(map[string]any{"host": "db.internal", "port": 5432}, false, false)
	verr := classify(refused, req)
	if strings.Contains(verr.Hint, "port-forward") {
		t.Errorf("a remote host was told to check a port-forward: %s", verr.Hint)
	}
	if !strings.Contains(verr.Hint, "rta net port db.internal") {
		t.Errorf("the remote hint lost its next command: %s", verr.Hint)
	}
}
