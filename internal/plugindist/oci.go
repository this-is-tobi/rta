package plugindist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Fetching a plugin artifact out of an OCI registry, by hand and over plain
// HTTPS.
//
// **Why not oras-go.** It is the reference client and it would work; it also
// costs 509,312 bytes — measured against a baseline linking what this path
// already has, about 1.5% of rta — to perform four requests this file spells
// out in full. Binary size is a stated constraint here, and an install path
// is the code most worth being able to read end to end in one sitting: a
// registry client is where a credential would leak if one ever leaked, so
// "somebody else handles the handshake" is the wrong kind of comfort. The
// distribution spec's pull half is small enough to own.
//
// The four requests, verified against ghcr.io on 2026-09-04:
//
//	GET  /v2/<repo>/manifests/<ref>     → 401 with a Bearer challenge
//	GET  <realm>?service=&scope=        → { "token": … }
//	GET  /v2/<repo>/manifests/<ref>     → the manifest JSON
//	GET  /v2/<repo>/blobs/<digest>      → 307 to storage, then the bytes
//
// **Anonymous, and only anonymous.** rta sends no credential to any registry.
// A public artifact needs none — the token above is handed to anyone who asks
// — and a private one is refused by name rather than reached for. That is not
// a gap to fill in later without thinking: an index is somebody else's
// repository, so a manifest claiming oci://evil.example.com must never cause
// rta to authenticate to evil.example.com, and the rule when private
// registries arrive is that a credential is selected by a host the *operator*
// configured and never by a host a *manifest* names.
//
// **The redirect is why the credential question is not theoretical.** A blob
// GET answers 307 to a different host entirely — ghcr sends you to
// pkg-containers.githubusercontent.com with a presigned URL. A client that
// carried the Authorization header across would hand every blob server a
// registry token, so ociClient strips it on any hop that leaves the registry
// host — see there for why net/http's own rule is not quite the one to rely
// on.

// ociScheme is https and is not a setting. It is a var so the tests can run a
// fake registry over plain http instead of minting a certificate chain for
// one; TestARegistryIsReachedOverHTTPS pins the default, because a plugin
// binary fetched in cleartext is not fetched — it is accepted from whoever is
// nearest, which is the sentence checkArtifactURL already refuses http for.
var ociScheme = "https"

// ociRef is a reference split into the three things the API needs.
type ociRef struct {
	host string
	repo string
	// ref is a tag or a `sha256:…` digest — whatever goes in the manifest URL.
	ref string
}

