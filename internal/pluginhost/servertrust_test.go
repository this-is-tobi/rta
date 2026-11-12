package pluginhost

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log"
	mathbig "math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/this-is-tobi/rta/internal/tlstrust"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/wire"
	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

var vault = plugin.Service{Namespace: "vault", Product: "Vault", Reach: plugin.EndpointURL,
	Default: "https://127.0.0.1:8200", Port: 8200, Switch: plugin.TLSByScheme}

// serviceStub is a plugin process whose one capability does what a plugin on
// the SDK does: plugin.Service's client reaches the server, and its refusal
// crosses the wire, what the server presented with it.
type serviceStub struct {
	rtav1.PluginServiceClient
	calls int
	// trusted is what each call carried.
	trusted [][][]byte
}

func (s *serviceStub) Call(_ context.Context, req *rtav1.CallRequest, _ ...grpc.CallOption) (*rtav1.CallResponse, error) {
	s.calls++
	s.trusted = append(s.trusted, req.GetTrusted())
	pr := plugin.NewRequest(wire.ValuesFromProto(req.GetValues()), false, false).
		WithSurface(wire.SurfaceFromProto(req.GetSurface())).
		WithProfile(req.GetProfile(), wire.TunnelFromProto(req.GetTunnel())).
		WithTrusted(req.GetTrusted())
	failed := func(verr *view.Error) (*rtav1.CallResponse, error) {
		return &rtav1.CallResponse{Result: &rtav1.CallResponse_Error{Error: wire.ErrorToProto(verr)}}, nil
	}
	client, verr := vault.HTTPClient(pr)
	if verr != nil {
		return failed(verr)
	}
	target, _ := vault.Target(pr)
	resp, err := client.Get(target.String() + "/v1/sys/health")
	if err != nil {
		if verr := vault.Refusal(pr, err); verr != nil {
			return failed(verr)
		}
		return failed(view.Errorf("vault.conn.failed", "%v", err))
	}
	_ = resp.Body.Close()
	return &rtav1.CallResponse{Result: &rtav1.CallResponse_View{View: wire.ViewToProto(view.Text{Body: "sealed: false"})}}, nil
}

