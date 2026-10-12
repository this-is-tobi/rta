package profile

import (
	"strings"
	"testing"
)

// The refusal of a key nothing reads under a plugin entry says which keys an
// entry does take, and Lookup's list left out `secrets-from`, which Check's own
// lists and the loader accepts — so an operator who misspelled it was told it
// was not one of the keys, in the sentence that was meant to say which were.
func TestLookupNamesEveryKeyAPluginEntryTakes(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      pg:
        sets:
          host: s.internal
`)
	_, verr := Lookup(cfg, pgCap(), "staging", pgRegistry(t))
	if verr == nil || verr.Code != "core.profile.unknownkey" {
		t.Fatalf("a misspelt key under a plugin entry was not refused as one nothing reads: %v", verr)
	}
	for _, key := range []string{"set", "only", "secrets", "secrets-from", "kube", "ssh", "tunnelTLS"} {
		if !strings.Contains(verr.Hint, key) {
			t.Errorf("the hint does not name %s among the keys an entry takes: %q", key, verr.Hint)
		}
	}
}
