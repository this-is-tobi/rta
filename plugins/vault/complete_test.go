package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The recipes behind live completion, proven against a server that records
// what was asked: names only come back, only read-class requests go out
// (GET, and LIST — Vault's names-only verb), the path walk stays inside the
// typed folder, and every failure is silence rather than an error.

// recordingVault serves canned JSON per path and records every request line.
func recordingVault(t *testing.T, routes map[string]string) (*httptest.Server, func() []string) {
	t.Helper()
	var (
		mu    sync.Mutex
		asked []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		// The client may or may not keep a trailing slash; the fixture should
		// not care which.
		body, ok := routes[r.URL.Path]
		if !ok {
			body, ok = routes[strings.TrimSuffix(r.URL.Path, "/")]
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

// readOnly asserts every recorded request was GET or LIST — a completion
// must never write, whatever the plugin author wires it to.
func readOnly(t *testing.T, asked []string) {
	t.Helper()
	for _, line := range asked {
		if !strings.HasPrefix(line, "GET ") && !strings.HasPrefix(line, "LIST ") {
			t.Errorf("a completion sent %q — listings are GET or LIST, nothing else", line)
		}
	}
}

func TestSuggestMountsFiltersByEngineAndTrimsTheSlash(t *testing.T) {
	srv, asked := recordingVault(t, map[string]string{
		"/v1/sys/mounts": `{"data":{
			"secret/":{"type":"kv","options":{"version":"2"}},
			"kv-apps/":{"type":"kv","options":{"version":"2"}},
			"transit/":{"type":"transit"},
			"sys/":{"type":"system"}}}`,
	})
	r := req(t, "vault.kv.list", map[string]any{"address": srv.URL, "token": "t"})

	if got := suggestMounts("kv")(context.Background(), r); len(got) != 2 ||
		got[0] != "kv-apps" || got[1] != "secret" {
		t.Errorf("kv mounts = %v, want the two kv engines, slash trimmed, sorted", got)
	}
	if got := suggestMounts("transit")(context.Background(), r); len(got) != 1 || got[0] != "transit" {
		t.Errorf("transit mounts = %v, want the one transit engine", got)
	}
	readOnly(t, asked())
}

func TestSuggestPathsWalksTheTypedFolder(t *testing.T) {
	srv, asked := recordingVault(t, map[string]string{
		"/v1/secret/metadata/team": `{"data":{"keys":["db-creds","ci/"]}}`,
	})
	r := req(t, "vault.kv.get", map[string]any{
		"address": srv.URL, "token": "t", "path": "team/d",
	})

	got := suggestPaths(context.Background(), r)
	if len(got) != 2 || got[0] != "team/ci/" || got[1] != "team/db-creds" {
		t.Fatalf("paths = %v, want the folder's entries re-rooted onto team/, the folder "+
			"keeping its trailing slash", got)
	}
	listed := false
	for _, line := range asked() {
		if strings.Contains(line, "/v1/secret/metadata/team") &&
			(strings.HasPrefix(line, "LIST ") || strings.Contains(line, "list=true")) {
			listed = true
		}
	}
	if !listed {
		t.Errorf("no LIST of the typed folder; asked: %v — anything else walks the wrong level "+
			"or reads values", asked())
	}
	readOnly(t, asked())
}

func TestSuggestTransitKeysAndPoliciesListNames(t *testing.T) {
	srv, asked := recordingVault(t, map[string]string{
		"/v1/transit/keys":     `{"data":{"keys":["app-signing","backup"]}}`,
		"/v1/sys/policies/acl": `{"data":{"keys":["default","read-apps"]}}`,
		"/v1/sys/policy":       `{"data":{"policies":["default","read-apps"]}}`,
	})
	r := req(t, "vault.transit.encrypt", map[string]any{"address": srv.URL, "token": "t"})

	if got := suggestTransitKeys(context.Background(), r); len(got) != 2 || got[0] != "app-signing" {
		t.Errorf("transit keys = %v, want the two names", got)
	}
	pr := req(t, "vault.policy.get", map[string]any{"address": srv.URL, "token": "t"})
	if got := suggestPolicies(context.Background(), pr); len(got) != 2 || got[0] != "default" {
		t.Errorf("policies = %v, want the two names", got)
	}
	readOnly(t, asked())
}

// Every failure is silence: a completion that cannot answer must slow
// nobody down, and the run that follows classifies the same failure.
func TestSuggestionsAreSilentOnEveryFailure(t *testing.T) {
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
	}))
	t.Cleanup(denied.Close)

	for _, tc := range []struct {
		name    string
		address string
	}{
		{"denied", denied.URL},
		{"nothing listening", "http://127.0.0.1:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := req(t, "vault.kv.get", map[string]any{
				"address": tc.address, "token": "t", "path": "team/",
			})
			if got := suggestMounts("kv")(context.Background(), r); got != nil {
				t.Errorf("suggestMounts = %v, want silence", got)
			}
			if got := suggestPaths(context.Background(), r); got != nil {
				t.Errorf("suggestPaths = %v, want silence", got)
			}
			if got := suggestTransitKeys(context.Background(), r); got != nil {
				t.Errorf("suggestTransitKeys = %v, want silence", got)
			}
			if got := suggestPolicies(context.Background(), r); got != nil {
				t.Errorf("suggestPolicies = %v, want silence", got)
			}
		})
	}
}

// The wiring is part of the contract: every input whose values exist
// server-side declares Live with a Suggest, so a refactor cannot silently
// put a service listing on the keystroke channel — or drop the completion —
// without failing here. wrap's ttl stays local on purpose: its Suggest is a
// static list.
func TestServerSideInputsDeclareLive(t *testing.T) {
	wantLive := map[string]bool{"mount": true, "path": true, "key": true, "name": true}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		for _, f := range c.Inputs {
			if wantLive[f.Name] {
				seen[f.Name] = true
				if !f.Live || f.Suggest == nil {
					t.Errorf("%s: input %q is not Live with a Suggest", c.ID, f.Name)
				}
			} else if f.Live {
				t.Errorf("%s: input %q declares Live, and nothing here lists it server-side", c.ID, f.Name)
			}
		}
	}
	for name := range wantLive {
		if !seen[name] {
			t.Errorf("no capability declares input %q — the wiring this test pins moved", name)
		}
	}
}