// serving is an HTTPS server on 127.0.0.1 presenting a self-signed
// certificate of its own, which nothing on this machine vouches for.
func serving(t *testing.T) *httptest.Server {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: mathbig.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "vault"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	// Every refused handshake is one these tests mean.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// reaching is the vault capability wired to stub, as attach wires a plugin's.
func reaching(stub rtav1.PluginServiceClient) plugin.Capability {
	c := &Client{stub: stub}
	dst := plugin.Capability{ID: "vault.health", Inputs: vault.Fields()}
	c.attach(&dst, &rtav1.Capability{})
	return dst
}

// healthAt is one call of c to url, on sf, through a forward on coordinate
// when one is given.
func healthAt(c plugin.Capability, url string, sf plugin.Surface, coordinate string) (view.View, *view.Error) {
	in := plugin.Inputs{Caller: map[string]any{"url": url}}
	if coordinate != "" {
		in.ProfileName, in.Tunnel, in.Coordinate = "lab", plugin.TunnelKube, coordinate
	}
	v, err := c.Run(context.Background(), plugin.ResolveRequest(c, in, false, false).WithSurface(sf))
	var verr *view.Error
	errors.As(err, &verr)
	return v, verr
}

// told is a refusal as a test reports it: its code, message and hint.
func told(verr *view.Error) string {
	if verr == nil {
		return "no refusal"
	}
	return verr.Code + ": " + verr.Message + " [hint: " + verr.Hint + "]"
}

func codeOf(verr *view.Error) string {
	if verr == nil {
		return ""
	}
	return verr.Code
}

// A server reached directly whose certificate nothing vouches for is refused,
// and what it presented kept for the operator, the refusal saying how they look
// at it; once they trust it, the call reaches it carrying what they trusted.
func TestAServerReachedDirectlyWaitsForTheOperatorsWord(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	srv := serving(t)
	server := strings.TrimPrefix(srv.URL, "https://")
	stub := &serviceStub{}
	c := reaching(stub)

	_, verr := healthAt(c, srv.URL, plugin.SurfaceCLI, "")
	if verr == nil || !strings.HasPrefix(verr.Code, "vault.tls.") || verr.Presented != nil ||
		!strings.Contains(verr.Hint, "`rta cert trust "+server+"` shows the certificate it presented and trusts it on your word") ||
		stub.calls != 1 {
		t.Fatalf("a first call: %s after %d calls", told(verr), stub.calls)
	}
	servers, _ := tlstrust.List()
	if len(servers) != 1 || servers[0].Pending == nil || servers[0].Trusted != nil || servers[0].Server != server {
		t.Fatalf("kept: %+v", servers)
	}
	if _, verr := tlstrust.Trust("vault", server, servers[0].Pending.Fingerprint()); verr != nil {
		t.Fatal(verr)
	}
	if _, verr := healthAt(c, srv.URL, plugin.SurfaceCLI, ""); verr != nil || len(stub.trusted[len(stub.trusted)-1]) != 1 {
		t.Errorf("a call once the operator trusted it: %+v, carrying %d certificates", verr, len(stub.trusted[len(stub.trusted)-1]))
	}
}

// Over MCP the refusal sends the agent to the operator, naming no server.
func TestAnAgentIsSentToTheOperatorAndToldNoServer(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	srv := serving(t)
	_, verr := healthAt(reaching(&serviceStub{}), srv.URL, plugin.SurfaceMCP, "")
	if verr == nil || !strings.Contains(verr.Hint, "ask the operator to run `rta cert trust`, which shows") ||
		strings.Contains(verr.Hint, "rta cert trust "+strings.TrimPrefix(srv.URL, "https://")) {
		t.Errorf("an agent's refusal: %s", told(verr))
	}
}

// Through a kube: forward the first certificate is trusted as it arrives and
// the call made again with it, once; a server presenting another after that
// is refused, the one trusted kept, and the refusal says when it was trusted.
func TestAForwardTrustsTheFirstCertificateAndNoOtherAfterIt(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	coordinate := "kube:lab/vault/svc/vault:8200"
	stub := &serviceStub{}
	c := reaching(stub)

	srv := serving(t)
	var notes []string
	in := plugin.Inputs{Caller: map[string]any{"url": srv.URL}, ProfileName: "lab", Tunnel: plugin.TunnelKube,
		Coordinate: coordinate}
	ctx := tlstrust.Noting(context.Background(), func(note string) { notes = append(notes, note) })
	if v, err := c.Run(ctx, plugin.ResolveRequest(c, in, false, false).WithSurface(plugin.SurfaceMCP)); err != nil || v == nil ||
		stub.calls != 2 || stub.trusted[0] != nil || len(stub.trusted[1]) != 1 {
		t.Fatalf("a first call through the forward: %v after %d calls", err, stub.calls)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "trusted the certificate "+coordinate+" presented, as it arrived") {
		t.Errorf("the call's surface heard %q", notes)
	}
	servers, _ := tlstrust.List()
	if len(servers) != 1 || servers[0].Server != coordinate || servers[0].Trusted == nil ||
		servers[0].Trusted.How != tlstrust.ByForward || servers[0].Trusted.Profile != "lab" {
		t.Fatalf("kept: %+v", servers)
	}

	// The pod is replaced by one with a certificate of its own.
	stub.calls = 0
	other := serving(t)
	_, verr := healthAt(c, other.URL, plugin.SurfaceCLI, coordinate)
	if codeOf(verr) != "vault.tls.changed" || stub.calls != 1 ||
		!strings.Contains(verr.Hint, "the one it replaces was trusted") || !strings.Contains(verr.Hint, "as it arrived through a forward") ||
		!strings.Contains(verr.Hint, "`rta cert trust "+coordinate+"` shows what it presents now beside it") {
		t.Errorf("another certificate through the forward: %s after %d calls", told(verr), stub.calls)
	}
	servers, _ = tlstrust.List()
	if len(servers) != 1 || servers[0].Pending == nil ||
		tlstrust.Fingerprint(servers[0].Trusted.Chain) == tlstrust.Fingerprint(servers[0].Pending.Chain) {
		t.Errorf("kept after the change: %+v", servers)
	}
}

// An ssh: forward is no kube: one: its last hop crosses the network beyond the
// jump host, so its first certificate waits for the operator as a direct
// server's does.
func TestAnSSHForwardWaitsForTheOperatorsWord(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	srv := serving(t)
	stub := &serviceStub{}
	c := reaching(stub)
	in := plugin.Inputs{Caller: map[string]any{"url": srv.URL}, ProfileName: "bastion", Tunnel: plugin.TunnelSSH,
		Coordinate: "ssh:ops@jump.internal/vault.internal:8200"}
	_, err := c.Run(context.Background(), plugin.ResolveRequest(c, in, false, false).WithSurface(plugin.SurfaceCLI))
	var verr *view.Error
	if !errors.As(err, &verr) || !strings.HasPrefix(verr.Code, "vault.tls.") || stub.calls != 1 {
		t.Fatalf("a first call through ssh: %v after %d calls", err, stub.calls)
	}
	servers, _ := tlstrust.List()
	if len(servers) != 1 || servers[0].Trusted != nil || servers[0].Pending == nil ||
		servers[0].Server != "ssh:ops@jump.internal/vault.internal:8200" {
		t.Errorf("kept: %+v", servers)
	}
}

// A store rta did not write trusts nothing and keeps nothing, and the refusal
// says so: at a terminal why, to an agent that it is the operator's.
func TestAStoreRtaDidNotWriteKeepsNothing(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if err := os.WriteFile(tlstrust.Path(), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := serving(t)
	stub := &serviceStub{}
	_, verr := healthAt(reaching(stub), srv.URL, plugin.SurfaceCLI, "kube:lab/vault/svc/vault:8200")
	if verr == nil || !strings.Contains(verr.Hint, "rta could not keep the certificate it presented to be trusted") ||
		stub.calls != 1 {
		t.Errorf("a call beside a store rta did not write: %s after %d calls", told(verr), stub.calls)
	}
	_, verr = healthAt(reaching(&serviceStub{}), srv.URL, plugin.SurfaceMCP, "")
	if verr == nil || !strings.Contains(verr.Hint, "for the operator to trust (core.trust.unsealed)") {
		t.Errorf("an agent's call beside it: %s", told(verr))
	}
}

// readingStub is a plugin process that only notes what a form's read and a
// completion carried of what was trusted.
type readingStub struct {
	rtav1.PluginServiceClient
	prefilled, suggested [][]byte
}

func (s *readingStub) Prefill(_ context.Context, req *rtav1.PrefillRequest, _ ...grpc.CallOption) (*rtav1.PrefillResponse, error) {
	s.prefilled = req.GetTrusted()
	return &rtav1.PrefillResponse{}, nil
}

func (s *readingStub) Suggest(_ context.Context, req *rtav1.SuggestRequest, _ ...grpc.CallOption) (*rtav1.SuggestResponse, error) {
	s.suggested = req.GetTrusted()
	return &rtav1.SuggestResponse{}, nil
}

// A form reads the record from the server its call will write to, and a
// completion lists what a call there would find, so each is handed what was
// trusted for that server — and remembers nothing of its own.
func TestAFormAndACompletionAreHandedWhatWasTrusted(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	presented := [][]byte{selfSignedDER(t)}
	if _, _, verr := tlstrust.Record("vault", "vault.internal:8200", "", presented, true); verr != nil {
		t.Fatal(verr)
	}
	stub := &readingStub{}
	c := &Client{stub: stub}
	inputs := append(vault.Fields(), plugin.Field{Name: "path", Type: plugin.String})
	dst := plugin.Capability{ID: "vault.kv.set", Inputs: inputs}
	raw := &rtav1.Capability{HasPrefill: true, Inputs: make([]*rtav1.Field, len(inputs))}
	for i := range raw.Inputs {
		raw.Inputs[i] = &rtav1.Field{HasSuggest: inputs[i].Name == "path"}
	}
	c.attach(&dst, raw)
	req := plugin.ResolveRequest(dst, plugin.Inputs{Caller: map[string]any{"url": "https://vault.internal:8200"}}, false, false)
	if _, err := dst.Prefill(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	for i := range dst.Inputs {
		if dst.Inputs[i].Name == "path" {
			dst.Inputs[i].Suggest(context.Background(), req)
		}
	}
	if len(stub.prefilled) != 1 || len(stub.suggested) != 1 {
		t.Errorf("a form carried %d certificates and a completion %d, want the one trusted", len(stub.prefilled), len(stub.suggested))
	}
}

// selfSignedDER is a certificate, DER, nothing on this machine vouches for.
func selfSignedDER(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: mathbig.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "vault"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// A call reaching no server is the plugin's alone: nothing is read or kept.
func TestACallReachingNoServerTouchesNoTrust(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	stub := &countingStub{}
	c := &Client{stub: stub}
	dst := plugin.Capability{ID: "demo.run", Inputs: []plugin.Field{{Name: "note", Type: plugin.String}}}
	c.attach(&dst, &rtav1.Capability{})
	if _, err := dst.Run(context.Background(), plugin.NewRequest(map[string]any{"note": "x"}, false, false)); err != nil || stub.calls != 1 {
		t.Errorf("%v after %d calls", err, stub.calls)
	}
	if _, err := os.Stat(tlstrust.Path()); !os.IsNotExist(err) {
		t.Errorf("a store was touched: %v", err)
	}
}
