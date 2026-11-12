package plugindist

import (
	"testing"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/textclean"
)

// Whatever two claims an index makes, they order the same both ways round: an
// unordered pair is unordered either way and carries no order, and an ordered
// one orders the other way mirrored. Both are an index's text, so a long run of
// digits or a stray byte must not panic either.
func FuzzVersionClaimsOrderTheSameBothWays(f *testing.F) {
	for _, seed := range [][2]string{
		{"1.3.2", "1.4.0"}, {"v1.4.0", "1.4.0"}, {"1.4.0-rc.1", "1.4.0"}, {"dev", "0.1.0"},
		{"00001.0.0", "1.0.0"}, {"1.2.3.4", "1.2.3"}, {"", "v"}, {"v.1.2", "1..2"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		order, ok := compareClaims(a, b)
		back, backOK := compareClaims(b, a)
		if ok != backOK || order != -back {
			t.Fatalf("compareClaims(%q, %q) = %d, %v but (%q, %q) = %d, %v", a, b, order, ok, b, a, back, backOK)
		}
		if !ok && order != 0 {
			t.Fatalf("compareClaims(%q, %q) = %d, false: an unordered pair carries an order", a, b, order)
		}
		if order < -1 || order > 1 {
			t.Fatalf("compareClaims(%q, %q) = %d", a, b, order)
		}
	})
}

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
		"apiVersion: rta/v3\n" + goodManifest,
		"apiVersion: rta/v3\nscope: cluster\n",
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
		if m.APIVersion != "" && m.APIVersion != APIVersionV1 && m.APIVersion != APIVersionV2 {
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
