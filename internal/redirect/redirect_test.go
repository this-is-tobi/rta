package redirect

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A redirect is followed to any host and refused down to plain http or when it
// would turn a write into a read; nothing is sent to the address refused.
func TestARedirectIsFollowedAnywhereButDownToPlainHTTPOrOffAWrite(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHits.Add(1)
		_, _ = io.WriteString(w, "plain")
	}))
	t.Cleanup(plain.Close)
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, r.Method+" "+string(body))
	}))
	t.Cleanup(other.Close)
	start := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/elsewhere":
			http.Redirect(w, r, other.URL+"/landed", http.StatusFound)
		case "/down":
			http.Redirect(w, r, plain.URL+"/landed", http.StatusFound)
		case "/kept":
			http.Redirect(w, r, other.URL+"/landed", http.StatusTemporaryRedirect)
		case "/rewritten":
			http.Redirect(w, r, other.URL+"/landed", http.StatusFound)
		}
	}))
	t.Cleanup(start.Close)
	// Both test servers' certificates, as a client of this machine would
	// trust a real host's.
	pool := start.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	pool.AddCert(other.Certificate())
	client := start.Client()
	client.CheckRedirect = Follow

	do := func(method, path, body string) (string, error) {
		req, err := http.NewRequest(method, start.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		got, _ := io.ReadAll(resp.Body)
		return string(got), nil
	}
	if got, err := do(http.MethodGet, "/elsewhere", ""); err != nil || got != "GET " {
		t.Errorf("another https host = %q, %v; want it followed", got, err)
	}
	if got, err := do(http.MethodPost, "/kept", "q"); err != nil || got != "POST q" {
		t.Errorf("a 307 on a POST = %q, %v; want the POST and its body followed", got, err)
	}
	if _, err := do(http.MethodGet, "/down", ""); err == nil || !strings.Contains(err.Error(), "over plain http") {
		t.Errorf("https down to http = %v, want it refused", err)
	}
	if _, err := do(http.MethodPost, "/rewritten", "q"); err == nil || !strings.Contains(err.Error(), "again as a GET") {
		t.Errorf("a 302 on a POST = %v, want it refused", err)
	}
	if n := plainHits.Load(); n != 0 {
		t.Errorf("%d requests went to the plain address", n)
	}
}

// What a refusal names is where the redirect came from and went, never the
// path or query, where a signed download carries its token.
func TestARefusalNamesNoPathOrQuery(t *testing.T) {
	from, _ := http.NewRequest(http.MethodGet, "https://releases.example/asset?sig=SECRET", nil)
	to, _ := http.NewRequest(http.MethodGet, "http://blobs.example/x?token=SECRET", nil)
	err := Follow(to, []*http.Request{from})
	if err == nil || strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "http://blobs.example") {
		t.Errorf("refusal = %v", err)
	}
}
