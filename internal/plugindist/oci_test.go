package plugindist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A registry, small enough to be read in one go and complete enough to make
// the client take every branch of the real handshake: the 401 challenge, the
// token endpoint, the manifest, and a blob served from a different host
// behind a redirect — which is what ghcr actually does.
type fakeRegistry struct {
	*httptest.Server
	blobs *httptest.Server

	repo string
	// blob is what the artifact's bytes are; digest is what the registry
	// *claims* they are, so a test can make the two disagree.
	blob   []byte
	digest string

	// authorized records every Authorization header the blob host saw. It
	// must stay empty: a token that follows a cross-host redirect is a token
	// handed to every blob server a registry cares to name.
	authorized []string
	// tokenScope is the scope the client asked for, so a test can prove it
	// echoed the challenge rather than inventing one.
	tokenScope string
	// manifest, when set, replaces the generated one.
	manifest string
	// indexOnly makes the manifest a multi-platform index that is served only
	// to a client naming the index media types, as a real registry does.
	indexOnly bool
	// manifestStatus, when set, replaces 200 on the tokened manifest read.
	manifestStatus int
}

func newFakeRegistry(t *testing.T, blob []byte) *fakeRegistry {
	t.Helper()
	sum := sha256.Sum256(blob)
	r := &fakeRegistry{repo: "example/rta-plugin-hello", blob: blob,
		digest: "sha256:" + hex.EncodeToString(sum[:])}

	// The blob host, deliberately a second server on a second port so the
	// redirect below is genuinely cross-host.
	r.blobs = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.authorized = append(r.authorized, req.Header.Get("Authorization"))
		w.Write(r.blob)
	}))
	t.Cleanup(r.blobs.Close)

	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/token" {
			r.tokenScope = req.URL.Query().Get("scope")
			json.NewEncoder(w).Encode(map[string]string{"token": "anonymous-pull"})
			return
		}
		if req.Header.Get("Authorization") == "" {
			w.Header().Set("Www-Authenticate", fmt.Sprintf(
				`Bearer realm="%s/token",service="fake",scope="repository:%s:pull"`,
				r.Server.URL, r.repo))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case strings.Contains(req.URL.Path, "/manifests/"):
			if r.manifestStatus != 0 {
				w.WriteHeader(r.manifestStatus)
				return
			}
			// What ghcr really does, measured 2026-09-04: a multi-arch tag
			// asked for with only the image-manifest media types answers 404,
			// not the index. A client that does not name the index types
			// therefore reports "no such artifact" about a tag that plainly
			// exists.
			if r.indexOnly && !strings.Contains(req.Header.Get("Accept"), "image.index") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.manifest != "" {
				io.WriteString(w, r.manifest)
				return
			}
			fmt.Fprintf(w, `{"schemaVersion":2,
				"mediaType":"application/vnd.oci.image.manifest.v1+json",
				"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip",
				"digest":%q,"size":%d}]}`, r.digest, len(r.blob))
		case strings.Contains(req.URL.Path, "/blobs/"):
			http.Redirect(w, req, r.blobs.URL+"/storage", http.StatusTemporaryRedirect)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(r.Server.Close)
	return r
}

// ref is the oci:// URL naming this registry's artifact.
func (r *fakeRegistry) ref() string {
	return "oci://" + strings.TrimPrefix(r.Server.URL, "http://") + "/" + r.repo + ":1.0.0"
}

// overHTTP points the client at the fake registry, which has no certificate.
func overHTTP(t *testing.T) {
	t.Helper()
	original := ociScheme
	ociScheme = "http"
	t.Cleanup(func() { ociScheme = original })
}

// https is not negotiable, and this is the line that says so — the fake
// registry below runs over http and would quietly become the shipped default
// if a cleanup ever stopped running.
func TestARegistryIsReachedOverHTTPS(t *testing.T) {
	if ociScheme != "https" {
		t.Fatalf("ociScheme = %q, and a plugin binary fetched in cleartext is not fetched", ociScheme)
	}
}

