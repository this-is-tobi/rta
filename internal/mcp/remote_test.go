package mcp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
)

func TestStaticTokenVerifierAcceptsAndRejects(t *testing.T) {
	v := StaticTokenVerifier(map[string]string{"tok-a-0123456789abcdef": "alice", "tok-b-0123456789abcdef": "bob"})
	ctx := context.Background()

	info, err := v(ctx, "tok-a-0123456789abcdef", nil)
	if err != nil {
		t.Fatalf("valid token refused: %v", err)
	}
	if info.UserID != "alice" {
		t.Errorf("UserID = %q, want %q", info.UserID, "alice")
	}

	if _, err := v(ctx, "tok-wrong", nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("invalid token: err = %v, want auth.ErrInvalidToken", err)
	}
	if _, err := v(ctx, "", nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("empty token: err = %v, want auth.ErrInvalidToken", err)
	}
}

func TestComposeTriesEachAndFailsGenerically(t *testing.T) {
	first := StaticTokenVerifier(map[string]string{"tok-a-0123456789abcdef": "alice"})
	second := StaticTokenVerifier(map[string]string{"tok-b-0123456789abcdef": "bob"})
	var stderr strings.Builder
	v := Compose(&stderr, first, second)
	ctx := context.Background()

	// Second verifier's token succeeds even though the first rejects it —
	// composition is OR, not "the first configured mechanism wins".
	info, err := v(ctx, "tok-b-0123456789abcdef", nil)
	if err != nil || info.UserID != "bob" {
		t.Fatalf("tok-b-0123456789abcdef: info=%v err=%v, want bob/nil", info, err)
	}

	// Every mechanism failing folds into one generic error — never which
	// mechanism almost worked, since that text reaches an unauthenticated
	// caller's HTTP response body via auth.RequireBearerToken.
	_, err = v(ctx, "tok-neither", nil)
	if err != auth.ErrInvalidToken {
		t.Errorf("err = %v, want the exact sentinel auth.ErrInvalidToken (nothing wrapped in, nothing appended)", err)
	}
	if stderr.Len() == 0 {
		t.Error("nothing written to stderr — the operator has no way to see why a request was refused")
	}
}

// Chmod after the write, not just WriteFile's mode argument: that argument
// applies only at creation and is filtered by the process umask, so under the
// common 022 a request for 0620 silently produced 0600 — a permission test
// that cannot express the permission it is testing, and one that would have
// reported the group-write refusal below as working before it existed.
func writeTokenFile(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadTokenFile(t *testing.T) {
	t.Run("valid file", func(t *testing.T) {
		path := writeTokenFile(t, 0o600, "# comment\n\nalice tok-a-0123456789abcdef\nbob tok-b-0123456789abcdef\n")
		tokens, groupReadable, err := LoadTokenFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if groupReadable {
			t.Error("0600 file reported group-readable")
		}
		if tokens["tok-a-0123456789abcdef"] != "alice" || tokens["tok-b-0123456789abcdef"] != "bob" {
			t.Errorf("tokens = %v", tokens)
		}
	})
	t.Run("world-readable refuses", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits do not apply")
		}
		path := writeTokenFile(t, 0o644, "alice tok-a-0123456789abcdef\n")
		if _, _, err := LoadTokenFile(path); err == nil {
			t.Fatal("world-readable token file was accepted")
		}
	})
	// The half the old mask missed entirely, and the more serious half: a
	// group member who can write this file does not merely read the tokens,
	// they append one of their own and become a valid caller under any label
	// they like. Group read stays a warning (below); group write is a
	// refusal.
	t.Run("group-writable refuses", func(t *testing.T) {
		path := writeTokenFile(t, 0o620, "alice tok-a-0123456789abcdef\n")
		if _, _, err := LoadTokenFile(path); err == nil {
			t.Error("a group-writable token file was accepted")
		}
	})

	t.Run("group-executable refuses", func(t *testing.T) {
		path := writeTokenFile(t, 0o610, "alice tok-a-0123456789abcdef\n")
		if _, _, err := LoadTokenFile(path); err == nil {
			t.Error("a group-executable token file was accepted")
		}
	})

	t.Run("group-readable warns but loads", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission bits do not apply")
		}
		path := writeTokenFile(t, 0o640, "alice tok-a-0123456789abcdef\n")
		tokens, groupReadable, err := LoadTokenFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !groupReadable {
			t.Error("0640 file not reported group-readable")
		}
		if len(tokens) != 1 {
			t.Errorf("tokens = %v", tokens)
		}
	})
	t.Run("malformed line", func(t *testing.T) {
		path := writeTokenFile(t, 0o600, "alice tok-a-0123456789abcdef extra\n")
		if _, _, err := LoadTokenFile(path); err == nil {
			t.Fatal("a three-field line was accepted")
		}
	})
	t.Run("short token", func(t *testing.T) {
		// A one-character token started the listener once. On this
		// transport the token is the entire credential.
		path := writeTokenFile(t, 0o600, "alice x\n")
		_, _, err := LoadTokenFile(path)
		if err == nil || !strings.Contains(err.Error(), "too short") {
			t.Fatalf("a one-character token: %v, want a refusal naming the floor", err)
		}
	})
	t.Run("duplicate token", func(t *testing.T) {
		path := writeTokenFile(t, 0o600, "alice tok-a-0123456789abcdef\nbob tok-a-0123456789abcdef\n")
		if _, _, err := LoadTokenFile(path); err == nil {
			t.Fatal("the same token assigned to two labels was accepted")
		}
	})
	t.Run("bad label charset", func(t *testing.T) {
		// The non-breaking hyphen (U+2011) renders identically to ASCII "-"
		// but is refused by grant.CheckAgent for exactly that reason — two
		// fields, so this exercises the charset check rather than the
		// field-count one.
		path := writeTokenFile(t, 0o600, "ali‑ce tok-a-0123456789abcdef\n")
		if _, _, err := LoadTokenFile(path); err == nil {
			t.Fatal("a label with a non-breaking hyphen (U+2011) was accepted")
		}
	})
	t.Run("empty file", func(t *testing.T) {
		path := writeTokenFile(t, 0o600, "# only comments\n")
		if _, _, err := LoadTokenFile(path); err == nil {
			t.Fatal("a file with no tokens was accepted")
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if _, _, err := LoadTokenFile(filepath.Join(t.TempDir(), "nope")); err == nil {
			t.Fatal("a nonexistent file was accepted")
		}
	})
}

