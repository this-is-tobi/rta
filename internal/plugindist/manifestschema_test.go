package plugindist

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// runningAs says which build this process is, for the message that names it,
// and puts back what was there: the version is package state in session.
func runningAs(t *testing.T, version string) {
	t.Helper()
	was := session.Self()
	session.SetSelf(version)
	t.Cleanup(func() { session.SetSelf(was) })
}

// stamped is goodManifest stating apiVersion first, the way a generator
// would write it. The value is quoted the way Go quotes it, which YAML reads
// back byte for byte, so a hostile value can be built from escapes.
func stamped(apiVersion string) string {
	return "apiVersion: " + strconv.Quote(apiVersion) + "\n" + goodManifest
}

// Every manifest an index holds today states no apiVersion, so a missing one
// is rta/v1 for as long as an index can hold such a file, and stating it is
// accepted too: a generator that starts to write the key must not hide the
// plugin from a reader that has read the key all along.
func TestAManifestMayStateTheAPIVersionRtaReads(t *testing.T) {
	bare, verr := ParseManifest([]byte(goodManifest))
	if verr != nil {
		t.Fatalf("a manifest stating no apiVersion was refused: %v", verr)
	}
	if bare.APIVersion != "" {
		t.Fatalf("APIVersion = %q, want it left empty when the manifest states none", bare.APIVersion)
	}
	for name, doc := range map[string]string{
		"first":   "apiVersion: rta/v1\n" + goodManifest,
		"last":    goodManifest + "apiVersion: rta/v1\n",
		"quoted":  stamped("rta/v1"),
		"emptied": "apiVersion: \"\"\n" + goodManifest,
		"v2":      stamped("rta/v2"),
	} {
		t.Run(name, func(t *testing.T) {
			m, verr := ParseManifest([]byte(doc))
			if verr != nil {
				t.Fatalf("refused: %v", verr)
			}
			if m.Name != bare.Name || m.Version != bare.Version || len(m.Capabilities) != len(bare.Capabilities) {
				t.Fatalf("parsed %+v, want what the unstamped manifest parses to", m)
			}
		})
	}
}

// A document of a newer apiVersion is refused for being one, whatever else it
// changed. It will not decode the way this reader's struct expects — it adds
// a key, retypes a field, or tightens a rule — and the symptom (`unknown
// field`, a type error, a safety class this reader has no word for) would
// blame the manifest for what is this rta being old. The version is read
// before any of them can speak.
func TestAManifestOfANewerAPIVersionIsRefusedWhateverElseItChanged(t *testing.T) {
	runningAs(t, "0.59.0")
	mutate := func(from, to string) string { return strings.Replace(goodManifest, from, to, 1) }
	cases := []struct{ name, doc string }{
		{"decodes cleanly", stamped("rta/v3")},
		{"stated last", goodManifest + "apiVersion: rta/v3\n"},
		{"adds a key", stamped("rta/v3") + "scope: cluster\n"},
		{"retypes a field", "apiVersion: rta/v3\n" + mutate("platforms:", "platforms: {linux: amd64}\nwas:")},
		{"retypes the version", "apiVersion: rta/v3\n" + mutate("version: 0.1.0", "version: [0, 1, 0]")},
		{"a safety class this reader has no word for", "apiVersion: rta/v3\n" + mutate("safety: write", "safety: privileged")},
		{"a name this reader refuses", "apiVersion: rta/v3\n" + mutate("name: pg", "name: PG")},
		{"drops a required field", "apiVersion: rta/v3\nname: pg\n"},
		{"a much later one", stamped("rta/v10") + "scope: cluster\n"},
		{"one past any integer", stamped("rta/v99999999999999999999999999")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, verr := ParseManifest([]byte(tc.doc))
			if verr == nil {
				t.Fatal("accepted")
			}
			if verr.Code != "plugin.index.apiversion" {
				t.Fatalf("code = %s (%s), want plugin.index.apiversion", verr.Code, verr.Message)
			}
			for _, want := range []string{"is newer than rta/v2", "upgrade rta", "(0.59.0)"} {
				if !strings.Contains(verr.Message, want) {
					t.Fatalf("message = %q, want it to say %q", verr.Message, want)
				}
			}
		})
	}
}

