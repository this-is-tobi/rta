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
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/pkg/view"
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
	// Operator, when non-nil, mounts the operator channel (NewOperatorHandler)
	// under /operator/v1 beside the MCP handler. It sits outside the bearer
	// wall on purpose — its authentication is the envelope signature, and an
	// agent's bearer token must open nothing there — see operatorhttp.go.
	// Cross-origin protection still wraps it, like everything on this
	// listener. nil means the channel is absent: those paths fall through to
	// the MCP handler and its bearer refusal, indistinguishable from a server
	// that never heard of operators.
	Operator http.Handler
	// Resource, when non-nil, makes this listener an OAuth protected resource
	// a client can discover: its metadata (NewProtectedResource) is served at
	// the one well-known path RFC 9728 gives it, outside the bearer wall,
	// since a client reads it before it holds a token, and every 401 the wall
	// answers names that path. nil, as on a server with static tokens alone,
	// means no document and a 401 with no challenge, exactly as before there
	// was one. Cross-origin protection wraps it like everything else here.
	Resource *ProtectedResource
	// ObserveListener and ObserveHandler, when both are set, run the probes
	// and the counters (NewObserveHandler) on a second address for the
	// lifetime of this Serve call.
	//
	// A second listener rather than more paths here, because of what this
	// listener holds to: anything that runs a capability or reads state sits
	// behind the bearer wall, and beside it there is only what cannot be
	// anywhere else — the operator channel, whose envelope signature is its
	// own authentication, and the one constant document RFC 9728 pins to
	// this origin (Resource), which a client finds by the URL it was given
	// and nowhere else, so it cannot move to a second address. A probe can
	// be anywhere, and /readyz reads the data directory's state; every open
	// path here makes this listener's security a little more a property of
	// route matching rather than of the wrapper, and the most a routing
	// mistake is allowed to cost is reaching a constant document, or a
	// channel that checks its own signatures, instead of the wall — never
	// what is past it. Apart, an operator can also bind the observation
	// address somewhere the agent-facing one is not.
	ObserveListener net.Listener
	ObserveHandler  http.Handler
}

// defaultShutdownGrace is RemoteOptions.ShutdownGrace's value when unset.
const defaultShutdownGrace = 10 * time.Second

// maxRequestBody bounds one request on the MCP listener. It is the SDK's own
// default, stated here rather than inherited: a tool call's arguments are a
// JSON object a schema already bounds in shape, and 4 MiB is past anything a
// legitimate call carries — a default a future SDK release could widen for
// its own reasons should not widen this listener's exposure without a line
// in this file changing.
const maxRequestBody = 4 << 20

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
// check is optional or bypassable by a flag. Beside the wall there is only
// what RemoteOptions asks for by name, the operator channel and the resource
// metadata — see ObserveListener there for why nothing else is.
func Serve(ctx context.Context, server *sdk.Server, ln net.Listener, opts RemoteOptions) error {
	if opts.Verifier == nil {
		return errors.New("mcp: Serve requires a Verifier — refusing to open an unauthenticated listener")
	}
	if opts.Resource != nil && opts.Resource.path == "" {
		return errors.New("mcp: Serve was handed a ProtectedResource NewProtectedResource did not build — " +
			"refusing to mount a path nobody validated")
	}
	grace := opts.ShutdownGrace
	if grace <= 0 {
		grace = defaultShutdownGrace
	}
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{MaxRequestBodyBytes: maxRequestBody})
	verifier := slowFailures(opts.Verifier, newBackoff(bearerFree, bearerWindow, bearerStep, bearerMax))
	wall := &auth.RequireBearerTokenOptions{
		// Static tokens have no per-token expiry of their own — they are
		// valid until the operator rewrites the file — so nothing here
		// synthesizes one. A real exp claim (OIDC) is still enforced: this
		// only excuses TokenInfo.Expiration being zero, never overrides it
		// when set.
		AllowMissingExpiration: true,
	}
	if opts.Resource != nil {
		wall.ResourceMetadataURL = opts.Resource.metadataURL
	}
	authed := auth.RequireBearerToken(verifier, wall)(limitSessions(server, handler))
	root := authed
	if opts.Operator != nil || opts.Resource != nil {
		mux := http.NewServeMux()
		if opts.Operator != nil {
			mux.Handle("/operator/v1/", opts.Operator)
		}
		if opts.Resource != nil {
			// An exact pattern, not a subtree: the path with a slash after it,
			// or anything beneath it, meets the wall like any other.
			mux.Handle(opts.Resource.path, auth.ProtectedResourceMetadataHandler(opts.Resource.document))
		}
		mux.Handle("/", authed)
		root = mux
	}
	// The deprecated StreamableHTTPOptions.CrossOriginProtection field is
	// left unset on purpose; this is the SDK's own documented replacement.
	protected := http.NewCrossOriginProtection().Handler(root)

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

	// The observation listener's own timeouts are ordinary, unlike the MCP
	// one's: nothing here streams, so a write deadline is a straightforward
	// guard rather than something that would cut a long tool call in half.
	var observeServer *http.Server
	if opts.ObserveListener != nil && opts.ObserveHandler != nil {
		observeServer = &http.Server{
			Handler:           opts.ObserveHandler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			// Reported, never fatal. A dead observation listener is a pod that
			// stops answering probes and gets restarted on its own schedule —
			// which is the orchestrator's decision to make. Taking the MCP
			// server down here would turn a monitoring fault into an outage.
			if err := observeServer.Serve(opts.ObserveListener); err != nil &&
				!errors.Is(err, http.ErrServerClosed) && opts.Stderr != nil {
				fmt.Fprintf(opts.Stderr, "rta: observation listener stopped: %v\n", err)
			}
		}()
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(ln) }()

	if observeServer != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
			defer cancel()
			if err := observeServer.Shutdown(shutdownCtx); err != nil {
				_ = observeServer.Close()
			}
		}()
	}

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

