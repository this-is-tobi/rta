package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

// These combinations must never reach net.Listen, let alone mcp.Serve —
// each is checked and refused before this command builds anything that
// could accept a connection.

func TestHTTPRefusesConsentWithoutOperators(t *testing.T) {
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0", "--consent"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("--http --consent with no --operators was accepted")
	}
	if !strings.Contains(err.Error(), "consent") || !strings.Contains(err.Error(), "--operators") {
		t.Errorf("err = %q, want it to name --consent and the --operators fix", err)
	}
}

// With a roster named, --consent is a control somebody can exercise, so the
// consent gate opens and the next check in line — the missing verifier —
// is what refuses. The roster does not need to exist for this: it is
// loaded after the verifier checks, and what this test pins is only which
// gate answered.
func TestHTTPConsentWithOperatorsClearsTheConsentGate(t *testing.T) {
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0", "--consent",
		"--operators", "does-not-matter-yet", "--operators-url", "https://rta.example.com"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("--http with no verifier was accepted")
	}
	if strings.Contains(err.Error(), "consent") {
		t.Errorf("err = %q, still the consent refusal despite --operators", err)
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("err = %q, want the missing-verifier refusal next in line", err)
	}
}

func TestHTTPRequiresATokenFile(t *testing.T) {
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("--http with no --token-file was accepted")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("err = %q, want it to name the missing verifier", err)
	}
}

// The consent check runs first: a caller who passed --consent with neither
// --operators nor --token-file should never see the token-file message
// stand in for the consent one, since fixing that message's complaint
// would still refuse.
func TestHTTPConsentIsCheckedBeforeTokenFile(t *testing.T) {
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0", "--consent"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "consent") {
		t.Fatalf("err = %v, want the consent refusal even though --token-file is also missing", err)
	}
}

func TestHTTPOIDCIssuerRequiresAudience(t *testing.T) {
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0", "--oidc-issuer", "https://idp.example"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("--oidc-issuer with no --oidc-audience was accepted")
	}
	if !strings.Contains(err.Error(), "oidc-audience") {
		t.Errorf("err = %q, want it to name --oidc-audience", err)
	}
}

func TestHTTPOIDCIssuerRequiresASubject(t *testing.T) {
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0",
		"--oidc-issuer", "https://idp.example", "--oidc-audience", "rta"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("--oidc-issuer with no --oidc-subject was accepted")
	}
	if !strings.Contains(err.Error(), "oidc-subject") {
		t.Errorf("err = %q, want it to name --oidc-subject", err)
	}
}

// Well-formed but pointing nowhere real: discovery must fail as this
// command's own clear error, not several layers down inside net/http.
func TestHTTPOIDCDiscoveryFailureIsRefusedAtStartup(t *testing.T) {
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0",
		"--oidc-issuer", "http://127.0.0.1:1", "--oidc-audience", "rta", "--oidc-subject", "alice"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("an --oidc-issuer with no discovery document was accepted")
	}
	if !strings.Contains(err.Error(), "oidc-issuer") {
		t.Errorf("err = %q, want it to name --oidc-issuer", err)
	}
}

func TestHTTPRefusesAnUnreadableTokenFile(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX permission bits do not apply")
	}
	path := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(path, []byte("alice tok-a-0123456789abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0", "--token-file", path})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("a world-readable --token-file was accepted")
	}
	if !strings.Contains(err.Error(), "weak permissions") {
		t.Errorf("err = %q, want it to name the permission problem", err)
	}
}

// A roster that will not load refuses startup outright — a server that
// silently dropped its operator channel would read as "enabled" to the
// person who passed the flag and as "absent" to everyone else.
func TestHTTPRefusesAGarbledOperatorsFile(t *testing.T) {
	dir := t.TempDir()
	tokens := filepath.Join(dir, "tokens")
	if err := os.WriteFile(tokens, []byte("alice tok-a-0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	roster := filepath.Join(dir, "operators")
	if err := os.WriteFile(roster, []byte("tobi not-a-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0",
		"--token-file", tokens, "--operators", roster,
		"--operators-url", "https://rta.example.com"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("a garbled --operators file was accepted")
	}
	if !strings.Contains(err.Error(), "--operators") {
		t.Errorf("err = %q, want it to name --operators", err)
	}
}

// Without the canonical URL there is nothing to verify signatures against
// that a relay could not forge, so the channel refuses to exist rather than
// exist unbound.
func TestHTTPOperatorsRequiresACanonicalURL(t *testing.T) {
	dir := t.TempDir()
	tokens := filepath.Join(dir, "tokens")
	if err := os.WriteFile(tokens, []byte("alice tok-a-0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	roster := filepath.Join(dir, "operators")
	if err := os.WriteFile(roster, []byte("# empty on purpose\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--as", "probe", "--http", "127.0.0.1:0",
		"--token-file", tokens, "--operators", roster})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--operators-url") {
		t.Fatalf("err = %v, want the missing --operators-url named", err)
	}
}

// Every refusal serve makes before it listens is coded, and rendered in the
// format asked for: a combination of its flags that cannot work as typed is
// core.usage and exits 2, and what failed on this machine carries a code of
// its own and exits 1. They were the last plain errors on the command, so
// `-o json` got a box of prose for the mistakes a person wiring up a server
// makes first.
func TestServeRefusalsAreCodedInTheFormatAskedFor(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX permission bits do not apply")
	}
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	tokens := filepath.Join(dir, "tokens")
	if err := os.WriteFile(tokens, []byte("alice tok-a-0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(dir, "loose")
	if err := os.WriteFile(loose, []byte("alice tok-a-0123456789abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	roster := filepath.Join(dir, "operators")
	if err := os.WriteFile(roster, []byte("# empty on purpose\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	web := []string{"--http", "127.0.0.1:0"}
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"--observe", "127.0.0.1:0"}, CodeUsage},
		{append(web, "--consent"), CodeUsage},
		{web, CodeUsage},
		{append(web, "--oidc-issuer", "https://idp.example"), CodeUsage},
		{append(web, "--oidc-issuer", "https://idp.example", "--oidc-audience", "rta"), CodeUsage},
		{append(web, "--token-file", tokens, "--operators", roster), CodeUsage},
		{append(web, "--token-file", tokens, "--operators", roster,
			"--operators-url", "http://rta.example.com"), CodeUsage},
		{[]string{"--http", "127.0.0.1:99999", "--token-file", tokens}, "core.mcp.listen"},
		{append(web, "--token-file", loose), "core.mcp.tokenfile"},
		{append(web, "--oidc-issuer", "http://127.0.0.1:1", "--oidc-audience", "rta",
			"--oidc-subject", "alice"), "core.mcp.oidc"},
	} {
		cmd := NewRoot(registry.New(), "test")
		cmd.SetArgs(append([]string{"mcp", "serve", "--as", "probe", "-o", "json"}, tc.args...))
		cmd.SetOut(new(strings.Builder))
		cmd.SetErr(new(strings.Builder))
		err := cmd.Execute()
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != tc.code {
			t.Errorf("%v: err = %#v, want %s", tc.args, err, tc.code)
			continue
		}
		if want := map[bool]int{true: 2, false: 1}[tc.code == CodeUsage]; ExitCode(err) != want {
			t.Errorf("%v: exit %d, want %d", tc.args, ExitCode(err), want)
		}
		var buf bytes.Buffer
		if !RenderTopLevelError(&buf, cmd, err) || !json.Valid(buf.Bytes()) {
			t.Errorf("%v: rendered %q, want the refusal as json", tc.args, buf.String())
		}
	}
}
