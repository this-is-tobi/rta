package plugindist

import (
	"context"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// hostPlatform is the only one a generated manifest can claim here, because
// reading a declaration means running the binary.
func hostPlatform(url string) []PlatformSource {
	return []PlatformSource{{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: url}}
}

// fileURL is the file:// URL naming a local path, which is not the scheme
// concatenated with the path on every platform — the form these tests used
// everywhere.
//
// On Unix a path already begins with "/", so the concatenation happens to
// produce the three slashes a file URL needs. A Windows path begins with a
// drive letter and separates with backslashes, so it produced
// `file://C:\Users\...`, where "C:" parses as the host and the backslashes are
// not legal in a path — checkArtifactURL refused it before any test reached
// what it meant to test.
func fileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		// C:/Users/… → /C:/Users/…, which is what file:///C:/Users/… means.
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func generate(t *testing.T, req GenerateRequest) ([]byte, Manifest) {
	t.Helper()
	doc, m, verr := Generate(context.Background(), req, io.Discard)
	if verr != nil {
		t.Fatalf("generate: %v (hint: %s)", verr, verr.Hint)
	}
	return doc, m
}

// The claim the whole generator makes: what comes out is what the binary
// says, and it installs. Anything hand-written has to survive verifyClaims at
// somebody else's machine; this survives it here, through the real Install.
func TestAGeneratedManifestInstalls(t *testing.T) {
	testData(t)
	bin := hello(t)
	doc, m := generate(t, GenerateRequest{Binary: bin, Platforms: hostPlatform(fileURL(bin))})

	// "dev" is what an unstamped build honestly is: hello() builds with a
	// plain `go build`, so nothing set main.version, and the declaration says
	// so rather than naming a release nobody cut. The stamped half is
	// TestABuildStampIsWhatTheManifestClaims below.
	if m.Name != "hello" || m.Version != "dev" {
		t.Fatalf("manifest = %q %q, want the declaration's own", m.Name, m.Version)
	}
	if m.Summary != "A worked example of an rta plugin" {
		t.Fatalf("summary = %q, want the one in the binary", m.Summary)
	}
	if len(m.Capabilities) != 2 {
		t.Fatalf("capabilities = %+v, want the two hello declares", m.Capabilities)
	}
	for _, c := range m.Capabilities {
		if c.Safety != string(plugin.Read) || c.Grant {
			t.Fatalf("%s = %s grant:%v, want read and no grant", c.ID, c.Safety, c.Grant)
		}
		if c.Summary == "" {
			t.Fatalf("%s carries no summary, and search is mostly summaries", c.ID)
		}
	}
	if m.Platforms[0].SHA256 != sha256Of(t, bin) {
		t.Fatalf("sha256 = %s, want the artifact's own", m.Platforms[0].SHA256)
	}
	if FileName(m) != "index/hello.yaml" {
		t.Fatalf("FileName = %q", FileName(m))
	}

	attach(t, string(doc))
	rep, verr := Install(context.Background(), "hello", io.Discard)
	if verr != nil {
		t.Fatalf("installing the generated manifest: %v (hint: %s)", verr, verr.Hint)
	}
	if rep.Digest != sha256Of(t, bin) {
		t.Fatalf("digest = %s", rep.Digest)
	}
}

// An index is a git repository, so a regeneration that reorders fields is a
// diff somebody has to read and nobody can check.
func TestRegeneratingAnUnchangedPluginChangesNothing(t *testing.T) {
	bin := hello(t)
	req := GenerateRequest{Binary: bin, Platforms: []PlatformSource{
		{OS: "linux", Arch: "arm64", URL: fileURL(bin)},
		{OS: "darwin", Arch: "arm64", URL: fileURL(bin)},
		{OS: "linux", Arch: "amd64", URL: fileURL(bin)},
	}}
	first, m := generate(t, req)
	second, _ := generate(t, req)
	if string(first) != string(second) {
		t.Fatal("two generations of one plugin produced different bytes")
	}
	got := m.Offered()
	if got != "darwin/arm64, linux/amd64, linux/arm64" {
		t.Fatalf("platforms = %q, want them sorted", got)
	}
}

// The generator is held to the grammar it generates for: whatever it returns
// has already been through the parser an index will hold it to.
// The version a manifest states is a fact about a release, and the only way
// that fact reaches the artifact is the linker. Every plugin in this
// repository declares `Version: version` against a `var version = "dev"`, so
// this is the whole chain the release depends on — ldflag, declaration,
// generated manifest — and nothing else in the tree exercises it end to end.
//
// The eleven plugins here each claimed a hand-written "0.1.0" until this
// existed, four minor versions behind the rta they shipped beside, which is
// what a number nobody's build sets does on its own.
func TestABuildStampIsWhatTheManifestClaims(t *testing.T) {
	bin := filepath.Join(t.TempDir(), pluginhost.BinaryName("hello"))
	build := exec.Command("go", "build",
		"-ldflags", "-X main.version=v9.9.9-stamped",
		"-o", bin, "../../examples/plugin-hello")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building a stamped hello: %v: %s", err, out)
	}
	_, m := generate(t, GenerateRequest{Binary: bin, Platforms: hostPlatform(fileURL(bin))})
	if m.Version != "v9.9.9-stamped" {
		t.Fatalf("version = %q, want the one the linker stamped", m.Version)
	}
}

