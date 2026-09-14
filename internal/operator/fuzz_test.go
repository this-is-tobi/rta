package operator

import (
	"os"
	"path/filepath"
	"testing"
)

// The roster decides who may operate a server and the operator writes it by
// hand; a line that does not parse must refuse the whole file, never enroll
// a key under a role it did not say. Held against arbitrary bytes: a file
// that loads has as many operators as entries, and a file that does not
// leaves nothing enrolled.
func FuzzLoadRoster(f *testing.F) {
	for _, seed := range []string{
		"tobi AAAAC3NzaC1lZDI1NTE5AAAAIExample\n", "dash AAAA role=read\n", "old AAAA expires=2020-01-01\n",
		"bad AAAA role=admin\n", "# comment\n", "", "one\n", "a b c d\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "operators")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		r, _, err := LoadRoster(path)
		if err != nil {
			if r.Len() != 0 {
				t.Fatalf("a refused roster still enrolled %d keys", r.Len())
			}
			return
		}
		if got := len(r.Operators()); got != r.Len() {
			t.Fatalf("Operators() lists %d and Len() counts %d", got, r.Len())
		}
	})
}
