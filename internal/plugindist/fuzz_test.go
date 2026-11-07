package plugindist

import (
	"testing"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/textclean"
)

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
		if !ociTagRe().MatchString(ref.ref) && !ociDigestRe.MatchString(ref.ref) {
			t.Fatalf("parseOCIRef(%q) accepted reference %q", raw, ref.ref)
		}
	})
}

// Whatever bytes an index holds, a manifest ParseManifest accepts is one
// whose apiVersion this reader has and whose free-form parts display as what
// they are, and one it refuses is refused in one of the two codes an index
// author or a reader can act on. The manifest is the hostile input of the
// whole package: it is read from somebody else's repository on every search.
func FuzzParseManifest(f *testing.F) {
	for _, seed := range []string{
		goodManifest,
		"apiVersion: rta/v1\n" + goodManifest,
		"apiVersion: rta/v2\n" + goodManifest,
		"apiVersion: rta/v2\nscope: cluster\n",
		"apiVersion: [rta/v1]\n" + goodManifest,
		goodManifest + "apiVersion: \"rta/v\\u202e1\"\n",
		goodManifest + "annotations:\n  license: Apache-2.0\n  example.com/owner: team\n",
		goodManifest + "annotations:\n  License: x\n",
		goodManifest + "annotations:\n  k: \"a\\u202eb\"\n",
		goodManifest + "annotations:\n  k: [a]\n",
		goodManifest + "license: Apache-2.0\n",
		goodManifest + "homepage: https://example.com/\\u202e\n",
		"", "name: [", "annotations:\n  a: b\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		m, verr := ParseManifest(raw)
		if verr != nil {
			if verr.Code != "plugin.index.manifest" && verr.Code != CodeAPIVersion {
				t.Fatalf("ParseManifest(%q) refused with code %q", raw, verr.Code)
			}
			return
		}
		if m.APIVersion != "" && m.APIVersion != APIVersionV1 {
			t.Fatalf("ParseManifest(%q) accepted apiVersion %q", raw, m.APIVersion)
		}
		if len(m.Annotations) > maxAnnotations {
			t.Fatalf("ParseManifest(%q) accepted %d annotations", raw, len(m.Annotations))
		}
		for k, v := range m.Annotations {
			if !annotationKey(k) || len(k) > maxAnnotationKey {
				t.Fatalf("ParseManifest(%q) accepted annotation key %q", raw, k)
			}
			if textclean.Deceives(v) || utf8.RuneCountInString(v) > maxAnnotationValue {
				t.Fatalf("ParseManifest(%q) accepted annotation %q: %q", raw, k, v)
			}
		}
		if textclean.Deceives(m.Homepage) || utf8.RuneCountInString(m.Homepage) > maxManifestHomepage {
			t.Fatalf("ParseManifest(%q) accepted homepage %q", raw, m.Homepage)
		}
	})
}
