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
	"path/filepath"
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
	var (
		body           io.ReadCloser
		registryDigest string
	)
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
		f, err := os.Open(localPath(u))
		if err != nil {
			return "", view.Errorf("plugin.install.fetch", "%v", err)
		}
		body = f
	case "oci":
		ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
		var verr *view.Error
		// The registry's own claim about these bytes, kept for the comparison
		// below. It is not the index's claim and not rta's measurement — a
		// third statement about the same artifact, from the party actually
		// serving it.
		body, registryDigest, verr = ociBlob(ctx, rawURL)
		if verr != nil {
			return "", verr
		}
	default:
		return "", view.Errorf("plugin.install.fetch",
			"%q: the schemes are https, file and oci", rawURL)
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
	sum := hex.EncodeToString(h.Sum(nil))
	// Checked here rather than left to the caller's comparison against the
	// index. The caller catches an index that lied; this catches a registry
	// serving something its own manifest does not describe, and the two are
	// different failures with different people able to fix them.
	if registryDigest != "" && registryDigest != sum {
		return "", ociMismatch(rawURL, registryDigest, sum)
	}
	return sum, nil
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

// localPath is the file a file:// URL names, in the spelling this operating
// system opens.
//
// **A URL path is not a filesystem path, and only one platform lets you
// pretend otherwise.** A file URL's path is always slash-separated and always
// starts with "/", so on Unix it happens to be the path already and `u.Path`
// was passed to os.Open unchanged. The absolute form on Windows is
// `file:///C:/dir/x`, whose path parses as `/C:/dir/x` — a leading slash in
// front of a drive letter, which Windows rejects outright: "The filename,
// directory name, or volume label syntax is incorrect."
//
// So `file://` — a scheme checkArtifactURL admits, and the one a local index
// naming a local artifact depends on — could not install anything at all on
// Windows. It was invisible from here because the only tests that exercised
// it built their URLs by concatenation and were refused a step earlier, for a
// different reason, on that platform alone.
//
// FromSlash after trimming, rather than trimming alone, so the result is
// separated the way the rest of this package spells a path.
func localPath(u *url.URL) string {
	p := u.Path
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
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
