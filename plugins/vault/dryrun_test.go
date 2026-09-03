package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// The assertion here is on the wire, not on the filesystem. The conformance
// suite watches a directory and cannot see a request that leaves the machine,
// which is exactly what all three defects here were. recordingVault
// (complete_test.go) already records every request line and 404s anything it
// has no route for, so the routes below are only what an allowed *read* needs
// to get past its own parsing.
var dryRunRoutes = map[string]string{
	"/v1/secret/data/app/db":  `{"data":{"data":{"password":"s3cret"},"metadata":{"version":1}}}`,
	"/v1/transit/decrypt/app": `{"data":{"plaintext":"aGVsbG8="}}`,
	"/v1/sys/wrapping/lookup": `{"data":{"creation_path":"sys/wrapping/wrap","creation_ttl":300}}`,
}

func dryReq(t *testing.T, capID, address string, values map[string]any) plugin.Request {
	t.Helper()
	values["address"] = address
	values["token"] = "test"
	for _, c := range Plugin().Capabilities {
		if c.ID == capID {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), true, false)
		}
	}
	t.Fatalf("no capability %q", capID)
	return plugin.Request{}
}

// **Every mutating capability, driven under --dry-run, against a Vault that
// fails the test if it is written to.**
//
// Three of these shipped doing the thing they promised to describe:
// vault.kv.set created a real new secret version, vault.wrap.set minted a
// live single-use cubbyhole token and printed it — a credential that is spent
// the moment it exists — and vault.transit.encrypt used the key. All three
// sat behind a green TestConformance for as long as the plugin has existed,
// because with no inputs the suite drove one capability of fourteen.
func TestNoMutatingCapabilityActsUnderDryRun(t *testing.T) {
	// A preview may reach Vault when reaching it is how it describes what it
	// would do — but only at the one path that does the describing, named
	// here. The HTTP method cannot carry this rule: Vault reads with PUT
	// (sys/wrapping/lookup, transit/decrypt) as readily as with GET, so
	// "no writes" and "no POSTs" are different sentences and only the first
	// one is the rule. The reason is spelled out because an exemption nobody
	// can argue with is an exemption nobody reads.
	previewReads := map[string]struct{ path, why string }{
		"vault.kv.get": {"/v1/secret/data/app/db",
			"revealing is the capability; a dry run that showed nothing would describe nothing"},
		"vault.transit.decrypt": {"/v1/transit/decrypt/app",
			"same — the plaintext is what this returns, and decrypting does not consume anything"},
		"vault.wrap.get": {"/v1/sys/wrapping/lookup",
			"looks the token up instead of unwrapping it, so the token's one read survives the preview"},
	}
	values := map[string]map[string]any{
		"vault.kv.get":          {"path": "app/db"},
		"vault.kv.set":          {"path": "app/db", "data": []string{"password=s3cret"}},
		"vault.transit.encrypt": {"key": "app", "plaintext": "hello"},
		"vault.transit.decrypt": {"key": "app", "ciphertext": "vault:v1:xxxx"},
		"vault.wrap.set":        {"data": []string{"password=s3cret"}},
		"vault.wrap.get":        {"wrapping-token": "hvs.conformance"},
	}
	for _, c := range Plugin().Capabilities {
		if c.Safety != plugin.Write && c.Safety != plugin.Destructive {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			srv, asked := recordingVault(t, dryRunRoutes)
			dir := t.TempDir()
			v := values[c.ID]
			if v == nil {
				v = map[string]any{}
			}
			if c.ID == "vault.snapshot" {
				v["out"] = filepath.Join(dir, "vault.snap")
			}
			if c.ID == "vault.restore" {
				// A separate TempDir, not dir: the file check runs before the
				// dry-run branch, so the input must exist — and pre-creating
				// it in dir would read as the stray write this test hunts.
				v["file"] = archiveOnDisk(t, "archive")
			}

			view, err := c.Run(t.Context(), dryReq(t, c.ID, srv.URL, v))
			if err != nil {
				t.Fatalf("dry run failed: %v", err)
			}
			if view == nil {
				t.Fatal("dry run returned nothing — a preview that says nothing is not a preview")
			}
			allow, allowed := previewReads[c.ID]
			for _, h := range asked() {
				if !allowed {
					t.Errorf("--dry-run reached Vault: %s", h)
					continue
				}
				if _, path, _ := strings.Cut(h, " "); path != allow.path {
					t.Errorf("--dry-run may reach %s (%s) and reached %s instead",
						allow.path, allow.why, h)
				}
			}
			if entries, _ := os.ReadDir(dir); len(entries) > 0 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("--dry-run wrote %v", names)
			}
		})
	}
}