func TestWhatComesOutParsesBackIn(t *testing.T) {
	bin := hello(t)
	doc, m := generate(t, GenerateRequest{
		Binary:    bin,
		Version:   "v9.9.9",
		Homepage:  "https://example.com/hello",
		Platforms: hostPlatform(fileURL(bin)),
	})
	reparsed, verr := ParseManifest(doc)
	if verr != nil {
		t.Fatalf("the generated manifest does not parse: %v", verr)
	}
	if reparsed.Version != "v9.9.9" {
		t.Fatalf("version = %q, want the one given rather than the declaration's", reparsed.Version)
	}
	if reparsed.Homepage != m.Homepage {
		t.Fatalf("homepage = %q", reparsed.Homepage)
	}
	if !strings.HasPrefix(string(doc), "# rta-plugin-hello,") {
		t.Fatalf("no header naming the artifact:\n%s", firstLine(string(doc), ""))
	}
}

// A published artifact is not on this machine, so its hash comes from the
// checksums file a release publishes — looked up by filename, which is what
// the URL's last segment is.
func TestAPublishedArtifactTakesItsHashFromTheChecksumsFile(t *testing.T) {
	bin := hello(t)
	sums, verr := ParseChecksums([]byte(
		"3d1d0b3a0e2d2b0a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2e3d4c5b6a  hello_linux_amd64.tar.gz\n" +
			"aa1d0b3a0e2d2b0a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2e3d4c5baa *dist/hello_linux_arm64.tar.gz\n"))
	if verr != nil {
		t.Fatalf("parsing checksums: %v", verr)
	}
	_, m := generate(t, GenerateRequest{
		Binary:    bin,
		Checksums: sums,
		Platforms: []PlatformSource{
			{OS: "linux", Arch: "amd64", URL: "https://example.com/d/hello_linux_amd64.tar.gz"},
			{OS: "linux", Arch: "arm64", URL: "https://example.com/d/hello_linux_arm64.tar.gz"},
		},
	})
	if m.Platforms[0].SHA256[:2] != "3d" || m.Platforms[1].SHA256[:2] != "aa" {
		t.Fatalf("checksums = %+v, want the ones stated for each filename", m.Platforms)
	}
	// An archive needs the member to extract, and nothing else can name it.
	for _, p := range m.Platforms {
		if p.Bin != "rta-plugin-hello" {
			t.Fatalf("%s/%s bin = %q", p.OS, p.Arch, p.Bin)
		}
	}
}