func TestServeRequiresAVerifier(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	reg := testRegistry(t)
	server := NewServer(reg, "test", Options{})
	if err := Serve(context.Background(), server, ln, RemoteOptions{}); err == nil {
		t.Fatal("Serve ran with no Verifier configured")
	}
}

// authedClient returns an *http.Client that sends token as a bearer token on
// every request, or none at all when token is "".
func authedClient(token string) *http.Client {
	return &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}}
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (rt bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if rt.token != "" {
		req.Header.Set("Authorization", "Bearer "+rt.token)
	}
	return rt.base.RoundTrip(req)
}

// startRemote binds an ephemeral loopback listener, serves reg through it
// with the given verifier, and returns the bound address. The server stops
// when the test's context is cancelled, via t.Cleanup.
func startRemote(t *testing.T, opts Options, verifier auth.TokenVerifier) string {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	server := NewServer(testRegistry(t), "test", opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, server, ln, RemoteOptions{Verifier: verifier}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve did not shut down cleanly: %v", err)
		}
	})
	return addr
}

func TestHTTPTransportRequiresABearerToken(t *testing.T) {
	addr := startRemote(t, Options{}, StaticTokenVerifier(map[string]string{"tok-a-0123456789abcdef": "alice"}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, token := range []string{"", "tok-wrong"} {
		client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
		transport := &sdk.StreamableClientTransport{Endpoint: "http://" + addr, HTTPClient: authedClient(token)}
		if _, err := client.Connect(ctx, transport, nil); err == nil {
			t.Errorf("token %q connected without being authenticated", token)
		}
	}
}

// This is the test that answers whether the SDK carries a per-request
// TokenInfo down to the ctx a registered ToolHandler actually receives, over
// the real Streamable HTTP transport rather than the in-memory one every
// other test in this package uses — connect()/asAgent() never touch an
// http.Request at all, so they cannot exercise this.
func TestHTTPTransportRecordsWhichCredentialCalled(t *testing.T) {
	addr := startRemote(t, Options{},
		Compose(nil, StaticTokenVerifier(map[string]string{"tok-a-0123456789abcdef": "alice"})))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &sdk.StreamableClientTransport{Endpoint: "http://" + addr, HTTPClient: authedClient("tok-a-0123456789abcdef")}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("valid token was refused: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "demo_item_list", Arguments: map[string]any{"name": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("call failed: %+v", res.Content)
	}

	entries, err := agentlog.Read(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("nothing was recorded")
	}
	if got := entries[len(entries)-1].Credential; got != "alice" {
		t.Errorf("Credential = %q, want %q — the verified bearer identity did not reach the tool call's context", got, "alice")
	}
}
