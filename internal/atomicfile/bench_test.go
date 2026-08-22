package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

// config.yaml, roughly, since that is the file written most often.
var body = []byte("dashboard:\n  order: [todo.list, note.list, sys.overview]\n")

// The three of these together are why Write does not call Sync, kept
// runnable so the claim in atomicfile.go is reproducible rather than
// remembered. On the machine this was decided on (macOS, APFS, NVMe):
//
//	BenchmarkWrite             207µs
//	BenchmarkWriteSyncing     5338µs     <- 26x, and F_FULLFSYNC is why
//	BenchmarkOsWriteFile        54µs     <- what config.Write used to do
//	BenchmarkPublishAndRelease 394µs     <- the grant lock, both halves
//
// Write is a keystroke in the dashboard (`[`, `]`, `H` each save the
// arrangement) and half of every gated MCP call (the grant lock). 5ms of
// blocking disk on either is not a trade this package gets to make quietly.
// The 54µs line is the one worth keeping in view too: atomicity is not free
// either, it is just affordable — four times the cost of a truncating write,
// for a file that can no longer be found empty.
func BenchmarkWrite(b *testing.B) {
	target := filepath.Join(b.TempDir(), "config.yaml")
	for b.Loop() {
		if err := Write(target, body, 0o644); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWriteSyncing(b *testing.B) {
	dir := b.TempDir()
	target := filepath.Join(dir, "config.yaml")
	for b.Loop() {
		tmp, err := os.CreateTemp(dir, ".x-*")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := tmp.Write(body); err != nil {
			b.Fatal(err)
		}
		if err := tmp.Sync(); err != nil {
			b.Fatal(err)
		}
		tmp.Close()
		os.Chmod(tmp.Name(), 0o644)
		if err := os.Rename(tmp.Name(), target); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOsWriteFile(b *testing.B) {
	target := filepath.Join(b.TempDir(), "config.yaml")
	for b.Loop() {
		if err := os.WriteFile(target, body, 0o644); err != nil {
			b.Fatal(err)
		}
	}
}

// The grant lock, acquired and released — twice per gated MCP call.
func BenchmarkPublishAndRelease(b *testing.B) {
	target := filepath.Join(b.TempDir(), "grants.json.lock")
	token := []byte("12345 6f1b3c9d2e8a4b70\n")
	for b.Loop() {
		if _, err := Publish(target, token, 0o600); err != nil {
			b.Fatal(err)
		}
		os.Remove(target)
	}
}