// The message is printed alone by `index add` and `outdated`, so it carries
// everything: what the manifest said, what this rta reads, which rta this is,
// and whose move it is. Held to the byte because it is the sentence a person
// reads in place of a plugin they were looking for.
func TestTheRefusalOfANewerAPIVersionStandsAlone(t *testing.T) {
	runningAs(t, "0.59.0")
	_, verr := ParseManifest([]byte(stamped("rta/v3")))
	if verr == nil {
		t.Fatal("accepted")
	}
	const want = `apiVersion "rta/v3" is newer than rta/v2, which this rta (0.59.0) reads — upgrade rta`
	if verr.Message != want {
		t.Fatalf("message = %q\nwant      %q", verr.Message, want)
	}

	runningAs(t, "")
	_, verr = ParseManifest([]byte(stamped("rta/v3")))
	if verr == nil {
		t.Fatal("accepted")
	}
	const unnamed = `apiVersion "rta/v3" is newer than rta/v2, which this rta reads — upgrade rta`
	if verr.Message != unnamed {
		t.Fatalf("message = %q\nwant      %q", verr.Message, unnamed)
	}
}

// The slips an author can make with the field are the author's to fix, in the
// code every other malformed manifest has, and none of them reads as "upgrade
// rta": a reader told to upgrade for a typo would upgrade and meet the same
// refusal.
func TestAnAPIVersionRtaDoesNotKnowIsAnAuthorsSlip(t *testing.T) {
	runningAs(t, "0.59.0")
	for _, bad := range []string{
		"rta/v0", "rta.v1", "v1", "RTA/V1", "rta/V2", "rta/v3beta1", "rta/v1alpha1", "1", "2",
		"rta/v01", "rta/v", "rta/v-1", "rta/v+2", "rta/v1.1", "rta/v1 ", " rta/v1", "rta/v1/x",
		"rta/v\u0662", "example.com/v1", "rta.dev/v1", "kubernetes/v1", "rta",
	} {
		t.Run(bad, func(t *testing.T) {
			_, verr := ParseManifest([]byte(stamped(bad)))
			if verr == nil {
				t.Fatal("accepted")
			}
			if verr.Code != "plugin.index.manifest" {
				t.Fatalf("code = %s (%s), want plugin.index.manifest", verr.Code, verr.Message)
			}
			if !strings.Contains(verr.Message, "not one rta knows") ||
				!strings.Contains(verr.Message, strconv.Quote(bad)) {
				t.Fatalf("message = %q, want it to echo %q as not one rta knows", verr.Message, bad)
			}
			if strings.Contains(verr.Message, "upgrade rta") {
				t.Fatalf("message = %q: a slip is not cured by upgrading", verr.Message)
			}
		})
	}

	// A value that is no string at all is refused where the decoder finds it.
	for _, doc := range []string{
		"apiVersion: [rta/v1]\n" + goodManifest,
		"apiVersion: {rta: v1}\n" + goodManifest,
	} {
		if _, verr := ParseManifest([]byte(doc)); verr == nil || verr.Code != "plugin.index.manifest" {
			t.Fatalf("%q: verr = %v, want plugin.index.manifest", doc[:30], verr)
		}
	}
}

// What a manifest says is echoed back to whoever reads the refusal, and the
// echo must neither print a control sequence nor grow with the claim: one
// short, quoted, clipped line for a value of any size and any bytes.
func TestAnAPIVersionIsQuotedAndClippedInTheRefusal(t *testing.T) {
	runningAs(t, "0.59.0")
	hostile := "rta/v1\x1b[31m\u202e" + strings.Repeat("x", 900_000)
	for name, value := range map[string]string{
		"a malformed one":     hostile,
		"a newer one's tail":  "rta/v3" + hostile,
		"a newer one's digit": "rta/v" + strings.Repeat("9", 900_000),
	} {
		t.Run(name, func(t *testing.T) {
			_, verr := ParseManifest([]byte(stamped(value)))
			if verr == nil {
				t.Fatal("accepted")
			}
			if n := len(verr.Message); n > 400 {
				t.Fatalf("message is %d bytes; a claim of any size must be clipped to a line", n)
			}
			for _, r := range verr.Message + verr.Hint {
				if r < 0x20 || r == 0x7f || r == '\u202e' {
					t.Fatalf("message holds %U raw: %q", r, verr.Message)
				}
			}
		})
	}
}

