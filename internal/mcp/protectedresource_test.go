package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/this-is-tobi/rta/internal/agentlog"
)

// docIssuer is the issuer a resource names in the tests that never verify a
// token through it: the document publishes it, and nothing here fetches it.
const docIssuer = "https://id.example.com/realms/main"

// startResourceRemote serves the test registry behind a static token, as the
// resource http://<the address bound><path>, with operator beside it when it
// is not nil.
func startResourceRemote(t *testing.T, path string, operator http.Handler) (string, *ProtectedResource) {
	t.Helper()
	var resource *ProtectedResource
	addr := startRemoteWith(t, Options{}, func(addr string) RemoteOptions {
		resource = mustResource(t, "http://"+addr+path, docIssuer)
		return RemoteOptions{
			Verifier: StaticTokenVerifier(map[string]string{"tok-a-0123456789abcdef": "alice"}),
			Resource: resource,
			Operator: operator,
		}
	})
	return addr, resource
}

func mustResource(t *testing.T, resource, issuer string) *ProtectedResource {
	t.Helper()
	r, verr := NewProtectedResource(resource, issuer)
	if verr != nil {
		t.Fatalf("NewProtectedResource(%q): %v", resource, verr)
	}
	return r
}

type answer struct {
	status int
	header http.Header
	body   []byte
}

// ask sends one request, with token as its bearer when it is not "".
func ask(t *testing.T, method, target, token string) answer {
	t.Helper()
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader("{}")
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return answer{res.StatusCode, res.Header, raw}
}

// Every 401 the wall answers says where the resource metadata is, which is
// how an OAuth client learns where to log in. Without it a client that meets
// the wall falls back to taking rta's own origin for the authorization
// server, and the login it starts there goes nowhere.
func TestEvery401NamesWhereTheResourceMetadataIs(t *testing.T) {
	addr, _ := startResourceRemote(t, "", nil)
	want := `Bearer resource_metadata="http://` + addr + `/.well-known/oauth-protected-resource"`
	for _, token := range []string{"", "tok-wrong-0123456789abcdef"} {
		got := ask(t, http.MethodPost, "http://"+addr+"/", token)
		if got.status != http.StatusUnauthorized {
			t.Errorf("token %q: %d, want 401", token, got.status)
		}
		if challenge := got.header.Values("WWW-Authenticate"); !slices.Equal(challenge, []string{want}) {
			t.Errorf("token %q: WWW-Authenticate = %q, want %q", token, challenge, want)
		}
	}
}

// The document is served without a credential, which is its whole point: a
// client reads it before it has one. It names this server, the one issuer and
// the header the token goes in, and nothing more — no scope, since rta
// authorizes with grants and checks none, and no name for the software
// behind it.
func TestTheResourceMetadataIsServedWithoutACredential(t *testing.T) {
	addr, resource := startResourceRemote(t, "", nil)
	metadata := "http://" + addr + "/.well-known/oauth-protected-resource"
	if resource.MetadataURL() != metadata {
		t.Fatalf("MetadataURL() = %q, want %q", resource.MetadataURL(), metadata)
	}

	got := ask(t, http.MethodGet, metadata, "")
	if got.status != http.StatusOK {
		t.Fatalf("GET = %d %s, want 200", got.status, got.body)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(got.body, &doc); err != nil {
		t.Fatalf("the document is not JSON: %v: %s", err, got.body)
	}
	want := map[string]any{
		"resource":                 "http://" + addr,
		"authorization_servers":    []any{docIssuer},
		"bearer_methods_supported": []any{"header"},
	}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("the document = %v, want %v", doc, want)
	}

	if got := ask(t, http.MethodOptions, metadata, ""); got.status != http.StatusNoContent ||
		got.header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("OPTIONS = %d with Access-Control-Allow-Origin %q, want 204 and *",
			got.status, got.header.Get("Access-Control-Allow-Origin"))
	}
	if got := ask(t, http.MethodPost, metadata, ""); got.status != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", got.status)
	}
}

