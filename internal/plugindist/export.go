package plugindist

import (
	"context"
	"io"
	"os"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The three primitives a release-asset installer needs, exported for the
// pkg built-in and nobody else yet. They are the very functions plugin
// install runs — fetch and hash in one pass, extract exactly one regular
// file from a .tar.gz under a decompressed-size cap, hash what landed — and
// that is the point: a binary somebody's tool installs into ~/.local/bin
// deserves the same evidence-before-placement path as a plugin rta will
// launch. Their errors are coded plugin.install.*, and a caller with its own
// vocabulary re-wraps the message rather than the code.

// Fetch downloads an https URL into dst, hashing as it writes, and returns
// the lowercase hex sha256 of what landed. The transfer is capped and timed
// the way a plugin artifact's is.
func Fetch(ctx context.Context, rawURL string, dst *os.File) (string, *view.Error) {
	return fetchArtifact(ctx, rawURL, dst)
}

// ExtractMember streams exactly one named regular file out of a .tar.gz.
func ExtractMember(archive io.Reader, member string, dst io.Writer) (int64, *view.Error) {
	return extractMember(archive, member, dst)
}

// DigestFile is rta's own sha256 of a file on disk, in the spelling every
// pin uses: 64 lowercase hex characters, no prefix.
func DigestFile(path string) (string, *view.Error) {
	return digestFile(path)
}
