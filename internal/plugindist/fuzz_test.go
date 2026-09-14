package plugindist

import "testing"

// Whatever an index says an artifact's address is, the reference that comes
// back is one the grammar accepts — the request path is built from these
// three strings, and the regexps are the check that keeps a manifest from
// splicing anything else into it.
func FuzzParseOCIRef(f *testing.F) {
	for _, seed := range []string{
		"oci://ghcr.io/example/rta-plugin-pg:1.2.0-linux-amd64",
		"oci://ghcr.io/example/rta-plugin-pg@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"oci://ghcr.io/example/plugin", "oci:///nohost:tag", "https://ghcr.io/x:1", "oci://h/UPPER:1",
		"oci://h/a/b:c:d", "oci://h/a@sha256:short", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		ref, verr := parseOCIRef(raw)
		if verr != nil {
			return
		}
		if ref.host == "" || !ociRepoRe.MatchString(ref.repo) {
			t.Fatalf("parseOCIRef(%q) accepted host %q repo %q", raw, ref.host, ref.repo)
		}
		if !ociTagRe.MatchString(ref.ref) && !ociDigestRe.MatchString(ref.ref) {
			t.Fatalf("parseOCIRef(%q) accepted reference %q", raw, ref.ref)
		}
	})
}
