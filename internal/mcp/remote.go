package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
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
	// ShutdownGrace bounds how long a graceful shutdown waits for in-flight
	// calls to finish once Serve's ctx is cancelled, before the remaining
	// connections are forced closed. Zero means defaultShutdownGrace (10s).
	// Production callers have no reason to set this; it exists so a test can
	// exercise the forced-close path without waiting out ten real seconds.
	ShutdownGrace time.Duration
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
// defaultShutdownGrace is RemoteOptions.ShutdownGrace's value when unset.
const defaultShutdownGrace = 10 * time.Second

func Serve(ctx context.Context, server *sdk.Server, ln net.Listener, opts RemoteOptions) error {
	if opts.Verifier == nil {
		return errors.New("mcp: Serve requires a Verifier — refusing to open an unauthenticated listener")
	}
	grace := opts.ShutdownGrace
	if grace <= 0 {
		grace = defaultShutdownGrace
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
		// Asked to stop is not a failure to report, whether every connection
		// drained inside grace or had to be forced closed — a plain SIGINT
		// under load must not come back as the same error a real failure
		// would. A caller that wants to know which happened reads Stderr,
		// not the return value.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			if opts.Stderr != nil {
				fmt.Fprintf(opts.Stderr,
					"rta: mcp http: graceful shutdown did not finish within %s, forcing the remaining connections closed: %v\n",
					grace, err)
			}
			_ = httpServer.Close()
		}
		return nil
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
// Each candidate is compared in constant time: an early exit on the first
// differing byte would leak, one guess at a time, which characters of a
// valid token are correct. The loop does still exit on the first match, but
// that costs nothing an attacker can observe: Go randomizes map iteration
// order on every call, so which candidate gets compared first — and
// therefore the loop's total length — carries no signal that stays stable
// across repeated attempts.
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
// Refuses a file the group or world can read, write or execute. This file is
// the entire trust anchor for the static-token path, and unlike
// grants.json — which rta writes itself at 0600 and protects with an HMAC
// seal against tampering, not exposure — rta never writes this one; the
// operator does, by whatever means they chose, and a permission check at
// load time is the only guarantee available that a wider read did not
// happen along the way.
//
// On Windows this check does not run: Go's Mode().Perm() on that platform is
// synthesized from the single read-only file attribute, identical for
// owner/group/other, so applying the POSIX bit test there would either
// refuse nearly every file or verify nothing — neither is honest. Rather
// than add a new dependency for real ACL introspection (see
// internal/pluginhost/procattr_windows.go for the same tradeoff made the
// same way elsewhere in this codebase), internal/app/mcp.go prints an
// explicit warning on Windows instead of silently claiming a guarantee this
// function cannot give there.
func LoadTokenFile(path string) (tokens map[string]string, groupReadable bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	// Stat the open handle, not the path a second time: checking permissions
	// and reading content through the same fd closes the window between
	// them — a check against the path and a read against the path again
	// could see two different files if something replaced it in between.
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if runtime.GOOS != "windows" {
		mode := info.Mode().Perm()
		if mode&0o007 != 0 {
			return nil, false, fmt.Errorf(
				"%s has weak permissions (mode %s) — someone besides its owner can read, write or "+
					"execute it, and it is the entire trust anchor for the static-token path; chmod 600 it",
				path, mode)
		}
		groupReadable = mode&0o070 != 0
	}
	raw, err := io.ReadAll(f)
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

// OIDCVerifier builds a verifier for ID tokens issued by issuer, accepted
// only for audience and only for one of subjects.
//
// subjects is not optional. issuer and audience alone name an application
// an identity provider will vouch for, not the specific person the operator
// meant — "one instance, one principal" is this design's whole premise, and
// without pinning specific subjects that premise is asserted, not enforced.
//
// Discovery — the JWKS location and the set of signing algorithms the
// provider actually supports — happens once here, against issuer as the
// operator configured it. That set, not the token's own "alg" header, is
// what bounds which algorithm ID.Verify will accept; a token's self-claimed
// algorithm is exactly the thing every algorithm-confusion attack relies on
// being trusted; oidc.Provider.Verifier never does. The fetch target is
// fixed at startup and never derived from anything inside a caller-presented
// token, which is what keeps a hostile "iss" claim from turning into a
// server-side request to somewhere the operator did not configure.
//
// stderr receives the real reason a token was rejected — bad signature,
// wrong audience or issuer, expiry, or a subject outside the allowlist — the
// moment it happens. The returned function itself always fails with the bare
// auth.ErrInvalidToken sentinel and nothing else, so this verifier is safe
// to hand directly to auth.RequireBearerToken and not only through Compose.
//
// issuer must be https, unless it names a loopback address — the exception
// exists for local testing (this package's own tests included), and it
// covers only the discovery/JWKS fetch: a plaintext round trip either of
// those took could be tampered with in transit, up to and including handing
// back a signing key the attacker controls.
//
// Discovery is bounded by oidcDiscoveryTimeout rather than running on ctx
// alone: ctx here is the command's own context, cancelled only by
// SIGINT/SIGTERM, so an --oidc-issuer host that never answers would
// otherwise hang `rta mcp serve --http` at startup indefinitely.
func OIDCVerifier(ctx context.Context, issuer, audience string, subjects []string, stderr io.Writer) (auth.TokenVerifier, error) {
	if len(subjects) == 0 {
		return nil, errors.New("oidc: at least one --oidc-subject is required — " +
			"an issuer and audience alone identify an application, not a person")
	}
	for _, s := range subjects {
		if strings.TrimSpace(s) == "" {
			return nil, errors.New("oidc: --oidc-subject cannot be empty")
		}
	}
	if err := requireSecureIssuer(issuer); err != nil {
		return nil, err
	}
	discoverCtx, cancel := context.WithTimeout(ctx, oidcDiscoveryTimeout)
	defer cancel()
	provider, err := oidc.NewProvider(discoverCtx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery against %s: %w", issuer, err)
	}
	verify := provider.Verifier(&oidc.Config{ClientID: audience}).Verify
	allowed := make(map[string]bool, len(subjects))
	for _, s := range subjects {
		allowed[s] = true
	}
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		idToken, err := verify(ctx, token)
		if err != nil {
			if stderr != nil {
				fmt.Fprintf(stderr, "rta: mcp http: oidc token rejected: %v\n", err)
			}
			return nil, auth.ErrInvalidToken
		}
		if !allowed[idToken.Subject] {
			if stderr != nil {
				fmt.Fprintf(stderr, "rta: mcp http: oidc token subject %q is not in the allowed list\n", idToken.Subject)
			}
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: idToken.Subject, Expiration: idToken.Expiry}, nil
	}, nil
}

// oidcDiscoveryTimeout bounds the one-time discovery/JWKS fetch OIDCVerifier
// performs at startup. See the timeout paragraph on OIDCVerifier's doc
// comment for why this cannot simply be left to the caller's context.
const oidcDiscoveryTimeout = 15 * time.Second

// requireSecureIssuer refuses a plaintext issuer unless it names a loopback
// address. See the issuer paragraph on OIDCVerifier's doc comment.
func requireSecureIssuer(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("oidc: --oidc-issuer: %w", err)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("oidc: --oidc-issuer %q must be https — plain http is only accepted for a "+
		"loopback address, since discovery and JWKS are fetched from wherever it points", issuer)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