// ociDigestRe bounds what may be spliced into a request path. Everything here
// reaches a URL rta builds from a string an index supplied, so the grammar is
// the check: a repository is lowercase path segments, a digest is an
// algorithm and hex, and a tag is the registry spec's own character set. A
// value that does not match is refused rather than escaped, because "escaped
// correctly" is a claim about every downstream parser and this is a claim
// about the value.
var (
	ociRepoRe   = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
	ociTagRe    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	ociDigestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// parseOCIRef reads oci://<host>/<repo>[:<tag>|@<digest>].
//
// The digest form is the one worth preferring in a manifest: it names the
// bytes rather than a label somebody can move. Both are accepted because a
// tag is what a publisher has at the moment they publish.
func parseOCIRef(raw string) (ociRef, *view.Error) {
	bad := func(why string) *view.Error {
		return view.Errorf("plugin.install.oci", "%s: %s", raw, why).
			WithHint("an OCI artifact is `oci://<registry>/<repository>:<tag>` or " +
				"`…@sha256:<hex>`, for example " +
				"oci://ghcr.io/example/rta-plugin-pg:1.2.0-linux-amd64")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "oci" {
		return ociRef{}, bad("not an oci:// URL")
	}
	if u.Host == "" {
		return ociRef{}, bad("names no registry")
	}
	// url.Parse puts everything after the host in Path, digest included,
	// because ':' and '@' are legal there. Splitting by hand keeps the two
	// forms apart without a second parse.
	rest := strings.TrimPrefix(u.Path, "/")
	out := ociRef{host: u.Host}
	switch {
	case strings.Contains(rest, "@"):
		out.repo, out.ref, _ = strings.Cut(rest, "@")
		if !ociDigestRe.MatchString(out.ref) {
			return ociRef{}, bad("the digest after @ is not sha256:<64 hex>")
		}
	default:
		// A colon in the last segment is the tag; one anywhere else is not a
		// reference this understands.
		slash := strings.LastIndex(rest, "/")
		if colon := strings.LastIndex(rest, ":"); colon > slash {
			out.repo, out.ref = rest[:colon], rest[colon+1:]
			if !ociTagRe.MatchString(out.ref) {
				return ociRef{}, bad("the tag after : is not a legal tag")
			}
		} else {
			return ociRef{}, bad("names no tag or digest")
		}
	}
	if !ociRepoRe.MatchString(out.repo) {
		return ociRef{}, bad("the repository is not lowercase path segments")
	}
	// The host reaches a URL too, and url.Parse is permissive about what it
	// will accept there.
	if strings.ContainsAny(out.host, "/?#\\") {
		return ociRef{}, bad("the registry is not a host")
	}
	return out, nil
}

// ociManifestCap bounds the JSON. A manifest is a few hundred bytes and this
// is four orders of magnitude of headroom; the point is that a hostile
// registry cannot answer a small request with an unbounded body.
const ociManifestCap = 4 << 20

// These are the media types a pull names. Each pair is OCI's and Docker's
// spelling of the same thing — registries serve whichever they stored, so a
// client that asks for one gets 404s from half the world.
//
// The index types are here even though an index is refused: a registry that
// is not offered them answers a multi-arch tag with a 404, and the operator
// reads "has no such artifact" about a tag that plainly exists. Asking for it
// and then saying what it is turns a wrong answer into the right sentence.
const ociAccept = "application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.docker.distribution.manifest.v2+json," +
	"application/vnd.oci.image.index.v1+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json"

// ociLayer is the one field of a manifest this needs, plus the ones that say
// the answer was something else.
type ociManifest struct {
	MediaType string `json:"mediaType"`
	Manifests []struct {
		Digest string `json:"digest"`
	} `json:"manifests"`
	Layers []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	} `json:"layers"`
}

// ociLayer is what a registry says about an artifact without serving it: the
// digest of its one layer, and the media type that says whether those bytes
// are an archive or a bare binary.
type ociLayer struct {
	Ref       ociRef
	Digest    string
	MediaType string
	Size      int64
}

// IsArchive reports whether the layer is a gzipped tar, which decides whether
// a manifest needs a `bin:` naming the member to extract. rta publishes the
// same .tar.gz GoReleaser produces, and the OCI layer media type for one is
// the same string every registry uses.
func (l ociLayer) IsArchive() bool { return strings.Contains(l.MediaType, "tar+gzip") }

// ociResolve reads the manifest and returns its single layer, fetching none
// of the bytes. `rta plugin manifest` uses this to learn a published
// artifact's digest from the registry that will serve it, which is a better
// source than a checksums file sitting beside the build.
func ociResolve(ctx context.Context, raw string) (ociLayer, *view.Error) {
	ref, verr := parseOCIRef(raw)
	if verr != nil {
		return ociLayer{}, verr
	}
	body, verr := ociGet(ctx, ociBase(ref)+"/manifests/"+ref.ref, ociAccept, ref)
	if verr != nil {
		return ociLayer{}, verr
	}
	doc, err := io.ReadAll(io.LimitReader(body, ociManifestCap+1))
	body.Close()
	if err != nil {
		return ociLayer{}, view.Errorf("plugin.install.oci", "reading %s: %v", raw, err)
	}
	if int64(len(doc)) > ociManifestCap {
		return ociLayer{}, view.Errorf("plugin.install.oci",
			"%s: the manifest is over the %d MB cap", raw, ociManifestCap>>20)
	}
	var m ociManifest
	if err := json.Unmarshal(doc, &m); err != nil {
		return ociLayer{}, view.Errorf("plugin.install.oci",
			"%s: the registry's manifest is not JSON rta understands: %v", raw, err)
	}
	// A multi-platform index, where a single artifact was expected. Refused
	// rather than descended into: a manifest's platforms: entry already
	// states the os and arch, so an index inside it is two answers to one
	// question, and picking one silently is how a linux/amd64 plugin lands on
	// an arm64 machine.
	if len(m.Manifests) > 0 {
		return ociLayer{}, view.Errorf("plugin.install.oci",
			"%s points at a multi-platform index, and a manifest's platforms: entry "+
				"already says which os and arch it is for", raw).
			WithHint("publish one artifact per platform and name each from its own " +
				"platforms: entry, or point this entry at a single manifest by digest")
	}
	if len(m.Layers) != 1 {
		return ociLayer{}, view.Errorf("plugin.install.oci",
			"%s holds %d layers and a plugin artifact is one", raw, len(m.Layers))
	}
	layer := m.Layers[0]
	if !ociDigestRe.MatchString(layer.Digest) {
		return ociLayer{}, view.Errorf("plugin.install.oci",
			"%s: the registry states a layer digest rta cannot read: %q", raw, layer.Digest)
	}
	if layer.Size > artifactCap {
		return ociLayer{}, view.Errorf("plugin.install.oci",
			"%s: the registry states a %d MB layer, over the %d MB artifact cap",
			raw, layer.Size>>20, artifactCap>>20)
	}
	return ociLayer{Ref: ref, Digest: layer.Digest, MediaType: layer.MediaType,
		Size: layer.Size}, nil
}

func ociBase(ref ociRef) string {
	return ociScheme + "://" + ref.host + "/v2/" + ref.repo
}

// ociBlob resolves a reference and returns the artifact's bytes, along with
// the digest the registry itself states for them.
//
// That digest is a second, independent claim about the same bytes: the index
// says one thing in its sha256 field, the registry says another in its
// manifest, and rta computes a third from what actually arrives. All three
// have to agree. An index and a registry disagreeing is the interesting case
// — it means one of them is describing an artifact the other is not serving.
func ociBlob(ctx context.Context, raw string) (io.ReadCloser, string, *view.Error) {
	layer, verr := ociResolve(ctx, raw)
	if verr != nil {
		return nil, "", verr
	}
	blob, verr := ociGet(ctx, ociBase(layer.Ref)+"/blobs/"+layer.Digest, "*/*", layer.Ref)
	if verr != nil {
		return nil, "", verr
	}
	return blob, strings.TrimPrefix(layer.Digest, "sha256:"), nil
}

// ociGet performs one registry GET, answering a Bearer challenge once.
//
// Once, not in a loop: a registry that answers the tokened request with
// another challenge is a registry rta cannot satisfy anonymously, and
// retrying would turn that into a spin instead of a sentence.
func ociGet(ctx context.Context, target, accept string, ref ociRef) (io.ReadCloser, *view.Error) {
	resp, verr := ociRequest(ctx, target, accept, "")
	if verr != nil {
		return nil, verr
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return ociBody(resp, target, ref)
	}
	challenge := resp.Header.Get("Www-Authenticate")
	resp.Body.Close()
	token, verr := ociToken(ctx, challenge, ref)
	if verr != nil {
		return nil, verr
	}
	resp, verr = ociRequest(ctx, target, accept, token)
	if verr != nil {
		return nil, verr
	}
	return ociBody(resp, target, ref)
}

func ociRequest(ctx context.Context, target, accept, token string) (*http.Response, *view.Error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, view.Errorf("plugin.install.oci", "%v", err)
	}
	req.Header.Set("Accept", accept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ociClient(req.URL.Host).Do(req)
	if err != nil {
		return nil, view.Errorf("plugin.install.oci", "reaching %s: %v", target, err)
	}
	return resp, nil
}

// ociClient will not carry the registry's token anywhere but the registry.
//
// net/http drops Authorization on a redirect that leaves the domain, and that
// alone makes following ghcr's 307 to pkg-containers.githubusercontent.com
// safe. But its rule is *domain* comparison — isDomainOrSubdomain, which
// keeps the header for any sub-domain — so a registry redirecting a blob to
// blobs.itself.example is still handed the token, and the port is not part of
// the comparison at all.
//
// Today that token is one any anonymous caller can mint, so nothing leaks
// either way. Being explicit is for the day it is not: this is the exact line
// a private registry's credential would sit behind, and "net/http probably
// handles it" is the wrong thing to be finding out then. Host equality,
// port included.
func ociClient(registry string) *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after %d redirects", len(via))
			}
			if req.URL.Host != registry {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}
}

