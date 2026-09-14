package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/grant"
)

// The token file is the whole trust anchor of the static-token path and the
// operator writes it by hand. Whatever it holds, a file that loads yields
// only tokens long enough to stand on the wire and labels the ledger can
// carry — and a file that does not is refused rather than half-read.
func FuzzLoadTokenFile(f *testing.F) {
	for _, seed := range []string{
		"claude 0123456789abcdefghij\n", "# comment\nwork 0123456789abcdefghij\n",
		"short abc\n", "three words here\n", "dup 0123456789abcdefghij\ndup2 0123456789abcdefghij\n",
		"", "\n\n", "label\ttoken0123456789abcdef",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "tokens")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		tokens, _, err := LoadTokenFile(path)
		if err != nil {
			return
		}
		if len(tokens) == 0 {
			t.Fatal("a file with no tokens loaded")
		}
		for token, label := range tokens {
			if len(token) < minTokenLen {
				t.Fatalf("a %d-character token loaded under %q", len(token), label)
			}
			if verr := grant.CheckAgent(label); verr != nil {
				t.Fatalf("label %q loaded and is not one the ledger accepts: %v", label, verr)
			}
		}
	})
}
