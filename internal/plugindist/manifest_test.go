package plugindist

import (
	"strings"
	"testing"
)

const goodManifest = `name: pg
version: 0.1.0
summary: PostgreSQL toolkit
homepage: https://example.com/pg
platforms:
  - os: darwin
    arch: arm64
    url: https://example.com/pg_darwin_arm64.tar.gz
    sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    bin: rta-plugin-pg
  - os: linux
    arch: amd64
    url: https://example.com/pg_linux_amd64
    sha256: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
capabilities:
  - id: pg.status
    summary: connection health
    safety: read
  - id: pg.query
    safety: write
    grant: true
signature:
  sig: https://example.com/pg.sig
  key: https://example.com/pg.pub
`

func TestAManifestParsesAndItsClaimsAreLegible(t *testing.T) {
	m, verr := ParseManifest([]byte(goodManifest))
	if verr != nil {
		t.Fatalf("a well-formed manifest was refused: %v", verr)
	}
	if m.Name != "pg" || m.Version != "0.1.0" || len(m.Capabilities) != 2 {
		t.Fatalf("parsed %+v", m)
	}
	if p, ok := m.PlatformFor("darwin", "arm64"); !ok || p.Bin != "rta-plugin-pg" {
		t.Fatalf("PlatformFor(darwin/arm64) = %+v, %v", p, ok)
	}
	if _, ok := m.PlatformFor("plan9", "mips"); ok {
		t.Fatal("an unoffered platform resolved")
	}
	if got := m.SafetyLine(); !strings.Contains(got, "1 read") ||
		!strings.Contains(got, "1 write") || !strings.Contains(got, "1 needs a grant") {
		t.Fatalf("SafetyLine = %q", got)
	}
	if m.Offered() != "darwin/arm64, linux/amd64" {
		t.Fatalf("Offered = %q", m.Offered())
	}
}

// Every refusal a hostile or sloppy index can earn, one per rule. The index
// is somebody else's repository read on every search: nothing in it may
// traverse a path, print a control sequence, or claim outside its own name.
func TestHostileManifestsAreRefused(t *testing.T) {
	mutate := func(from, to string) string { return strings.Replace(goodManifest, from, to, 1) }
	cases := []struct{ name, doc, why string }{
		{"uppercase name", mutate("name: pg", "name: PG"), "not a plugin namespace"},
		{"no version", mutate("version: 0.1.0", "version: \"\""), "states no version"},
		{"no summary", mutate("summary: PostgreSQL toolkit", "summary: \"\""), "states no summary"},
		{"ansi in summary", mutate("summary: PostgreSQL toolkit",
			"summary: \"a\\e[31mred\\e[0m claim\""), "control or invisible"},
		{"newline in summary", mutate("summary: PostgreSQL toolkit",
			"summary: \"two\\nlines\""), "control or invisible"},
		{"oversized summary", mutate("summary: PostgreSQL toolkit",
			"summary: "+strings.Repeat("x", 121)), "the cap is"},
		{"http homepage", mutate("homepage: https://example.com/pg",
			"homepage: http://example.com/pg"), "not an https URL"},
		{"http artifact", mutate("url: https://example.com/pg_darwin_arm64.tar.gz",
			"url: http://example.com/pg_darwin_arm64.tar.gz"), "plain http"},
		{"ftp artifact", mutate("url: https://example.com/pg_darwin_arm64.tar.gz",
			"url: ftp://example.com/pg.tar.gz"), "the schemes are"},
		{"relative file url", mutate("url: https://example.com/pg_darwin_arm64.tar.gz",
			"url: file://pg.tar.gz"), "absolute path"},
		{"short sha256", mutate(
			"sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"sha256: abc123"), "64 hex"},
		{"traversal bin", mutate("bin: rta-plugin-pg", "bin: ../../../../bin/sh"), "clean relative path"},
		{"absolute bin", mutate("bin: rta-plugin-pg", "bin: /usr/bin/sh"), "clean relative path"},
		{"backslash bin", mutate("bin: rta-plugin-pg", "bin: dir\\pg"), "clean relative path"},
		{"foreign capability", mutate("id: pg.status", "id: kv.get"), "outside its own namespace"},
		{"duplicate capability", mutate("id: pg.query", "id: pg.status"), "twice"},
		{"bad safety", mutate("safety: write", "safety: dangerous"), "read, write, destructive"},
		{"unknown key", mutate("homepage:", "homepgae:"), "homepgae"},
		{"no platforms", mutate("platforms:", "unplatforms:"), ""},
		{"no capabilities", mutate("capabilities:", "uncapabilities:"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, verr := ParseManifest([]byte(tc.doc))
			if verr == nil {
				t.Fatal("accepted")
			}
			if tc.why != "" && !strings.Contains(verr.Message, tc.why) &&
				!strings.Contains(verr.Hint, tc.why) {
				t.Fatalf("refusal = %q (hint %q), want it to mention %q",
					verr.Message, verr.Hint, tc.why)
			}
		})
	}

	t.Run("duplicate platform", func(t *testing.T) {
		doc := strings.Replace(goodManifest, "os: linux", "os: darwin", 1)
		doc = strings.Replace(doc, "arch: amd64", "arch: arm64", 1)
		if _, verr := ParseManifest([]byte(doc)); verr == nil ||
			!strings.Contains(verr.Message, "twice") {
			t.Fatalf("verr = %v", verr)
		}
	})
	t.Run("oversized manifest", func(t *testing.T) {
		if _, verr := ParseManifest(make([]byte, manifestCap+1)); verr == nil ||
			!strings.Contains(verr.Message, "cap") {
			t.Fatalf("verr = %v", verr)
		}
	})
}