// The strictness is deliberate and stays: a key rta does not read is refused
// at the top level, so an author hears that a claim is not checked, which is
// what keeps a hand-written `signature:` from being believed. What changes is
// that the refusal says where a key rta does not read can go.
func TestAnUnknownTopLevelKeyIsStillRefusedAndSaysWhereItGoes(t *testing.T) {
	for _, key := range []string{"license: Apache-2.0", "signature: {sig: x}", "homepgae: https://example.com"} {
		t.Run(key, func(t *testing.T) {
			_, verr := ParseManifest([]byte(goodManifest + key + "\n"))
			if verr == nil {
				t.Fatal("accepted")
			}
			name, _, _ := strings.Cut(key, ":")
			if verr.Code != "plugin.index.manifest" || !strings.Contains(verr.Message, strconv.Quote(name)) {
				t.Fatalf("verr = %v, want plugin.index.manifest naming %q", verr, name)
			}
			if !strings.Contains(verr.Hint, "annotations:") {
				t.Fatalf("hint = %q, want it to name annotations: as the place", verr.Hint)
			}
		})
	}
	// The hint belongs to the unknown key and to nothing else.
	_, verr := ParseManifest([]byte(strings.Replace(goodManifest, "safety: write", "safety: dangerous", 1)))
	if verr == nil || strings.Contains(verr.Hint, "annotations") {
		t.Fatalf("a bad safety class: verr = %v, want no mention of annotations", verr)
	}
}

// annotated is goodManifest with an annotations block of the lines given,
// each indented under it.
func annotated(lines ...string) string {
	return goodManifest + "annotations:\n  " + strings.Join(lines, "\n  ") + "\n"
}

// An annotation is the one place a manifest holds what rta does not read, and
// it is kept so that a future reader has something to read; to this one it is
// inert, so everything else a manifest says is unchanged by it.
func TestAnnotationsAreTheOnePlaceAKeyRtaDoesNotReadIsAccepted(t *testing.T) {
	plain, verr := ParseManifest([]byte(goodManifest))
	if verr != nil {
		t.Fatal(verr)
	}
	m, verr := ParseManifest([]byte(annotated(
		"license: Apache-2.0",
		"example.com/owner: platform-team",
		`year: "2026"`,
		"empty: \"\"",
		"a: x",
		"0.1_b-c: d",
	)))
	if verr != nil {
		t.Fatalf("annotations were refused: %v", verr)
	}
	want := map[string]string{
		"license": "Apache-2.0", "example.com/owner": "platform-team",
		"year": "2026", "empty": "", "a": "x", "0.1_b-c": "d",
	}
	if len(m.Annotations) != len(want) {
		t.Fatalf("Annotations = %v, want %v", m.Annotations, want)
	}
	for k, v := range want {
		if got, ok := m.Annotations[k]; !ok || got != v {
			t.Fatalf("Annotations[%q] = %q, %v; want %q", k, got, ok, v)
		}
	}
	if m.SafetyLine() != plain.SafetyLine() || m.Offered() != plain.Offered() ||
		m.Name != plain.Name || m.Version != plain.Version || m.Summary != plain.Summary {
		t.Fatalf("annotations changed what the manifest says: %+v against %+v", m, plain)
	}
	p, ok := m.PlatformFor("darwin", "arm64")
	q, _ := plain.PlatformFor("darwin", "arm64")
	if !ok || p != q {
		t.Fatalf("PlatformFor = %+v, want %+v", p, q)
	}

	t.Run("none and an empty map", func(t *testing.T) {
		for _, doc := range []string{goodManifest + "annotations: {}\n", goodManifest + "annotations:\n"} {
			if _, verr := ParseManifest([]byte(doc)); verr != nil {
				t.Fatalf("refused: %v", verr)
			}
		}
	})
	t.Run("the limits themselves", func(t *testing.T) {
		var sixteen []string
		for i := range 16 {
			sixteen = append(sixteen, fmt.Sprintf("k%d: v", i))
		}
		for name, lines := range map[string][]string{
			"sixteen entries":           sixteen,
			"a 63-character key":        {strings.Repeat("a", 63) + ": v"},
			"a prefix and a name in 63": {"example.com/" + strings.Repeat("a", 51) + ": v"},
			"a 120-rune value":          {"k: " + strconv.Quote(strings.Repeat("é", 120))},
			"alphanumeric ends":         {"a.b_c-d: v", "0: v", "z9/9z: v"},
		} {
			if _, verr := ParseManifest([]byte(annotated(lines...))); verr != nil {
				t.Fatalf("%s: refused: %v", name, verr)
			}
		}
	})
}

