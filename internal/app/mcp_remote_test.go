package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/registry"
)

// These two combinations must never reach net.Listen, let alone
// mcp.Serve — both are checked and refused before this command builds
// anything that could accept a connection.

func TestHTTPRefusesConsentCombination(t *testing.T) {
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--http", "127.0.0.1:0", "--consent"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("--http --consent was accepted")
	}
	if !strings.Contains(err.Error(), "consent") {
		t.Errorf("err = %q, want it to name --consent as the reason", err)
	}
}

func TestHTTPRequiresATokenFile(t *testing.T) {
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--http", "127.0.0.1:0"})
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

// The consent check runs first: a caller who passed neither --consent nor
// --token-file should never see the token-file message stand in for the
// consent one, since fixing that message's complaint would still refuse.
func TestHTTPConsentIsCheckedBeforeTokenFile(t *testing.T) {
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--http", "127.0.0.1:0", "--consent"})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "consent") {
		t.Fatalf("err = %v, want the consent refusal even though --token-file is also missing", err)
	}
}

func TestHTTPRefusesAnUnreadableTokenFile(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX permission bits do not apply")
	}
	path := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(path, []byte("alice tok-a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRoot(registry.New(), "test")
	cmd.SetArgs([]string{"mcp", "serve", "--http", "127.0.0.1:0", "--token-file", path})
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("a world-readable --token-file was accepted")
	}
	if !strings.Contains(err.Error(), "world-readable") {
		t.Errorf("err = %q, want it to name the permission problem", err)
	}
}
