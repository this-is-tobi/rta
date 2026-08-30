package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rule-them-all/internal/grant"
)

// The HTTP transport: a caller proves who it is over the wire, instead of
// being trusted because it is this process's stdio parent. Everything below
// exists to keep that proof mandatory — there is no path through Serve that
// skips it, and no verifier here ever tells a failed caller why it failed.

// RemoteOptions configures the HTTP listener.
type RemoteOptions struct {
	// Verifier authenticates every request. Required: Serve refuses to run
	// with a nil one. Compose combines more than one mechanism.
	Verifier auth.TokenVerifier
	// Stderr is where the real reason a request was refused goes — never the
	// response, which an unauthenticated caller reads. nil discards it.
	Stderr io.Writer
}

// Serve runs server over the Streamable HTTP transport on ln, blocking
// until ctx is cancelled or the listener stops on its own.
//
// Takes an already-bound net.Listener rather than an address: the caller
// resolves and reports the real address (useful for ":0" and for tests),
// and a bind failure is the caller's to report in its own words rather than
// something this function has to reconstruct from net/http's error text.
// TLS is not this function's job either — ln is plain TCP, and a reverse
// proxy, ingress or service mesh in front of it is where that belongs.
//
// Bearer authentication wraps the whole protocol handler, and cross-origin
// protection wraps that: a browser page cannot drive this endpoint against
// its holder's wishes just because it can reach the address, and neither
// check is optional or bypassable by a flag.
func Serve(ctx context.Context, server *sdk.Server, ln net.Listener, opts RemoteOptions) error {
	if opts.Verifier == nil {
		return errors.New("mcp: Serve requires a Verifier — refusing to open an unauthenticated listener")
	}
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	authed := auth.RequireBearerToken(opts.Verifier, &auth.RequireBearerTokenOptions{
		// Static tokens have no per-token expiry of their own — they are
		// valid until the operator rewrites the file — so nothing here
		// synthesizes one. A real exp claim (OIDC) is still enforced: this
		// only excuses TokenInfo.Expiration being zero, never overrides it
		// when set.
		AllowMissingExpiration: true,
	})(handler)
	// The deprecated StreamableHTTPOptions.CrossOriginProtection field is
	// left unset on purpose; this is the SDK's own documented replacement.
	protected := http.NewCrossOriginProtection().Handler(authed)

	httpServer := &http.Server{
		Handler:           protected,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout is deliberately unset. The streamable transport can
		// hold a response open for a long tool call or a subscribed stream,
		// and a fixed write deadline would cut that off mid-flight whether
		// or not anything is wrong — IdleTimeout already bounds a connection
		// that stops making progress.
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(ln) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

// Compose tries each verifier in turn, succeeding on the first that accepts
// the token.
//
// The combined mechanism's strength is the *weaker* of its parts, not their
// sum — a leaked static token authenticates exactly as well as a valid OIDC
// one once both map onto the same server identity — which is what makes the
// audit trail (agentlog.Entry.Credential, populated from TokenInfo.UserID)
// the thing that makes a leaked credential attributable rather than
// invisible, not this function.
//
// Every failure is folded into one generic auth.ErrInvalidToken: which
// verifier almost worked, and why, is written to stderr and never to the
// response — auth.RequireBearerToken puts a verifier's returned error text
// verbatim into the body an unauthenticated caller reads.
func Compose(stderr io.Writer, verifiers ...auth.TokenVerifier) auth.TokenVerifier {
	return func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		var reasons []string
		for _, v := range verifiers {
			info, err := v(ctx, token, req)
			if err == nil {
				return info, nil
			}
			reasons = append(reasons, err.Error())
		}
		if stderr != nil && len(reasons) > 0 {
			fmt.Fprintf(stderr, "rta: mcp http: bearer token rejected by every configured verifier: %s\n",
				strings.Join(reasons, "; "))
		}
		return nil, auth.ErrInvalidToken
	}
}

// StaticTokenVerifier authenticates against a fixed set of operator-issued
// tokens, mapping each to the label that names it in the audit trail.
//
// Comparison is constant-time per candidate: an early exit on the first
// differing byte would leak, one guess at a time, which characters of a
// valid token are correct. Trying every candidate rather than stopping at
// the first match costs nothing an attacker can observe, since the map has
// no meaningful iteration-time signal beyond what the constant-time compare
// itself already flattens.
func StaticTokenVerifier(tokens map[string]string) auth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		for candidate, label := range tokens {
			if subtle.ConstantTimeCompare([]byte(token), []byte(candidate)) == 1 {
				return &auth.TokenInfo{UserID: label}, nil
			}
		}
		return nil, auth.ErrInvalidToken
	}
}

// LoadTokenFile reads a static-token file: one "label token" pair per
// non-blank, non-comment line, whitespace-separated. A line starting with #
// is a comment.
//
// Refuses a file the group or world can read. This file is the entire trust
// anchor for the static-token path, and unlike grants.json — which rta
// writes itself at 0600 and protects with an HMAC seal against tampering,
// not exposure — rta never writes this one; the operator does, by whatever
// means they chose, and a permission check at load time is the only
// guarantee available that a wider read did not happen along the way.
func LoadTokenFile(path string) (tokens map[string]string, groupReadable bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	if runtime.GOOS != "windows" {
		mode := info.Mode().Perm()
		if mode&0o007 != 0 {
			return nil, false, fmt.Errorf(
				"%s is world-readable (mode %s) — it is the entire trust anchor for the static-token "+
					"path, readable by any account on this machine; chmod 600 it", path, mode)
		}
		groupReadable = mode&0o070 != 0
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, groupReadable, err
	}
	tokens = map[string]string{}
	seen := map[string]string{} // token -> label, to name a duplicate's other half
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, groupReadable, fmt.Errorf("%s:%d: expected \"label token\", got %q", path, i+1, line)
		}
		label, token := fields[0], fields[1]
		if verr := grant.CheckAgent(label); verr != nil || label == "" {
			return nil, groupReadable, fmt.Errorf("%s:%d: label %q is not a valid token label", path, i+1, label)
		}
		if other, dup := seen[token]; dup {
			return nil, groupReadable, fmt.Errorf("%s:%d: this token is already assigned to %q", path, i+1, other)
		}
		seen[token] = label
		tokens[token] = label
	}
	if len(tokens) == 0 {
		return nil, groupReadable, fmt.Errorf("%s has no tokens", path)
	}
	return tokens, groupReadable, nil
}