// minTokenLen is the shortest bearer token the file may hold. A one-character
// token started the listener once; on a transport where the token is the
// entire credential, that is a door with no lock. Sixteen characters of
// anything `rta gen token` produces is past what an online guess reaches;
// the floor is on length rather than on a measured entropy because the
// file is written by the operator, and refusing a token as "not random
// enough" is a judgement rta cannot make honestly from the bytes alone.
const minTokenLen = 16

// tokenShaped is a label that is the shape `rta gen token` writes, refused as
// a label because it is far likelier to be a token in the label's place.
// That output is 64 hex digits, which grant.CheckAgent accepts, so a pair
// written the wrong way round with a label of sixteen characters or more —
// `claude-desktop-laptop` — loaded silently: the label became the bearer
// credential, words anybody could guess, and the token the agent's name,
// recorded in the ledger and printed wherever an agent is. Thirty-two digits
// and up, so that `--length 16` is caught too; no name a person chose for a
// caller is that many hex digits and nothing else. A token of another
// alphabet in the label's place still loads, and nothing on the line can
// tell rta which word is which.
var tokenShaped = regexp.MustCompile(`^[0-9a-fA-F]{32,}$`)

// LoadTokenFile reads a static-token file: one "label token" pair per
// non-blank, non-comment line, whitespace-separated. A line starting with #
// is a comment.
//
// Refuses a file that anyone but its owner can write or execute, or that any
// account on the machine can read; a file the *group* can read loads with a
// warning rather than a refusal. That split is deliberate and is not the one
// this comment used to claim — it said the group could not read either, while
// the code only ever tested the world bits, so a 0640 file passed silently.
// Corrected here rather than in the code because the two halves earn
// different answers:
//
//   - Group *write* is authentication bypass, not exposure. A group member who
//     can write this file appends a line with a token of their own choosing
//     and is then a valid caller under whatever label they pick. Nothing rta
//     documents needs the file writable by anyone but its owner, so this is
//     refused outright, along with group execute.
//   - Group *read* leaks the tokens that exist, which is worse than 0600 and
//     better than the alternative of refusing to start: a shared-group file
//     is how some deployments hand a credential to a service account, and
//     docs/30-boundary/65-hosting-a-server.md promises only that world-readable files are refused.
//     internal/app/mcp.go prints the warning, and the operator decides.
//
// This file is the entire trust anchor for the static-token path, and unlike
// grants.json — which rta writes itself at 0600 and protects with an HMAC
// seal against tampering, not exposure — rta never writes this one; the
// operator does, by whatever means they chose, so it is held to
// paths.CheckTrustAnchor at load.
func LoadTokenFile(path string) (tokens map[string]string, groupReadable bool, err error) {
	f, err := atomicfile.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	// Stat the open handle, not the path a second time: checking permissions
	// and reading content through the same fd closes the window between
	// them — a check against the path and a read against the path again
	// could see two different files if something replaced it in between.
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	groupReadable, err = paths.CheckTrustAnchor(path, info, "it is the entire trust anchor for the static-token path")
	if err != nil {
		return nil, false, err
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, groupReadable, err
	}
	tokens = map[string]string{}
	seen := map[string]int{} // token -> the line holding it, to name a duplicate's other half
	// A refusal below names lines and never a word of one. It is printed on
	// stderr at startup, which is the journal, the pod log or the MCP
	// client's server log, read by people the file's mode keeps out, and no
	// word on a line can be told for certain to be a label rather than a
	// token: a token alone, a comment after the pair, or the pair the wrong
	// way round, where the "label" checked is the token and its alphabet is
	// often a label's. That holds for a line that passes every check too —
	// a reversed pair whose label is long enough to pass for a token does —
	// so a duplicate is named by the line that holds it first, not by that
	// line's label.
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			got := fmt.Sprintf("%d words", len(fields))
			if len(fields) == 1 {
				got = "one word"
			}
			if slices.ContainsFunc(fields[1:], func(w string) bool { return strings.HasPrefix(w, "#") }) {
				got += ", and a comment has a line of its own"
			}
			return nil, groupReadable, fmt.Errorf("%s:%d: expected \"label token\", got %s", path, i+1, got)
		}
		label, token := fields[0], fields[1]
		if len(token) < minTokenLen {
			return nil, groupReadable, fmt.Errorf("%s:%d: the token, the second word, is too short at %d characters — a "+
				"bearer token is the whole credential on this transport, and one shorter than %d is guessable; "+
				"`rta gen token` makes one", path, i+1, len(token), minTokenLen)
		}
		if verr := grant.CheckAgent(label); verr != nil || label == "" {
			return nil, groupReadable, fmt.Errorf("%s:%d: the label, the first word, is not a valid one: letters, "+
				"digits, - _ and . only, at most %d of them", path, i+1, grant.MaxAgentName)
		}
		if tokenShaped.MatchString(label) {
			return nil, groupReadable, fmt.Errorf("%s:%d: the label, the first word, has the shape of a token, "+
				"%d hex digits — is the pair the wrong way round? It is \"label token\", the label naming "+
				"who holds the token", path, i+1, len(label))
		}
		if first, dup := seen[token]; dup {
			return nil, groupReadable, fmt.Errorf("%s:%d: this token is already assigned on line %d", path, i+1, first)
		}
		seen[token] = i + 1
		tokens[token] = label
	}
	if len(tokens) == 0 {
		return nil, groupReadable, fmt.Errorf("%s has no tokens", path)
	}
	return tokens, groupReadable, nil
}

