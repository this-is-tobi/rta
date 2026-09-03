package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk/sdktest"
)

// sdktest is the definition of "a correct plugin" — no exemption for vault.
//
// Called with no inputs it drove one capability of fourteen — vault.snapshot,
// the only mutating one whose inputs all have defaults — and passed. Behind
// that, vault.kv.set really wrote a new secret version under --dry-run,
// vault.wrap.set really minted a live single-use token and printed it, and
// vault.transit.encrypt really used the key.
func TestConformance(t *testing.T) {
	sdktest.Check(t, Plugin(), sdktest.WithInputs(conformanceInputs))
}

// conformanceInputs points every mutating capability at an address nothing is
// listening on, so a dry run that stops being dry fails here as a refused
// connection rather than as a write to somebody's Vault.
func conformanceInputs(dir string) map[string]map[string]any {
	conn := func(m map[string]any) map[string]any {
		m["address"] = "http://127.0.0.1:1"
		m["token"] = "conformance"
		return m
	}
	return map[string]map[string]any{
		"vault.kv.get":          conn(map[string]any{"path": "app/db"}),
		"vault.kv.set":          conn(map[string]any{"path": "app/db", "data": []string{"password=s3cret"}}),
		"vault.transit.encrypt": conn(map[string]any{"key": "app", "plaintext": "hello"}),
		"vault.transit.decrypt": conn(map[string]any{"key": "app", "ciphertext": "vault:v1:xxxx"}),
		"vault.wrap.set":        conn(map[string]any{"data": []string{"password=s3cret"}}),
		"vault.wrap.get":        conn(map[string]any{"wrapping-token": "hvs.conformance"}),
		// Already drivable from its defaults, but its default --out is a path
		// in the operator's own home. Pointed inside dir so the rule that
		// watches for a stray write is watching the place it would land.
		"vault.snapshot": conn(map[string]any{"out": filepath.Join(dir, "vault.snap")}),
		// The input file lives outside dir on purpose: sdktest watches dir
		// for stray writes, and a fixture pre-created there would read as
		// one. os.MkdirTemp because this function has no *testing.T; the OS
		// temp dir's own cleanup owns the leftover.
		"vault.restore": conn(map[string]any{"file": restoreFixture()}),
	}
}

func restoreFixture() string {
	dir, err := os.MkdirTemp("", "rta-vault-restore")
	if err != nil {
		return "unwritable"
	}
	path := filepath.Join(dir, "vault.snap")
	if err := os.WriteFile(path, []byte("archive"), 0o600); err != nil {
		return "unwritable"
	}
	return path
}

// req builds a resolved request the way the host would, against the named
// capability's own declared inputs (connFields included, via cap) — so
// these test the values a handler actually sees, matching plugins/pg's own
// req helper.
func req(t *testing.T, capID string, values map[string]any) plugin.Request {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == capID {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
		}
	}
	t.Fatalf("no capability %q", capID)
	return plugin.Request{}
}

// Every classified failure has to say what to do next — the same bar
// plugins/pg's classify holds itself to, against Vault's error shapes.
func TestEveryClassifiedFailureNamesTheNextStep(t *testing.T) {
	r := req(t, "vault.seal.status", map[string]any{"address": "https://vault.internal:8200"})
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"denied", &vaultapi.ResponseError{StatusCode: 403, Errors: []string{"permission denied"}}, "vault.denied"},
		{"not found", &vaultapi.ResponseError{StatusCode: 404}, "vault.notfound"},
		{"bad request", &vaultapi.ResponseError{StatusCode: 400, Errors: []string{"missing client token"}}, "vault.badrequest"},
		{"sealed", &vaultapi.ResponseError{StatusCode: 412}, "vault.sealed"},
		{"server error", &vaultapi.ResponseError{StatusCode: 500, Errors: []string{"internal error"}}, "vault.request.failed"},
		{"kv secret missing", vaultapi.ErrSecretNotFound, "vault.notfound"},
		{"refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "vault.conn.refused"},
		{"unknown host", &net.DNSError{Err: "no such host", Name: "vault.internal"}, "vault.host.unknown"},
		{"anything else", errors.New("something unexpected"), "vault.conn.failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verr := classify(tc.err, r)
			if verr.Code != tc.code {
				t.Errorf("code = %q, want %q", verr.Code, tc.code)
			}
			if verr.Hint == "" {
				t.Error("no hint")
			}
			if verr.Message == "" {
				t.Error("no message")
			}
		})
	}
}