// Every refusal below is one that would otherwise have surfaced at a
// stranger's install, as a message about an index they do not maintain.
func TestTheGeneratorRefusesWhatWouldFailAtSomebodyElsesInstall(t *testing.T) {
	bin := hello(t)
	archive := filepath.Join(t.TempDir(), "hello.tar.gz")
	tarGz(t, bin, "rta-plugin-hello", archive)

	cases := []struct {
		name string
		req  GenerateRequest
		code string
		says string
	}{
		{"no platform at all",
			GenerateRequest{Binary: bin},
			"plugin.manifest.platforms", "nobody can install"},
		{"a zip rta has no reader for",
			GenerateRequest{Binary: bin, Platforms: hostPlatform("https://example.com/hello.zip")},
			"plugin.manifest.platform", "rta extracts .tar.gz only"},
		{"a published artifact nothing states a hash for",
			GenerateRequest{Binary: bin, Platforms: hostPlatform("https://example.com/hello.tar.gz")},
			"plugin.manifest.platform", "nothing states the sha256"},
		{"a bin: the archive does not hold",
			GenerateRequest{Binary: bin, Platforms: []PlatformSource{
				{OS: "linux", Arch: "amd64", URL: fileURL(archive), Bin: "wrong/place"}}},
			"plugin.manifest.platform", "holds no \"wrong/place\""},
		{"a file that is not there",
			GenerateRequest{Binary: bin,
				Platforms: hostPlatform("file:///nowhere/at/all/hello.tar.gz")},
			"plugin.manifest.platform", "hashing"},
		// The two below would be caught by the round trip anyway, and its
		// message is "the generated manifest is wrong" — the right sentence
		// for a bug in the generator and the wrong one for a value somebody
		// typed. These name the flag instead.
		{"a homepage that is not https",
			GenerateRequest{Binary: bin, Homepage: "http://example.com",
				Platforms: hostPlatform(fileURL(bin))},
			"plugin.manifest.homepage", "is not an https URL"},
		{"one platform stated twice",
			GenerateRequest{Binary: bin, Platforms: []PlatformSource{
				{OS: "linux", Arch: "amd64", URL: fileURL(bin)},
				{OS: "linux", Arch: "amd64", URL: fileURL(bin)}}},
			"plugin.manifest.platform", "given twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, verr := Generate(context.Background(), c.req, io.Discard)
			if verr == nil {
				t.Fatal("generated a manifest that could not be installed from")
			}
			if verr.Code != c.code || !strings.Contains(verr.Message, c.says) {
				t.Fatalf("verr = %s: %q, want %s saying %q", verr.Code, verr.Message, c.code, c.says)
			}
		})
	}
}

// A bin: claim is a guess about somebody else's archive everywhere except
// here, where the archive is in reach.
func TestABinClaimIsProvedWhileTheArchiveIsInReach(t *testing.T) {
	bin := hello(t)
	archive := filepath.Join(t.TempDir(), "hello.tar.gz")
	tarGz(t, bin, "somewhere/inside/rta-plugin-hello", archive)

	// The default guess is the plain binary name, which this archive does not
	// hold — and saying so here is the whole point.
	if _, _, verr := Generate(context.Background(), GenerateRequest{
		Binary: bin, Platforms: hostPlatform(fileURL(archive))}, io.Discard); verr == nil {
		t.Fatal("a bin: nothing checked was written down as a claim")
	}
	_, m := generate(t, GenerateRequest{Binary: bin, Platforms: []PlatformSource{
		{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: fileURL(archive),
			Bin: "somewhere/inside/rta-plugin-hello"}}})
	if m.Platforms[0].Bin != "somewhere/inside/rta-plugin-hello" {
		t.Fatalf("bin = %q", m.Platforms[0].Bin)
	}
}

func TestChecksumsFilesAreReadOrRefusedWholesale(t *testing.T) {
	if _, verr := ParseChecksums([]byte("not a checksum line\n")); verr == nil {
		t.Fatal("a file that is not checksums was read as checksums")
	}
	if _, verr := ParseChecksums([]byte("   \n\n")); verr == nil {
		t.Fatal("an empty checksums file was accepted")
	}
	if _, verr := ParseChecksums(make([]byte, checksumsCap+1)); verr == nil {
		t.Fatal("an oversized file was read")
	}
	sums, verr := ParseChecksums([]byte("\n" +
		"3d1d0b3a0e2d2b0a3f4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0a1f2e3d4c5b6a  a.tar.gz\r\n"))
	if verr != nil || sums["a.tar.gz"][:2] != "3d" {
		t.Fatalf("sums = %v, %v", sums, verr)
	}
}