// OIDCVerifier builds a verifier for the bearer tokens issuer signs, accepted
// only for audience and only for one of subjects.
//
// What it verifies is a signed JWT, offline, against the keys the issuer
// publishes: go-oidc's ID-token verifier, which checks the signature, iss,
// aud, exp and nbf and nothing else, so a JWT access token passes exactly
// when an ID token with the same claims would. typ, azp and scope are never
// read. An opaque access token, or an encrypted one, does not parse and is
// refused, because nothing here does RFC 7662 introspection: that would put a
// client secret on this server and a round trip to the issuer in front of
// every call, for the identity providers that issue JWT access tokens anyway.
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
// back a signing key the attacker controls. And so must everything discovery
// leads to, which checking the issuer alone did not cover (secureFetch).
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
	plain, err := requireSecureIssuer(issuer)
	if err != nil {
		return nil, err
	}
	fetch := secureFetch{next: oidcTransport(), plainLoopback: plain}
	discoverCtx, cancel := context.WithTimeout(ctx, oidcDiscoveryTimeout)
	defer cancel()
	// The provider keeps the client it was discovered with, and every later
	// fetch of the signing keys goes through it too (go-oidc's remote key set).
	discoverCtx = oidc.ClientContext(discoverCtx, &http.Client{Transport: fetch})
	provider, err := oidc.NewProvider(discoverCtx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery against %s: %w", issuer, err)
	}
	var discovered struct {
		Keys string `json:"jwks_uri"`
	}
	if err := provider.Claims(&discovered); err != nil {
		return nil, fmt.Errorf("oidc: discovery against %s: %w", issuer, err)
	}
	keys, err := url.Parse(discovered.Keys)
	if err == nil {
		err = fetch.allows(keys)
	}
	if err != nil {
		return nil, fmt.Errorf("oidc: %s names its signing keys at %q, and they are not fetched from there: %w — "+
			"the identity provider's discovery document has to name an https address", issuer, discovered.Keys, err)
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

// secureFetch is the transport discovery and every fetch of the signing keys
// go through: it refuses any request that is not https, wherever its address
// came from.
//
// **Because an https issuer was the only address anything checked.** The
// discovery document names where the keys are (jwks_uri), and go-oidc fetched
// them from there over plain http as readily as over https, and followed a
// redirect from the issuer's https discovery address to a plain one: a key
// swapped in transit on either signs any token `rta mcp serve --http` then
// accepts. The one plain address allowed is a loopback one under an issuer
// that is itself plain loopback, the local-testing exception
// requireSecureIssuer makes; under an https issuer nothing is fetched in the
// clear, loopback included.
//
// A transport rather than a redirect policy, since the keys address is not a
// redirect: every request either fetch makes, each hop of a redirect
// included, passes through here.
type secureFetch struct {
	next          http.RoundTripper
	plainLoopback bool
}

func (f secureFetch) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := f.allows(r.URL); err != nil {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, err
	}
	return f.next.RoundTrip(r)
}