// ociBody turns a response into bytes or into the right sentence.
//
// The 401/403 case is the one worth getting right. A registry answers an
// unauthorized read with 401 or 404 more or less as it pleases, depending on
// whether it will admit the repository exists — so the naive reading reports
// "no such plugin" when the truth is "you may not see this one", and sends
// somebody to debug an index that is correct.
func ociBody(resp *http.Response, target string, ref ociRef) (io.ReadCloser, *view.Error) {
	if resp.StatusCode == http.StatusOK {
		return resp.Body, nil
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, view.Errorf("plugin.install.oci",
			"%s will not serve %s anonymously", ref.host, ref.repo).
			WithHint("rta sends no credential to a registry, so it reads public " +
				"artifacts only — a private one has to be mirrored somewhere this " +
				"machine may read, or published publicly")
	case http.StatusNotFound:
		return nil, view.Errorf("plugin.install.oci",
			"%s has no %s at %s", ref.host, ref.repo, ref.ref).
			WithHint("registries answer an unauthorized read with 404 as readily as " +
				"401, so this is either a wrong reference or one you may not see")
	default:
		return nil, view.Errorf("plugin.install.oci", "fetching %s: %s", target, resp.Status)
	}
}

// ociToken answers a `Bearer realm=…,service=…,scope=…` challenge.
//
// No credential is sent, so the realm is followed wherever it points: ghcr's
// is ghcr.io and Docker Hub's is auth.docker.io, a different host entirely,
// which is why this cannot be pinned to the registry's own name. That
// permissiveness is only safe while there is nothing to leak — the moment rta
// carries a registry credential, which host a challenge may send it to
// becomes the whole security question. See this file's header.
func ociToken(ctx context.Context, challenge string, ref ociRef) (string, *view.Error) {
	fields := ociChallenge(challenge)
	realm := fields["realm"]
	if !strings.HasPrefix(realm, ociScheme+"://") {
		return "", view.Errorf("plugin.install.oci",
			"%s asked rta to authenticate somewhere that is not an https realm", ref.host).
			WithHint("the challenge was: " + firstLine(challenge, "(none)"))
	}
	q := url.Values{}
	if s := fields["service"]; s != "" {
		q.Set("service", s)
	}
	// The registry's own scope when it named one, and the pull scope this
	// request needs when it did not.
	scope := fields["scope"]
	if scope == "" {
		scope = "repository:" + ref.repo + ":pull"
	}
	q.Set("scope", scope)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm+"?"+q.Encode(), nil)
	if err != nil {
		return "", view.Errorf("plugin.install.oci", "%v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", view.Errorf("plugin.install.oci", "reaching %s: %v", realm, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", view.Errorf("plugin.install.oci",
			"%s will not issue an anonymous pull token for %s: %s",
			ref.host, ref.repo, resp.Status).
			WithHint("rta sends no credential to a registry, so it reads public " +
				"artifacts only")
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, ociManifestCap)).Decode(&body); err != nil {
		return "", view.Errorf("plugin.install.oci", "%s: %v", realm, err)
	}
	// Two spellings of the same field: the distribution spec says `token`,
	// OAuth2 says `access_token`, and registries in the wild send either.
	if body.Token != "" {
		return body.Token, nil
	}
	if body.AccessToken != "" {
		return body.AccessToken, nil
	}
	return "", view.Errorf("plugin.install.oci", "%s issued no token", realm)
}

// ociChallenge reads the comma-separated key="value" pairs out of a
// WWW-Authenticate header. Only the Bearer scheme is understood; anything
// else leaves the map empty and the caller refuses on the missing realm.
func ociChallenge(header string) map[string]string {
	out := map[string]string{}
	rest, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return out
	}
	for _, part := range splitChallenge(rest) {
		k, v, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		out[strings.ToLower(k)] = strings.Trim(v, `"`)
	}
	return out
}

// splitChallenge splits on commas that are not inside a quoted value — a
// scope legitimately holds them ("repository:a:pull,push").
func splitChallenge(s string) []string {
	var out []string
	quoted, start := false, 0
	for i, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// ociMismatch is the refusal when the registry and the index describe
// different bytes. Both are claims by somebody else and rta believes neither;
// what makes this worth its own sentence is that the two disagreeing means
// one of them is describing an artifact the other is not serving, which is a
// different problem from a corrupted download.
func ociMismatch(rawURL, registry, got string) *view.Error {
	return view.Errorf("plugin.install.oci",
		"%s: the registry states sha256:%s and rta computed %s", rawURL, registry, got).
		WithHint(fmt.Sprintf("the bytes served under %s are not the bytes the registry's "+
			"own manifest describes", rawURL))
}
