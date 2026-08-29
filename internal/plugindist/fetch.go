package plugindist

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// artifactCap bounds a download. The largest plugin this repo builds is
// ~40 MB; a quarter gigabyte is generous headroom for a statically linked
// binary with room to grow, and a bound at all is the point — an index is
// somebody else's file, and "fill the disk" must not be among the things a
// hostile one can claim rta into doing. A var only so tests can prove the
// bound without writing a quarter gigabyte.
var artifactCap = int64(256 << 20)

// fetchTimeout bounds the whole transfer. Long enough for a large artifact
// on a slow line; the context still wins when the caller's is sooner.
const fetchTimeout = 5 * time.Minute

// fetchArtifact downloads url into dst, hashing as it writes, and returns the
// hex sha256 of what landed. The hash is computed by rta from the bytes —
// never trusted from anywhere — and the caller compares it against the
// index's claim.
func fetchArtifact(ctx context.Context, rawURL string, dst *os.File) (string, *view.Error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", view.Errorf("plugin.install.fetch", "%q does not parse as a URL", rawURL)
	}
	var body io.ReadCloser
	switch u.Scheme {
	case "https":
		ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", view.Errorf("plugin.install.fetch", "%v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", view.Errorf("plugin.install.fetch", "fetching %s: %v", rawURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", view.Errorf("plugin.install.fetch", "fetching %s: %s", rawURL, resp.Status)
		}
		body = resp.Body
	case "file":
		f, err := os.Open(u.Path)
		if err != nil {
			return "", view.Errorf("plugin.install.fetch", "%v", err)
		}
		body = f
	case "oci":
		return "", view.Errorf("plugin.install.oci",
			"%s names an oci:// artifact, and that transport is not built yet", rawURL).
			WithHint("an index may claim an oci:// artifact, but rta cannot fetch one yet; " +
				"ask the index for an https or file URL until the transport lands")
	default:
		return "", view.Errorf("plugin.install.fetch", "%q: the schemes are https and file", rawURL)
	}
	defer body.Close()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(body, artifactCap+1))
	if err != nil {
		return "", view.Errorf("plugin.install.fetch", "reading %s: %v", rawURL, err)
	}
	if n > artifactCap {
		return "", view.Errorf("plugin.install.fetch",
			"%s is over the %d MB artifact cap", rawURL, artifactCap>>20)
	}
	if n == 0 {
		return "", view.Errorf("plugin.install.fetch", "%s is empty", rawURL)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractMember streams exactly one named member out of a .tar.gz into dst.
//
// One member and nothing else is most of the security posture: no directory
// is ever created, no other file is ever written, no symlink or hardlink is
// ever followed, so the tar-slip and bomb families have nothing to grab. The
// member must be a regular file, and the size cap applies to the decompressed
// stream because gzip is the amplifier in every archive bomb.
func extractMember(archive io.Reader, member string, dst io.Writer) (int64, *view.Error) {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return 0, view.Errorf("plugin.install.archive", "not a gzip archive: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	want := path.Clean(member)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return 0, view.Errorf("plugin.install.archive",
				"the archive holds no %q — the index's bin: claim does not match its own artifact", member)
		}
		if err != nil {
			return 0, view.Errorf("plugin.install.archive", "reading the archive: %v", err)
		}
		if path.Clean(strings.TrimPrefix(hdr.Name, "./")) != want {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return 0, view.Errorf("plugin.install.archive",
				"%q is not a regular file in the archive", member)
		}
		n, err := io.Copy(dst, io.LimitReader(tr, artifactCap+1))
		if err != nil {
			return 0, view.Errorf("plugin.install.archive", "extracting %q: %v", member, err)
		}
		if n > artifactCap {
			return 0, view.Errorf("plugin.install.archive",
				"%q decompresses past the %d MB cap", member, artifactCap>>20)
		}
		if n == 0 {
			return 0, view.Errorf("plugin.install.archive", "%q is empty", member)
		}
		return n, nil
	}
}

// digestFile is rta's own hash of a file on disk — the value everything pins
// to, in the same spelling pluginhost computes on every Open.
func digestFile(path string) (string, *view.Error) {
	f, err := os.Open(path)
	if err != nil {
		return "", view.Errorf("plugin.install.digest", "%v", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", view.Errorf("plugin.install.digest", "%v", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