// The whole handshake, end to end, against a registry that behaves like the
// real one: challenge, token, manifest, cross-host blob redirect.
func TestAnArtifactIsPulledAnonymouslyThroughTheChallenge(t *testing.T) {
	overHTTP(t)
	body := []byte("the plugin artifact's bytes")
	reg := newFakeRegistry(t, body)

	dst, err := os.Create(filepath.Join(t.TempDir(), "artifact"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	sum, verr := fetchArtifact(context.Background(), reg.ref(), dst)
	if verr != nil {
		t.Fatalf("fetch: %v (hint: %s)", verr, verr.Hint)
	}
	if want := strings.TrimPrefix(reg.digest, "sha256:"); sum != want {
		t.Fatalf("sum = %s, want %s", sum, want)
	}
	landed, err := os.ReadFile(dst.Name())
	if err != nil || string(landed) != string(body) {
		t.Fatalf("landed = %q (%v)", landed, err)
	}
	if reg.tokenScope != "repository:"+reg.repo+":pull" {
		t.Fatalf("token scope = %q, want the one the challenge named", reg.tokenScope)
	}
}

// The security property of following the redirect at all. ghcr sends a blob
// GET to pkg-containers.githubusercontent.com with a presigned URL; a client
// that carried the Authorization header across would hand a registry token to
// every blob host a registry cares to name.
func TestTheRegistryTokenDoesNotFollowTheBlobRedirect(t *testing.T) {
	overHTTP(t)
	reg := newFakeRegistry(t, []byte("bytes"))
	dst, err := os.Create(filepath.Join(t.TempDir(), "artifact"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, verr := fetchArtifact(context.Background(), reg.ref(), dst); verr != nil {
		t.Fatalf("fetch: %v", verr)
	}
	if len(reg.authorized) == 0 {
		t.Fatal("the blob host was never reached, so this proves nothing")
	}
	for _, got := range reg.authorized {
		if got != "" {
			t.Fatalf("the blob host received Authorization %q", got)
		}
	}
}

// Three parties describe the same artifact — the index in its sha256 field,
// the registry in its manifest, and rta from the bytes that arrive. rta
// believes none of them; what this catches is the registry serving something
// its own manifest does not describe, which is a different failure from an
// index that lied and has a different person able to fix it.
func TestARegistryServingOtherBytesThanItDescribesIsRefused(t *testing.T) {
	overHTTP(t)
	reg := newFakeRegistry(t, []byte("bytes"))
	reg.digest = "sha256:" + strings.Repeat("0", 64)

	dst, err := os.Create(filepath.Join(t.TempDir(), "artifact"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	_, verr := fetchArtifact(context.Background(), reg.ref(), dst)
	if verr == nil || verr.Code != "plugin.install.oci" {
		t.Fatalf("verr = %v, want a refusal naming the disagreement", verr)
	}
	if !strings.Contains(verr.Message, "the registry states") {
		t.Fatalf("message = %q", verr.Message)
	}
}

// Registries answer an unauthorized read with 401 or 404 as they please,
// depending on whether they will admit the repository exists. Both have to
// say a credential might be the problem, or somebody goes off to debug an
// index that is correct.
func TestAPrivateArtifactSaysSoRatherThanSayingItIsMissing(t *testing.T) {
	overHTTP(t)
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{"401 on the tokened read", http.StatusUnauthorized, "anonymously"},
		{"403", http.StatusForbidden, "anonymously"},
		{"404, which is how many registries hide a private repository", http.StatusNotFound, "as readily as"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := newFakeRegistry(t, []byte("bytes"))
			reg.manifestStatus = tc.status
			dst, err := os.Create(filepath.Join(t.TempDir(), "artifact"))
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close()
			_, verr := fetchArtifact(context.Background(), reg.ref(), dst)
			if verr == nil {
				t.Fatal("a refused read was reported as a success")
			}
			if !strings.Contains(verr.Message+" "+verr.Hint, tc.want) {
				t.Fatalf("message = %q, hint = %q, want it to mention %q",
					verr.Message, verr.Hint, tc.want)
			}
		})
	}
}

// A manifest's platforms: entry already states an os and an arch. An index
// inside it is a second answer to the same question, and picking one silently
// is how a linux/amd64 plugin lands on an arm64 machine.
func TestAMultiPlatformIndexIsRefusedRatherThanDescendedInto(t *testing.T) {
	overHTTP(t)
	reg := newFakeRegistry(t, []byte("bytes"))
	reg.indexOnly = true
	reg.manifest = `{"schemaVersion":2,
		"mediaType":"application/vnd.oci.image.index.v1+json",
		"manifests":[{"digest":"sha256:` + strings.Repeat("a", 64) + `"}]}`
	dst, err := os.Create(filepath.Join(t.TempDir(), "artifact"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	_, verr := fetchArtifact(context.Background(), reg.ref(), dst)
	if verr == nil || !strings.Contains(verr.Message, "multi-platform index") {
		t.Fatalf("verr = %v, want the index refused by name", verr)
	}
}

// Everything here is spliced into a URL rta builds out of a string an index
// supplied, so the grammar is the check.
func TestAReferenceIsReadOrRefusedByItsGrammar(t *testing.T) {
	good := map[string]ociRef{
		"oci://ghcr.io/example/rta-plugin-pg:1.2.0-linux-amd64": {
			host: "ghcr.io", repo: "example/rta-plugin-pg", ref: "1.2.0-linux-amd64"},
		"oci://ghcr.io/example/rta-plugin-pg@sha256:" + strings.Repeat("b", 64): {
			host: "ghcr.io", repo: "example/rta-plugin-pg", ref: "sha256:" + strings.Repeat("b", 64)},
		"oci://registry.example.com:5000/deep/path/name:v1": {
			host: "registry.example.com:5000", repo: "deep/path/name", ref: "v1"},
	}
	for raw, want := range good {
		got, verr := parseOCIRef(raw)
		if verr != nil {
			t.Fatalf("%s: %v", raw, verr)
		}
		if got != want {
			t.Fatalf("%s = %+v, want %+v", raw, got, want)
		}
	}
	for _, raw := range []string{
		"oci://ghcr.io/example/name",                                // no tag or digest
		"oci://ghcr.io/example/name@sha256:zzzz",                    // not hex
		"oci://ghcr.io/example/name@md5:" + strings.Repeat("b", 64), // not sha256
		"oci://ghcr.io/Example/Name:v1",                             // uppercase repository
		"oci://ghcr.io/example/name:../../etc/passwd",               // traversal in the tag
		"oci://ghcr.io/example/../name:v1",                          // traversal in the repository
		"oci:///name:v1",                                            // no registry
		"https://ghcr.io/example/name:v1",                           // not oci
	} {
		if got, verr := parseOCIRef(raw); verr == nil {
			t.Fatalf("%s parsed as %+v, want a refusal", raw, got)
		}
	}
}

// A scope legitimately holds commas ("repository:a:pull,push"), so splitting
// the challenge on every comma loses the rest of the value.
func TestAChallengeIsSplitOnUnquotedCommas(t *testing.T) {
	got := ociChallenge(`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:a/b:pull,push"`)
	if got["realm"] != "https://ghcr.io/token" || got["service"] != "ghcr.io" {
		t.Fatalf("challenge = %+v", got)
	}
	if got["scope"] != "repository:a/b:pull,push" {
		t.Fatalf("scope = %q, want the comma inside the quotes kept", got["scope"])
	}
	if len(ociChallenge(`Basic realm="somewhere"`)) != 0 {
		t.Fatal("a non-Bearer challenge was read as one")
	}
}

// `rta plugin manifest --platform <os>/<arch>=oci://…` needs no --checksums:
// the registry that will serve the bytes states their digest, and its media
// type says whether they are an archive. That is a better source than a file
// sitting beside the build, and it is the one the publishing pipeline uses.
func TestAnOCIPlatformTakesItsDigestFromTheRegistry(t *testing.T) {
	overHTTP(t)
	testData(t)
	reg := newFakeRegistry(t, []byte("a plugin archive"))
	host := strings.TrimPrefix(reg.Server.URL, "http://")

	_, m := generate(t, GenerateRequest{
		Binary: hello(t),
		Platforms: []PlatformSource{
			{OS: "linux", Arch: "amd64", URL: "oci://" + host + "/" + reg.repo + ":1.0.0"},
			{OS: "windows", Arch: "amd64", URL: "oci://" + host + "/" + reg.repo + ":1.0.0"},
		},
	})
	for _, p := range m.Platforms {
		if want := strings.TrimPrefix(reg.digest, "sha256:"); p.SHA256 != want {
			t.Fatalf("%s/%s sha256 = %q, want the registry's %q", p.OS, p.Arch, p.SHA256, want)
		}
		want := "rta-plugin-hello"
		if p.OS == "windows" {
			want += ".exe"
		}
		if p.Bin != want {
			t.Fatalf("%s/%s bin = %q, want %q — the layer is a tar+gzip",
				p.OS, p.Arch, p.Bin, want)
		}
	}
}