// A declaration missing what an index entry is made of names the plugin to
// fix rather than the manifest that could not be written.
func TestAPluginThatCannotBePublishedSaysWhichPartIsMissing(t *testing.T) {
	bin := hello(t)
	_, _, verr := Generate(context.Background(), GenerateRequest{
		Binary:    bin,
		Platforms: hostPlatform(fileURL(bin)),
		Version:   strings.Repeat("v", 41),
	}, io.Discard)
	if verr == nil || verr.Code != "plugin.manifest.write" {
		t.Fatalf("verr = %v, want the round-trip to catch an over-long version", verr)
	}
	if !strings.Contains(verr.Message, "rta would refuse") {
		t.Fatalf("message = %q", verr.Message)
	}
}

// Writing into an index is placement, and the layout is the claim: Manifests
// refuses a file whose name and content disagree, so the generator must be
// the thing that decides the filename.
func TestAGeneratedManifestLandsWhereTheIndexLooksForIt(t *testing.T) {
	testData(t)
	bin := hello(t)
	doc, m := generate(t, GenerateRequest{Binary: bin, Platforms: hostPlatform(fileURL(bin))})

	dir := t.TempDir()
	dest := filepath.Join(dir, FileName(m))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, doc, 0o644); err != nil {
		t.Fatal(err)
	}
	listed, bad := Manifests(Index{Name: "lab", Dir: dir})
	if len(bad) > 0 {
		t.Fatalf("the index refused what the generator wrote: %v", bad[0])
	}
	if len(listed) != 1 || listed[0].Manifest.Name != "hello" {
		t.Fatalf("listed = %+v", listed)
	}
}

// A plugin rta will register has to be a plugin somebody can publish, and the
// two grammars are written in different packages with their own caps: one for
// what an author may declare, one for what an index entry may claim. If the
// manifest's ever became the tighter of the two, the plugin it locked out
// would work perfectly and simply have no way into an index — and nothing
// about that failure would point at the number that caused it.
//
// The declaration's own limit is derived rather than restated here, because a
// second copy of the constant is the drift this is meant to catch.
func TestEverySummaryAPluginMayDeclareFitsInAManifest(t *testing.T) {
	declarable := longestAccepted(t, func(n int) bool {
		return plugin.Plugin{
			Name: "lab", Summary: strings.Repeat("s", n),
			Capabilities: []plugin.Capability{
				{ID: "lab.get", Summary: "g", Safety: plugin.Read, Run: noopRun}},
		}.Validate() == nil
	})
	publishable := longestAccepted(t, func(n int) bool {
		_, verr := ParseManifest([]byte("name: lab\nversion: 0.1.0\nsummary: " +
			strings.Repeat("s", n) + "\n" +
			"platforms:\n  - os: linux\n    arch: amd64\n    url: https://example.com/lab\n" +
			"    sha256: " + strings.Repeat("a", 64) + "\n" +
			"capabilities:\n  - id: lab.get\n    safety: read\n"))
		return verr == nil
	})
	if publishable < declarable {
		t.Fatalf("a plugin may declare a %d-rune summary and an index may only claim %d, "+
			"so a plugin between the two registers and cannot be published", declarable, publishable)
	}
}

func noopRun(context.Context, plugin.Request) (view.View, error) {
	return view.Text{Body: "ok"}, nil
}

// longestAccepted finds the largest length ok still accepts, by doubling then
// bisecting. It assumes only that acceptance is monotonic in length, which is
// what a cap is.
func longestAccepted(t *testing.T, ok func(n int) bool) int {
	t.Helper()
	if !ok(1) {
		t.Fatal("nothing of length 1 was accepted — the probe is measuring something else")
	}
	hi := 1
	for ok(hi) {
		hi *= 2
		if hi > 1<<20 {
			t.Fatal("no cap found below a megabyte")
		}
	}
	lo := hi / 2
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if ok(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}