// RFC 9728 §3 puts the metadata of a resource with a path at the well-known
// path followed by that path, and §3.3 makes the resource the root form
// describes the bare origin, which is another resource: a client reading it
// for http://host/mcp refuses it for naming somebody else. So the
// path-suffixed form is served, exactly, and nothing near it is.
func TestAResourceWithAPathIsDescribedOnlyAtItsOwnWellKnownPath(t *testing.T) {
	addr, resource := startResourceRemote(t, "/mcp", nil)
	metadata := "http://" + addr + "/.well-known/oauth-protected-resource/mcp"
	if resource.MetadataURL() != metadata {
		t.Fatalf("MetadataURL() = %q, want %q", resource.MetadataURL(), metadata)
	}

	got := ask(t, http.MethodGet, metadata, "")
	var doc struct {
		Resource string `json:"resource"`
	}
	if got.status != http.StatusOK || json.Unmarshal(got.body, &doc) != nil || doc.Resource != "http://"+addr+"/mcp" {
		t.Errorf("GET %s = %d %s, want 200 naming http://%s/mcp", metadata, got.status, got.body, addr)
	}
	if challenge := ask(t, http.MethodPost, "http://"+addr+"/mcp", "").header.Get("WWW-Authenticate"); challenge !=
		`Bearer resource_metadata="`+metadata+`"` {
		t.Errorf("the challenge = %q, want it to name %s", challenge, metadata)
	}
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp/"} {
		if got := ask(t, http.MethodGet, "http://"+addr+path, ""); got.status != http.StatusUnauthorized {
			t.Errorf("GET %s = %d %s, want the wall's 401", path, got.status, got.body)
		}
	}
}

// A server with static tokens alone has no authorization server to name, and
// a document without one would start an OAuth client on a login that cannot
// finish. Nothing is served, and its 401 stays the one it always was.
func TestAServerWithNoIssuerPublishesNoResourceMetadata(t *testing.T) {
	addr := startRemote(t, Options{}, StaticTokenVerifier(map[string]string{"tok-a-0123456789abcdef": "alice"}))
	got := ask(t, http.MethodGet, "http://"+addr+"/.well-known/oauth-protected-resource", "")
	if got.status != http.StatusUnauthorized {
		t.Errorf("GET = %d %s, want the wall's 401", got.status, got.body)
	}
	if challenge := got.header.Values("WWW-Authenticate"); len(challenge) != 0 {
		t.Errorf("WWW-Authenticate = %q, want none", challenge)
	}
}

// The document and the operator channel sit beside the wall together, and
// neither opens anything else: the operator's paths reach its handler, the
// document answers, and the protocol still wants a bearer.
func TestTheResourceMetadataSitsBesideTheOperatorChannel(t *testing.T) {
	operator := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	addr, resource := startResourceRemote(t, "", operator)
	if got := ask(t, http.MethodPost, "http://"+addr+"/operator/v1/x", ""); got.status != http.StatusTeapot {
		t.Errorf("/operator/v1/x = %d, want the operator handler's 418", got.status)
	}
	if got := ask(t, http.MethodGet, resource.MetadataURL(), ""); got.status != http.StatusOK {
		t.Errorf("the metadata = %d, want 200", got.status)
	}
	got := ask(t, http.MethodPost, "http://"+addr+"/", "")
	if want := `Bearer resource_metadata="` + resource.MetadataURL() + `"`; got.status != http.StatusUnauthorized ||
		got.header.Get("WWW-Authenticate") != want {
		t.Errorf("POST / = %d with %q, want 401 with %q", got.status, got.header.Get("WWW-Authenticate"), want)
	}
}

// Only NewProtectedResource builds a resource Serve mounts, because its path
// becomes a ServeMux pattern: one nobody validated is refused, never mounted.
func TestServeRefusesAResourceNobodyValidated(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	// Bounded, so that a Serve which did start returns instead of serving on.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = Serve(ctx, NewServer(testRegistry(t), "test", Options{}), ln, RemoteOptions{
		Verifier: StaticTokenVerifier(map[string]string{"tok-a-0123456789abcdef": "alice"}),
		Resource: &ProtectedResource{},
	})
	if err == nil {
		t.Fatal("Serve ran with a ProtectedResource NewProtectedResource never built")
	}
}