// Each limit, and each way an annotation could display as something else than
// it is. A violation refuses the manifest, which names the annotation: a
// claim that cannot be shown as what it is has no place beside the ones that
// can.
func TestHostileAnnotationsAreRefused(t *testing.T) {
	var seventeen []string
	for i := range 17 {
		seventeen = append(seventeen, fmt.Sprintf("k%d: v", i))
	}
	long := strings.Repeat("a", 64)
	cases := []struct {
		name  string
		lines []string
		why   string
	}{
		{"seventeen entries", seventeen, "at most 16"},
		{"an uppercase key", []string{"License: x"}, `"License"`},
		{"a key with a space", []string{`"my key": x`}, `"my key"`},
		{"a leading dash", []string{`"-x": v`}, `"-x"`},
		{"a trailing dash", []string{`"x-": v`}, `"x-"`},
		{"a leading dot", []string{`".x": v`}, `".x"`},
		{"a trailing underscore", []string{`"x_": v`}, `"x_"`},
		{"two slashes", []string{`"a/b/c": v`}, `"a/b/c"`},
		{"an empty prefix", []string{`"/name": v`}, `"/name"`},
		{"an empty name", []string{`"prefix/": v`}, `"prefix/"`},
		{"a prefix ending in a dash", []string{`"prefix-/name": v`}, `"prefix-/name"`},
		{"a name beginning with a dot", []string{`"prefix/.name": v`}, `"prefix/.name"`},
		{"a 64-character key", []string{long + ": v"}, "the cap is 63"},
		{"a prefix and name past 63", []string{"example.com/" + strings.Repeat("a", 52) + ": v"}, "the cap is 63"},
		{"a key beyond ASCII", []string{`"clé": v`}, `"clé"`},
		{"an escape in a key", []string{`"a\u001bb": v`}, `"a\x1bb"`},
		{"a newline in a key", []string{`"a\nb": v`}, `"a\nb"`},
		{"an escape in a value", []string{`k: "a\e[31mred"`}, `annotation "k"`},
		{"a newline in a value", []string{`k: "two\nlines"`}, `annotation "k"`},
		{"a tab in a value", []string{`k: "a\tb"`}, `annotation "k"`},
		{"a bidi override in a value", []string{`k: "a\u202eb"`}, `annotation "k"`},
		{"a zero-width space in a value", []string{`k: "a\u200bb"`}, `annotation "k"`},
		{"a value of 121 runes", []string{"k: " + strconv.Quote(strings.Repeat("é", 121))}, `annotation "k"`},
		{"a nested map as a value", []string{"k:", "  nested: x"}, "Annotations"},
		{"a list as a value", []string{"k: [a, b]"}, "Annotations"},
		{"a repeated key", []string{"k: a", "k: b"}, `"k"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, verr := ParseManifest([]byte(annotated(tc.lines...)))
			if verr == nil {
				t.Fatal("accepted")
			}
			if verr.Code != "plugin.index.manifest" {
				t.Fatalf("code = %s (%s), want plugin.index.manifest", verr.Code, verr.Message)
			}
			if !strings.Contains(verr.Message, tc.why) {
				t.Fatalf("message = %q, want it to mention %s", verr.Message, tc.why)
			}
			for _, r := range verr.Message {
				if (r < 0x20 && r != '\n') || r == '\u202e' || r == '\u200b' {
					t.Fatalf("message holds %U raw: %q", r, verr.Message)
				}
			}
		})
	}

	t.Run("annotations as something else than a map", func(t *testing.T) {
		for _, doc := range []string{
			goodManifest + "annotations: [a, b]\n",
			goodManifest + "annotations: text\n",
			goodManifest + "annotations: 3\n",
		} {
			if _, verr := ParseManifest([]byte(doc)); verr == nil || verr.Code != "plugin.index.manifest" {
				t.Fatalf("%q: verr = %v, want plugin.index.manifest", doc[len(goodManifest):], verr)
			}
		}
	})

	// The refusal is the same whichever of several bad entries comes first in
	// the map, so an index author is told the same thing on every run.
	t.Run("the same refusal each time", func(t *testing.T) {
		doc := annotated("Zed: v", "Alpha: v", "Mid: v")
		first := ""
		for range 20 {
			_, verr := ParseManifest([]byte(doc))
			if verr == nil {
				t.Fatal("accepted")
			}
			if first == "" {
				first = verr.Message
			}
			if verr.Message != first {
				t.Fatalf("message changed between runs: %q then %q", first, verr.Message)
			}
		}
	})
}

// Nothing reads an annotation: it is not a claim, it is not compared with the
// binary, it is not recorded, and it is not shown. A key that looks like a
// claim — signature, trusted, verified — parses and changes nothing a person
// or the gate sees, which is what makes it safe to accept at all.
func TestAnAnnotationIsInertWhateverItIsCalled(t *testing.T) {
	plain, verr := ParseManifest([]byte(goodManifest))
	if verr != nil {
		t.Fatal(verr)
	}
	m, verr := ParseManifest([]byte(annotated(
		"signature: https://example.com/pg.sig", "trusted: \"true\"", "verified: \"true\"",
		"safety: read", "needs: kubeconfig", "grant: none",
	)))
	if verr != nil {
		t.Fatalf("refused: %v", verr)
	}
	if m.SafetyLine() != plain.SafetyLine() {
		t.Fatalf("SafetyLine = %q, want %q: an annotation altered the claim a person reads",
			m.SafetyLine(), plain.SafetyLine())
	}
	if len(m.Needs) != 0 || m.Capabilities[0].Safety != "read" || m.Capabilities[1].Safety != "write" {
		t.Fatalf("an annotation reached a claim: %+v", m)
	}
}

// A homepage is shown to a person and comes from an index, so it is held to
// what a summary is held to. url.Parse admits a bidi override and an escape
// in a path, and a link that displays as another one is exactly what the
// override is for.
func TestAHomepageHoldsNoInvisibleCharacters(t *testing.T) {
	mutate := func(to string) string {
		return strings.Replace(goodManifest, "homepage: https://example.com/pg", "homepage: "+to, 1)
	}
	for name, hp := range map[string]string{
		"a bidi override":          `"https://example.com/pg\u202egnp.exe"`,
		"an escape":                `"https://example.com/pg\e[31m"`,
		"a zero-width space":       `"https://example.com/p\u200bg"`,
		"a newline":                `"https://example.com/pg\nname: x"`,
		"a tab":                    `"https://example.com/\tpg"`,
		"a homepage of 201":        "https://example.com/" + strings.Repeat("x", 181),
		"a homepage of a megabyte": "https://example.com/" + strings.Repeat("x", 900_000),
	} {
		t.Run(name, func(t *testing.T) {
			_, verr := ParseManifest([]byte(mutate(hp)))
			if verr == nil {
				t.Fatal("accepted")
			}
			if verr.Code != "plugin.index.manifest" || !strings.Contains(verr.Message, "homepage") {
				t.Fatalf("verr = %v, want plugin.index.manifest naming homepage", verr)
			}
			if len(verr.Message) > 400 {
				t.Fatalf("message is %d bytes, and echoes the homepage it refused", len(verr.Message))
			}
			for _, r := range verr.Message {
				if r < 0x20 || r == '\u202e' || r == '\u200b' {
					t.Fatalf("message holds %U raw: %q", r, verr.Message)
				}
			}
		})
	}
	if _, verr := ParseManifest([]byte(mutate("https://example.com/" + strings.Repeat("x", 180)))); verr != nil {
		t.Fatalf("a homepage of 200 characters was refused: %v", verr)
	}
	if _, verr := ParseManifest([]byte(goodManifest)); verr != nil {
		t.Fatal(verr)
	}
}

// writtenManifest is goodManifest as the writer marshals it, captured before
// the two keys existed: the sequences are not indented, and neither key is
// there. A struct tag that lost its omitempty changes it.
const writtenManifest = `name: pg
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
`

// The index writer does not know either key yet, and that is the safeguard
// and not an omission: an index regenerated by a rta that wrote apiVersion
// would be refused by every rta older than it, so the key is written only by
// a release that has decided to, for a manifest that needs it. Pinned two
// ways — the bytes the writer's marshaller produces for a fixed manifest, and
// the top-level keys of a real generated one.
func TestAGeneratedManifestStatesNeitherNewKey(t *testing.T) {
	m, verr := ParseManifest([]byte(goodManifest))
	if verr != nil {
		t.Fatal(verr)
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != writtenManifest {
		t.Fatalf("the writer's bytes changed:\n%s\nwant\n%s", got, writtenManifest)
	}

	bin := hello(t)
	doc, _ := generate(t, GenerateRequest{Binary: bin, Homepage: "https://example.com/hello",
		Platforms: hostPlatform(fileURL(bin))})
	var keys []string
	for _, line := range strings.Split(string(doc), "\n") {
		if line != "" && line[0] != ' ' && line[0] != '#' && line[0] != '-' {
			key, _, _ := strings.Cut(line, ":")
			keys = append(keys, key)
		}
	}
	if got := strings.Join(keys, " "); got != "name version summary homepage platforms capabilities" {
		t.Fatalf("a generated manifest's keys are %q; a plugin that needs no apiVersion is written as it always was", got)
	}
	parsed, verr := ParseManifest(doc)
	if verr != nil {
		t.Fatal(verr)
	}
	if parsed.APIVersion != "" || parsed.Annotations != nil {
		t.Fatalf("parsed APIVersion %q, Annotations %v; want neither", parsed.APIVersion, parsed.Annotations)
	}
}

// The generator keeps its own flag's message for a homepage the parser would
// refuse, rather than the parser's, which is about a manifest.
func TestTheGeneratorRefusesAHomepageThatWouldNotDisplayAsItself(t *testing.T) {
	bin := hello(t)
	_, _, verr := Generate(t.Context(), GenerateRequest{Binary: bin, Homepage: "https://example.com/a\u202eb",
		Platforms: hostPlatform(fileURL(bin))})
	if verr == nil || verr.Code != "plugin.manifest.homepage" {
		t.Fatalf("verr = %v, want plugin.manifest.homepage", verr)
	}
}

// The writer states rta/v2 for a plugin a reader of rta/v1 alone misreads, a
// renamed key or a key that starts another, and nothing for any other, so
// regenerating an index hides no other plugin from an older rta.
func TestTheWriterStatesV2OnlyForAPluginAnOlderRtaWouldMisread(t *testing.T) {
	declare := func(renamed map[string]string, keys ...string) plugin.Plugin {
		inputs := make([]plugin.Field, len(keys))
		for i, k := range keys {
			inputs[i] = plugin.Field{Name: strings.ReplaceAll(k, ".", "-"), Type: plugin.String, Config: k}
		}
		return plugin.Plugin{Name: "demo", Summary: "s", Renamed: renamed,
			Capabilities: []plugin.Capability{{ID: "demo.get", Summary: "g", Safety: plugin.Read, Inputs: inputs}}}
	}
	for _, tc := range []struct {
		name string
		p    plugin.Plugin
		want string
	}{
		{"plain keys", declare(nil, "host", "query.limit", "limit"), ""},
		{"a renamed key", declare(map[string]string{"sslmode": "tls"}, "tls"), APIVersionV2},
		{"a key that starts another", declare(nil, "database", "database.list.limit"), APIVersionV2},
		{"one level down", declare(nil, "query.list", "query.list.limit"), APIVersionV2},
		{"a shared ending is no start", declare(nil, "limit", "query.limit", "list.limit"), ""},
	} {
		if got := claimsOf(tc.p, "1.0.0", "").APIVersion; got != tc.want {
			t.Errorf("%s: apiVersion %q, want %q", tc.name, got, tc.want)
		}
	}
}