// A wrapped fmt.Errorf around vaultapi.ErrSecretNotFound — exactly what
// KVv2.Get/Put actually return, per the vendored source — must still
// classify: errors.Is has to see through the %w wrapping, not just match a
// bare sentinel nobody hands it directly.
func TestClassifyUnwrapsAWrappedSecretNotFound(t *testing.T) {
	wrapped := fmt.Errorf("error encountered while reading secret at secret/data/x: %w", vaultapi.ErrSecretNotFound)
	verr := classify(wrapped, req(t, "vault.kv.get", map[string]any{"path": "x"}))
	if verr.Code != "vault.notfound" {
		t.Errorf("code = %q, want vault.notfound", verr.Code)
	}
}

func TestDataFieldsParsesRepeatedKeyValuePairs(t *testing.T) {
	data, verr := dataFields([]string{"user=admin", "pass=hunter2"})
	if verr != nil {
		t.Fatal(verr)
	}
	if data["user"] != "admin" || data["pass"] != "hunter2" {
		t.Errorf("data = %v", data)
	}
}

func TestDataFieldsRejectsAPairWithNoEquals(t *testing.T) {
	_, verr := dataFields([]string{"user"})
	if verr == nil {
		t.Fatal("expected an error for a pair with no '='")
	}
	if verr.Code != "vault.data.invalid" {
		t.Errorf("code = %q", verr.Code)
	}
}

// A value containing '=' itself (a base64 blob, most commonly) must keep
// everything after the first '=' — strings.Cut splits on the first
// occurrence, not something re-derived here, but the case is worth pinning
// since a naive strings.Split(pair, "=") would silently drop the rest.
func TestDataFieldsKeepsEqualsSignsInsideTheValue(t *testing.T) {
	data, verr := dataFields([]string{"cert=MIIB==Q=="})
	if verr != nil {
		t.Fatal(verr)
	}
	if data["cert"] != "MIIB==Q==" {
		t.Errorf("cert = %q", data["cert"])
	}
}

func TestCellRendersEveryJSONShapeVaultActuallyReturns(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want string
	}{
		{"nil", nil, ""},
		{"string", "root", "root"},
		{"bool", true, "true"},
		{"number", float64(300), "300"},
		{"string slice", []interface{}{"default", "admin"}, "default, admin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cell(tc.in); got != tc.want {
				t.Errorf("cell(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Every capability that reaches Vault reaches off the box, so cap must have
// forced NoPreview on all of them — the property plugins/pg's own
// overview_test.go pins the same way, since it is what keeps the automatic
// dashboard from deciding, on its own, that a live deployment is worth
// polling.
func TestEveryCapabilityIsNoPreview(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if !c.NoPreview {
			t.Errorf("%s: NoPreview = false, want true — every capability here reaches off the box", c.ID)
		}
	}
}

// The capabilities called out explicitly as "Both Write+NeedsGrant"
// (kv.get's reveal, kv.put's overwrite, transit.decrypt's reveal, and the
// two wrap capabilities) must actually declare it — a design note is not an
// enforcement mechanism, the struct field is.
func TestWriteAndDestructiveCapabilitiesNeedAGrant(t *testing.T) {
	want := map[string]bool{
		"vault.kv.get":          true,
		"vault.kv.set":          true,
		"vault.transit.encrypt": false, // uses the caller's own plaintext, nothing revealed
		"vault.transit.decrypt": true,
		"vault.wrap.set":        true,
		"vault.wrap.get":        true,
		"vault.seal.status":     false,
		"vault.kv.list":         false,
		"vault.kv.tree":         false, // the same names kv.list gives, in one call instead of many
		"vault.token.status":    false,
		"vault.lease.show":      false,
		"vault.policy.list":     false,
		"vault.policy.get":      false,
		"vault.overview":        false,
		// Refuses SurfaceMCP outright rather than taking a grant: a snapshot of
		// the whole Vault has no blast radius a grant could name. NeedsGrant stays
		// unset for keys.backup's reason — a grant that can never be exercised is
		// an entry in `grant list` that means nothing.
		"vault.snapshot": false,
		// Its mirror: everything arriving instead of everything leaving, and
		// the same reasoning for the unset NeedsGrant.
		"vault.restore": false,
	}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		seen[c.ID] = true
		wantGrant, ok := want[c.ID]
		if !ok {
			t.Errorf("%s: not accounted for in this test's table", c.ID)
			continue
		}
		if c.NeedsGrant != wantGrant {
			t.Errorf("%s: NeedsGrant = %v, want %v", c.ID, c.NeedsGrant, wantGrant)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s: declared in this test's table but not in Plugin()", id)
		}
	}
}
