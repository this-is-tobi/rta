package plugindist

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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

// The cap on a manifest's bytes is not a cap on what those bytes expand into.
// An index is cloned from a repository somebody else merges to and re-read on
// every search, and 620 bytes of aliases nested through the fields Manifest
// declares cost 3.86 GB — comfortably under the 1 MB cap that was the only
// bound here. yaml.Strict() does not help: it refuses a misspelled key and
// says nothing about a legal one whose value fans out.
func TestAManifestBombIsRefusedBeforeItIsDecoded(t *testing.T) {
	var b strings.Builder
	b.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 6; i++ {
		fmt.Fprintf(&b, "a%d: &a%d [", i, i)
		for j := range 10 {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*a%d", i-1)
		}
		b.WriteString("]\n")
	}
	fmt.Fprintf(&b, "name: [*a5, *a5, *a5, *a5, *a5, *a5, *a5, *a5, *a5, *a5]\n")

	done := make(chan *view.Error, 1)
	go func() {
		_, verr := ParseManifest([]byte(b.String()))
		done <- verr
	}()
	select {
	case verr := <-done:
		if verr == nil {
			t.Fatal("an alias-expansion bomb parsed as a manifest")
		}
		if verr.Code != "plugin.index.manifest" {
			t.Fatalf("want plugin.index.manifest, got %s: %s", verr.Code, verr.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ParseManifest did not return within 2s — it expanded the bomb instead of refusing it")
	}
}

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

// The one line `rta plugin search` gives a plugin has to carry it too — a row
// reading "all read" for something that wants your kubeconfig is true and
// useless.
func TestTheSearchLineSaysWhatAPluginAsksToRead(t *testing.T) {
	m := Manifest{
		Capabilities: []CapabilityClaim{{ID: "lab.get", Safety: "read"}},
		Needs:        []plugin.Need{plugin.NeedKubeconfig, plugin.NeedSSH},
	}
	if got := m.SafetyLine(); got != "all read · none needs a grant · asks for kubeconfig, ssh" {
		t.Fatalf("SafetyLine = %q", got)
	}
	m.Needs = nil
	if got := m.SafetyLine(); got != "all read · none needs a grant" {
		t.Fatalf("SafetyLine = %q, want no clause when nothing is asked for", got)
	}
}

// A location rta has no way to allow cannot be claimed: `rta plugin allow`
// works from a closed set, so an entry naming something outside it would be
// asking the operator to approve a word.
func TestAManifestCannotAskForALocationRtaCannotAllow(t *testing.T) {
	doc := "name: lab\nversion: 0.1.0\nsummary: a lab\n" +
		"platforms:\n  - os: linux\n    arch: amd64\n    url: https://example.com/lab\n" +
		"    sha256: " + strings.Repeat("a", 64) + "\n" +
		"capabilities:\n  - id: lab.get\n    safety: read\n"
	if _, verr := ParseManifest([]byte(doc + "needs:\n  - my-private-keys\n")); verr == nil {
		t.Fatal("a manifest asked for a location nothing could grant")
	}
	if _, verr := ParseManifest([]byte(doc + "needs:\n  - aws\n  - aws\n")); verr == nil {
		t.Fatal("a manifest asked for one location twice")
	}
	if _, verr := ParseManifest([]byte(doc + "needs:\n  - aws\n")); verr != nil {
		t.Fatalf("a manifest stating a real location was refused: %v", verr)
	}
}