func (f secureFetch) allows(u *url.URL) error {
	switch {
	case u.Scheme == "https" && u.Host != "":
		return nil
	case u.Scheme == "http" && f.plainLoopback && isLoopbackHost(u.Hostname()):
		return nil
	}
	return fmt.Errorf("%s://%s is not https, and discovery and signing keys are fetched only over https", u.Scheme, u.Host)
}

// oidcTransport is what secureFetch sends through: the default transport's
// proxy and timeouts, as go-oidc's default client had. A test points it at a
// server with a certificate of its own.
var oidcTransport = func() http.RoundTripper {
	return http.DefaultTransport.(*http.Transport).Clone()
}

// requireSecureIssuer refuses a plaintext issuer unless it names a loopback
// address, and says whether it is that exception. See the issuer paragraph on
// OIDCVerifier's doc comment.
func requireSecureIssuer(issuer string) (plainLoopback bool, err error) {
	u, err := url.Parse(issuer)
	if err != nil {
		return false, fmt.Errorf("oidc: --oidc-issuer: %w", err)
	}
	if u.Scheme == "https" {
		return false, nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return true, nil
	}
	return false, fmt.Errorf("oidc: --oidc-issuer %q must be https — plain http is only accepted for a "+
		"loopback address, since discovery and JWKS are fetched from wherever it points", issuer)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ProtectedResource is this server described to an OAuth client: RFC 9728
// protected resource metadata, naming the URL clients are configured with and
// the one issuer whose tokens it accepts. A client that meets the wall reads
// the challenge, fetches this, and learns where to log in.
//
// Only NewProtectedResource builds one, and that is why nothing in it is
// exported: Serve mounts path as a ServeMux pattern, where a { is read as a
// wildcard and a malformed one panics at startup, so a path nobody validated
// must have no way to reach it.
type ProtectedResource struct {
	url         string
	metadataURL string
	path        string
	document    *oauthex.ProtectedResourceMetadata
}

// URL is the canonical resource URL: what an MCP client must be configured
// with, exactly, since it compares the document's resource to the URL it was
// given byte for byte (RFC 9728 §3.3).
func (p *ProtectedResource) URL() string { return p.url }

// MetadataURL is where the document is served, and what every 401 names.
func (p *ProtectedResource) MetadataURL() string { return p.metadataURL }

// protectedResourceWellKnown is RFC 9728 §3's well-known path. A resource with
// a path has its metadata at this followed by that path. §3.3 makes the
// resource this path alone describes the bare origin, so it is not served for
// a resource that has one: a client would refuse it as naming another.
const protectedResourceWellKnown = "/.well-known/oauth-protected-resource"

// NewProtectedResource validates resource, the URL MCP clients are configured
// with for this server, and describes it as a resource whose tokens issuer
// signs.
//
// The URL is an identity more than an address: a client compares the
// document's resource to the URL it was given, verbatim, and an RFC 8707
// authorization server mints it into a token's aud. So it is put in one
// canonical form here, the one the MCP specification recommends — the host
// lowercased, trailing slashes trimmed — and refused, never quietly repaired,
// wherever it could not be the string the operator typed and clients were
// told:
//
//   - https, or http for a loopback host only: the exception the issuer and
//     the operator URL make, for the same local testing, and the one the
//     SDK's own client makes. Anywhere else a client's token would cross the
//     wire in clear.
//   - no user name, and no query or fragment, not even a bare ? or #: RFC
//     9728 §1.2 forbids the last two in a resource identifier, and a URL that
//     dropped them would no longer be the one clients were given.
//     operator.CanonicalServerURL drops them, which is why it is not reused.
//   - a host, unless it is an IP address, and path segments of RFC 3986
//     unreserved characters only, no segment empty, . or .., and nothing
//     percent-escaped. The path becomes a ServeMux pattern, and both reach
//     every 401 inside a quoted string: url.Parse lets a quote or a letter
//     beyond ASCII through in a host, which a client cannot parse back out of
//     that header, and an escape decodes to a path the pattern no longer
//     spells.
//
// The issuer is published as it was given. Discovery, which startup runs
// next, refuses a provider whose own document names any other, so this is the
// string the tokens carry in iss.
func NewProtectedResource(resource, issuer string) (*ProtectedResource, *view.Error) {
	refuse := func(problem, hint string) *view.Error {
		return view.Errorf("core.mcp.resource", "--oidc-resource %q %s", resource, problem).WithHint(hint)
	}
	if issuer == "" {
		return nil, view.Errorf("core.mcp.resource", "--oidc-resource has no issuer to publish").
			WithHint("the document names --oidc-issuer as the authorization server a client logs in at")
	}
	if strings.ContainsAny(resource, "?#") {
		return nil, refuse("has a query or a fragment",
			"RFC 9728 forbids both in a resource identifier, even an empty one: drop everything from the ? or #")
	}
	u, err := url.Parse(resource)
	if err != nil {
		return nil, refuse("is not a URL: "+err.Error(),
			"the URL MCP clients are configured with, such as https://rta.example.com")
	}
	if u.Scheme == "" || u.Opaque != "" || u.Host == "" {
		return nil, refuse("is not an absolute URL",
			"a scheme and a host, as MCP clients are configured with it: https://rta.example.com, "+
				"or https://rta.example.com/mcp when the proxy serves rta under a path")
	}
	if u.User != nil {
		return nil, refuse("names a user",
			"a resource URL names a server, not an account: drop the part before the @")
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopbackHost(host)) {
		return nil, refuse("must be https",
			"plain http is accepted for a loopback address only, since a client sends its token to this URL: "+
				"give the https URL of the proxy that terminates TLS in front of rta")
	}
	if strings.HasSuffix(u.Host, ":") || (net.ParseIP(host) == nil && !unreserved(host)) {
		return nil, refuse("does not name its host as a DNS name or an IP address",
			"letters, digits and - . _ ~, then a port after a colon if there is one; a name beyond ASCII "+
				"is written in its xn-- form")
	}
	path := strings.TrimRight(u.Path, "/")
	if u.RawPath != "" || !servablePath(path) {
		return nil, refuse("has a path its metadata cannot be published under",
			"segments of letters, digits and - . _ ~, none empty, . or .., and nothing percent-escaped: "+
				"the path is part of the URL the metadata is served at")
	}
	origin := u.Scheme + "://" + strings.ToLower(u.Host)
	return &ProtectedResource{
		url:         origin + path,
		metadataURL: origin + protectedResourceWellKnown + path,
		path:        protectedResourceWellKnown + path,
		document: &oauthex.ProtectedResourceMetadata{
			Resource:               origin + path,
			AuthorizationServers:   []string{issuer},
			BearerMethodsSupported: []string{"header"},
		},
	}, nil
}

func servablePath(path string) bool {
	if path == "" {
		return true
	}
	for _, segment := range strings.Split(path, "/")[1:] {
		if segment == "" || segment == "." || segment == ".." || !unreserved(segment) {
			return false
		}
	}
	return true
}

// unreserved reports whether s is non-empty and every byte of it one of RFC
// 3986's unreserved characters.
func unreserved(s string) bool {
	for i := range len(s) {
		switch c := s[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
		default:
			return false
		}
	}
	return s != ""
}