func TestNewProtectedResource(t *testing.T) {
	for _, c := range []struct{ raw, url, metadata string }{
		{"HTTPS://RTA.example.com/", "https://rta.example.com",
			"https://rta.example.com/.well-known/oauth-protected-resource"},
		{"https://rta.example.com/mcp/", "https://rta.example.com/mcp",
			"https://rta.example.com/.well-known/oauth-protected-resource/mcp"},
		{"https://rta.example.com:8443/team/a.b_c~d-e", "https://rta.example.com:8443/team/a.b_c~d-e",
			"https://rta.example.com:8443/.well-known/oauth-protected-resource/team/a.b_c~d-e"},
		{"http://127.0.0.1:8443", "http://127.0.0.1:8443",
			"http://127.0.0.1:8443/.well-known/oauth-protected-resource"},
		{"http://[::1]:8443", "http://[::1]:8443", "http://[::1]:8443/.well-known/oauth-protected-resource"},
		{"http://localhost:8443/mcp", "http://localhost:8443/mcp",
			"http://localhost:8443/.well-known/oauth-protected-resource/mcp"},
	} {
		r, verr := NewProtectedResource(c.raw, docIssuer)
		if verr != nil {
			t.Errorf("%q refused: %v", c.raw, verr)
			continue
		}
		if r.URL() != c.url || r.MetadataURL() != c.metadata {
			t.Errorf("%q = %q at %q, want %q at %q", c.raw, r.URL(), r.MetadataURL(), c.url, c.metadata)
		}
	}
	for name, raw := range map[string]string{
		"plain http off loopback":      "http://rta.example.com",
		"another scheme":               "ftp://rta.example.com",
		"a query":                      "https://rta.example.com/?x=1",
		"a bare ?":                     "https://rta.example.com/?",
		"a fragment":                   "https://rta.example.com/#top",
		"a bare #":                     "https://rta.example.com/#",
		"a user name":                  "https://alice@rta.example.com",
		"a relative URL":               "rta.example.com/mcp",
		"no host":                      "https:///mcp",
		"an opaque URL":                "https:rta.example.com",
		"an empty port":                "https://rta.example.com:/mcp",
		"a quote in the host":          `https://rta"x.example.com`,
		"a letter beyond ASCII":        "https://rtä.example.com",
		"a wildcard in the path":       "https://rta.example.com/{x}",
		"an escaped brace in the path": "https://rta.example.com/%7Bx%7D",
		"an escaped letter":            "https://rta.example.com/%41",
		"a .. segment":                 "https://rta.example.com/a/../b",
		"a . segment":                  "https://rta.example.com/./mcp",
		"an empty segment":             "https://rta.example.com//mcp",
		"a space":                      "https://rta.example.com/a b",
		"nothing":                      "",
	} {
		r, verr := NewProtectedResource(raw, docIssuer)
		if verr == nil {
			t.Errorf("%s, %q, accepted as %q", name, raw, r.URL())
			continue
		}
		if verr.Code != "core.mcp.resource" || !strings.Contains(verr.Message, "--oidc-resource") || verr.Hint == "" {
			t.Errorf("%s: %#v, want core.mcp.resource naming --oidc-resource, with a hint", name, verr)
		}
	}
	if _, verr := NewProtectedResource("https://rta.example.com", ""); verr == nil {
		t.Error("a resource with no issuer to name was accepted")
	}
}

// The whole discovery, end to end, through the SDK's own OAuth client: it
// meets the wall, reads the challenge, fetches the document, finds the issuer
// there, logs in at the issuer naming the resource, and calls a tool with the
// token it got back. The server accepts that token because its audience is
// the resource, and records the call under the subject it names.
//
// Without the document the same client takes rta's own origin for the
// authorization server, the 2025-03-26 fallback, and sends its login and its
// token request there, where both meet the wall.
func TestAnOAuthClientFindsTheIssuerThroughTheResourceMetadata(t *testing.T) {
	idp := newFakeIdP(t)
	var resource *ProtectedResource
	startRemoteWith(t, Options{}, func(addr string) RemoteOptions {
		resource = mustResource(t, "http://"+addr, idp.issuer)
		verifier, err := OIDCVerifier(context.Background(), idp.issuer, resource.URL(), []string{testSubject}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		return RemoteOptions{Verifier: Compose(nil, verifier), Resource: resource}
	})

	var (
		mu           sync.Mutex
		authorizedAt []string
	)
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		PreregisteredClient: &oauthex.ClientCredentials{ClientID: "rta"},
		RedirectURL:         "http://127.0.0.1/callback",
		AuthorizationCodeFetcher: func(_ context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			u, err := url.Parse(args.URL)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			authorizedAt = append(authorizedAt, u.Scheme+"://"+u.Host+u.Path)
			mu.Unlock()
			return &auth.AuthorizationResult{Code: fakeCode, State: u.Query().Get("state")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: resource.URL(), OAuthHandler: handler}, nil)
	if err != nil {
		t.Fatalf("the client did not get through: %v", err)
	}
	defer func() { _ = session.Close() }()

	mu.Lock()
	logins := slices.Clone(authorizedAt)
	mu.Unlock()
	if want := []string{idp.issuer + "/authorize"}; !slices.Equal(logins, want) {
		t.Errorf("the client logged in at %q, want %q", logins, want)
	}
	if got := idp.requestedResources(); !slices.Equal(got, []string{resource.URL()}) {
		t.Errorf("the token requests named the resources %q, want %q", got, resource.URL())
	}

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
	if got := entries[len(entries)-1].Credential; got != testSubject {
		t.Errorf("Credential = %q, want the token's subject %q", got, testSubject)
	}
}
