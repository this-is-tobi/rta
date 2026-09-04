package plugindist

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
	"github.com/this-is-tobi/rule-them-all/internal/plugintrust"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// helloOnce builds examples/plugin-hello once for the package — pluginhost's
// own pattern, for its reason: install's verification launch is the real
// spawn path, sandbox included, and a fake that skipped the fork would skip
// the thing being verified.
var (
	helloOnce sync.Once
	helloPath string
	helloErr  error
)

func hello(t *testing.T) string {
	t.Helper()
	helloOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rta-dist-hello-*")
		if err != nil {
			helloErr = err
			return
		}
		// pluginhost.BinaryName, for the reason its twin in that package
		// carries: a fixture standing in for an installed plugin has to be
		// named the way one is named, and on Windows that means the .exe
		// without which nothing will execute it.
		helloPath = filepath.Join(dir, pluginhost.BinaryName("hello"))
		cmd := exec.Command("go", "build", "-o", helloPath, "../../examples/plugin-hello")
		if out, err := cmd.CombinedOutput(); err != nil {
			helloErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if helloErr != nil {
		t.Fatalf("building the example plugin: %v", helloErr)
	}
	return helloPath
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// helloManifest writes a manifest whose claims are true of the built hello
// plugin: its real declaration, its real checksum, a file:// URL to artifact.
func helloManifest(t *testing.T, artifact, bin string) string {
	t.Helper()
	doc := fmt.Sprintf(`name: hello
version: 0.1.0
summary: the example plugin
platforms:
  - os: %s
    arch: %s
    url: %s
    sha256: %s
`, runtimeGOOS(), runtimeGOARCH(), fileURL(artifact), sha256Of(t, artifact))
	if bin != "" {
		doc += "    bin: " + bin + "\n"
	}
	doc += `capabilities:
  - id: hello.greet
    summary: greet someone
    safety: read
  - id: hello.languages
    safety: read
`
	return doc
}

func runtimeGOOS() string   { return runtime.GOOS }
func runtimeGOARCH() string { return runtime.GOARCH }

// tarGz packs one file into a .tar.gz under the given member name.
func tarGz(t *testing.T, src, member, dest string) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: member, Mode: 0o755,
		Size: int64(len(raw)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(raw); err != nil {
		t.Fatal(err)
	}
	for _, c := range []io.Closer{tw, gz, out} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// attach makes an index called "lab" carrying one hello manifest.
func attach(t *testing.T, manifest string) {
	t.Helper()
	repo := gitFixture(t, map[string]string{"hello": manifest})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatal(verr)
	}
}

// An index attached from a path may name a file:// artifact — that is the
// local rehearsal `rta plugin manifest --index` is for, and every test in this
// package relies on it. An index attached from a network URL may not, and this
// is the difference.
//
// Without the gate, an index cloned from anywhere could name
// file:///home/you/.ssh/id_ed25519 as its artifact and rta would open it, in
// its own unconfined process, while pluginhost's denyset refuses a *plugin*
// that exact path. The bytes go nowhere — the checksum gate stops the install
// before anything launches — but the read happens, and the mismatch message
// hands back the first 48 bits of the real file's sha256.
//
// The origin is re-pointed after the attach rather than cloned from a real
// remote, because what the gate reads is remote.origin.url and what it has to
// refuse is any origin that is not a path on this machine. That also covers
// the case a cache could not: an index attached locally and re-pointed later.
func TestARemotelyAttachedIndexMayNotNameALocalFile(t *testing.T) {
	testData(t)
	ctx := context.Background()
	bin := hello(t)
	attach(t, helloManifest(t, bin, ""))

	ix, ok := IndexByName("lab")
	if !ok {
		t.Fatal("the index did not attach")
	}
	// It still installs while the origin is the path it was attached from.
	if _, verr := Install(ctx, "hello", io.Discard); verr != nil {
		t.Fatalf("a locally attached index was refused its own file:// artifact: %v", verr)
	}
	if _, verr := Remove("hello"); verr != nil {
		t.Fatal(verr)
	}

	setURL := exec.Command("git", "-C", ix.Dir, "remote", "set-url", "origin",
		"https://github.com/somebody/rta-index.git")
	if out, err := setURL.CombinedOutput(); err != nil {
		t.Fatalf("re-pointing the origin: %v: %s", err, out)
	}

	_, verr := Install(ctx, "hello", io.Discard)
	if verr == nil {
		t.Fatal("an index attached from a network URL opened a local file")
	}
	if verr.Code != "plugin.install.localfile" {
		t.Fatalf("code = %s, want plugin.install.localfile: %v", verr.Code, verr)
	}
	if !strings.Contains(verr.Message, "github.com/somebody/rta-index.git") {
		t.Errorf("the refusal does not name the origin it refused: %s", verr.Message)
	}
	assertNothingInstalled(t, sha256Of(t, bin))
}

// The whole arc, both artifact shapes: fetch, checksum, verification launch,
// store, symlink, trust, lockfile — every durable fact checked against what
// rta computed, none against the claim.
func TestInstallVerifiesPlacesTrustsAndRecords(t *testing.T) {
	for _, shape := range []string{"bare binary", "tar.gz"} {
		t.Run(shape, func(t *testing.T) {
			testData(t)
			bin := hello(t)
			artifact, member := bin, ""
			if shape == "tar.gz" {
				artifact = filepath.Join(t.TempDir(), "hello.tar.gz")
				member = "dist/rta-plugin-hello"
				tarGz(t, bin, member, artifact)
			}
			attach(t, helloManifest(t, artifact, member))

			rep, verr := Install(context.Background(), "hello", io.Discard)
			if verr != nil {
				t.Fatalf("install: %v (hint: %s)", verr, verr.Hint)
			}
			wantDigest := sha256Of(t, bin)
			if rep.Digest != wantDigest {
				t.Fatalf("digest = %s, want the binary's own %s", rep.Digest, wantDigest)
			}
			if rep.Signature != "none stated" {
				t.Fatalf("signature = %q", rep.Signature)
			}
			if rep.Declared.Name != "hello" || len(rep.Declared.Capabilities) != 2 {
				t.Fatalf("declared = %+v", rep.Declared)
			}

			// The store holds the bytes where the layout says, executable.
			placed := filepath.Join(StoreDir(), "hello", wantDigest, binaryName("hello"))
			info, err := os.Stat(placed)
			// The execute bit only where there is one: Go derives a Windows
			// FileMode from the read-only attribute, so this is 0 for every
			// file there and asserting it would fail on a correct install.
			if err != nil || (pluginhost.ExeSuffix == "" && info.Mode()&0o111 == 0) {
				t.Fatalf("stored binary: %v, mode %v", err, info)
			}
			if got, ok := CurrentDigest("hello"); !ok || got != wantDigest {
				t.Fatalf("CurrentDigest = %q, %v", got, ok)
			}
			// The bin/ link resolves to the stored file.
			link := filepath.Join(BinDir(), binaryName("hello"))
			if resolved, err := filepath.EvalSymlinks(link); err != nil ||
				sha256Of(t, resolved) != wantDigest {
				t.Fatalf("bin link resolves to %q (%v)", resolved, err)
			}
			// Install is the trust decision.
			if !plugintrust.Load().Trusts(wantDigest) {
				t.Fatal("the installed digest is not trusted — a second command would be needed")
			}
			// The lockfile records what rta computed.
			e, ok := LockedFor("hello")
			if !ok || e.Digest != wantDigest || e.Index != "lab" || e.Version != "0.1.0" {
				t.Fatalf("lock entry = %+v, %v", e, ok)
			}
			if e.InstalledAt.IsZero() || !strings.HasPrefix(e.URL, "file://") {
				t.Fatalf("lock entry = %+v", e)
			}

			// Installing what is installed points at upgrade instead.
			if _, verr := Install(context.Background(), "hello", io.Discard); verr == nil ||
				verr.Code != "plugin.install.installed" {
				t.Fatalf("second install: %v", verr)
			}
		})
	}
}

// A checksum that does not match the bytes is the index lying or the
// transport rewriting; either way the bytes are refused, the index is named,
// and nothing durable happens.
func TestAChecksumLieIsRefusedNamingTheIndex(t *testing.T) {
	testData(t)
	bin := hello(t)
	doc := helloManifest(t, bin, "")
	doc = strings.Replace(doc, sha256Of(t, bin), strings.Repeat("ab", 32), 1)
	attach(t, doc)

	_, verr := Install(context.Background(), "hello", io.Discard)
	if verr == nil || verr.Code != "plugin.install.checksum" {
		t.Fatalf("verr = %v", verr)
	}
	if !strings.Contains(verr.Message, `"lab"`) {
		t.Fatalf("the refusal does not name the index: %q", verr.Message)
	}
	assertNothingInstalled(t, sha256Of(t, bin))
}

// The check no opaque-executable manager can perform: the binary's own
// declaration against the index's claim, refused in every direction that
// matters — safety, grant, an extra capability, a promised one missing.
func TestADeclarationLieIsRefused(t *testing.T) {
	lies := []struct {
		name string
		edit func(string) string
		why  string
	}{
		{"safety class", func(d string) string {
			return strings.Replace(d, "safety: read", "safety: write", 1)
		}, "hello.greet is read, not write"},
		{"grant", func(d string) string {
			return strings.Replace(d, "id: hello.greet\n    summary: greet someone\n    safety: read",
				"id: hello.greet\n    summary: greet someone\n    safety: read\n    grant: true", 1)
		}, "needs no grant"},
		{"a capability the binary lacks", func(d string) string {
			return d + "  - id: hello.doom\n    safety: destructive\n"
		}, "does not declare hello.doom"},
		{"a capability the index omits", func(d string) string {
			return strings.Replace(d, "  - id: hello.languages\n    safety: read\n", "", 1)
		}, "also declares hello.languages"},
		// The claim an operator most wants before deciding, and the one an
		// index has most reason to leave quiet. hello asks for nothing, so an
		// index saying otherwise is inventing a question it never posed.
		{"a credential location the binary never asked for", func(d string) string {
			return d + "needs:\n  - aws\n"
		}, "does not ask for aws"},
	}
	for _, lie := range lies {
		t.Run(lie.name, func(t *testing.T) {
			testData(t)
			bin := hello(t)
			attach(t, lie.edit(helloManifest(t, bin, "")))
			_, verr := Install(context.Background(), "hello", io.Discard)
			if verr == nil || verr.Code != "plugin.install.claims" {
				t.Fatalf("verr = %v", verr)
			}
			if !strings.Contains(verr.Message, lie.why) {
				t.Fatalf("refusal = %q, want it to say %q", verr.Message, lie.why)
			}
			assertNothingInstalled(t, sha256Of(t, bin))
		})
	}
}

// assertNothingInstalled is the other half of every refusal: no store entry,
// no trust, no lock record, no staging debris.
func assertNothingInstalled(t *testing.T, digest string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(StoreDir(), "hello")); err == nil {
		t.Error("a refused install left a store entry")
	}
	if plugintrust.Load().Trusts(digest) {
		t.Error("a refused install trusted the digest")
	}
	if _, held := LockedFor("hello"); held {
		t.Error("a refused install wrote a lock entry")
	}
	entries, _ := os.ReadDir(filepath.Join(dataDirOf(t), "plugins"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Errorf("staging debris left behind: %s", e.Name())
		}
	}
}

func dataDirOf(t *testing.T) string {
	t.Helper()
	return os.Getenv("RTA_DATA_DIR")
}

// Remove takes everything back — store, trust, lock — and names the config
// statements now pointing at nothing, without touching them.
func TestRemoveUninstallsAndNamesOrphans(t *testing.T) {
	testData(t)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	bin := hello(t)
	attach(t, helloManifest(t, bin, ""))
	rep, verr := Install(context.Background(), "hello", io.Discard)
	if verr != nil {
		t.Fatal(verr)
	}
	pin := "hello@" + rep.Digest[:12]
	if err := config.Write(config.Config{
		Plugins: map[string]map[string]any{pin: {"lang": "fr"}},
		Profiles: map[string]config.Profile{
			"staging": {Plugins: map[string]config.Connection{pin: {}}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	removed, verr := Remove("hello")
	if verr != nil {
		t.Fatalf("remove: %v", verr)
	}
	if len(removed.Digests) != 1 || removed.Digests[0] != rep.Digest {
		t.Fatalf("removed digests = %v", removed.Digests)
	}
	if plugintrust.Load().Trusts(rep.Digest) {
		t.Error("the digest is still trusted after remove")
	}
	if _, err := os.Stat(filepath.Join(StoreDir(), "hello")); err == nil {
		t.Error("the store entry survived remove")
	}
	if _, err := os.Readlink(filepath.Join(BinDir(), binaryName("hello"))); err == nil {
		t.Error("the bin link survived remove")
	}
	if _, held := LockedFor("hello"); held {
		t.Error("the lock entry survived remove")
	}
	want := []string{"plugins." + pin, "profiles.staging." + pin}
	if len(removed.Orphans) != 2 || removed.Orphans[0] != want[0] || removed.Orphans[1] != want[1] {
		t.Fatalf("orphans = %v, want %v", removed.Orphans, want)
	}

	if _, verr := Remove("hello"); verr == nil || verr.Code != "plugin.remove.unknown" {
		t.Fatalf("removing what is not there: %v", verr)
	}
}

// Upgrade moves the pin, keeps the old digest's store directory for
// rollback, and reports the move; the same index state twice is up to date.
func TestUpgradeMovesKeepsRollbackAndReportsUpToDate(t *testing.T) {
	testData(t)
	bin := hello(t)
	repo := gitFixture(t, map[string]string{"hello": helloManifest(t, bin, "")})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatal(verr)
	}
	first, verr := Install(context.Background(), "hello", io.Discard)
	if verr != nil {
		t.Fatal(verr)
	}

	up, verr := Upgrade(context.Background(), "hello", io.Discard)
	if verr != nil {
		t.Fatalf("upgrade against an unchanged index: %v", verr)
	}
	if !up.UpToDate {
		t.Fatal("an unchanged index upgraded to itself")
	}

	// The upstream ships different bytes: the same source built with the
	// linker stripping symbols — a different artifact, the same declaration.
	dir := t.TempDir()
	rebuilt := filepath.Join(dir, binaryName("hello"))
	cmd := exec.Command("go", "build", "-ldflags=-s -w", "-o", rebuilt, "../../examples/plugin-hello")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rebuilding: %v: %s", err, out)
	}
	if sha256Of(t, rebuilt) == first.Digest {
		t.Skip("the stripped build hashed identically; nothing to upgrade between")
	}
	writeManifests(t, repo, map[string]string{"hello": helloManifest(t, rebuilt, "")})
	commitAll(t, repo, "0.1.1")
	if verr := UpdateIndex(context.Background(), "lab"); verr != nil {
		t.Fatal(verr)
	}

	up, verr = Upgrade(context.Background(), "hello", io.Discard)
	if verr != nil {
		t.Fatalf("upgrade: %v", verr)
	}
	if up.UpToDate || up.FromDigest != first.Digest || up.Digest == first.Digest {
		t.Fatalf("upgrade = %+v", up)
	}
	if len(up.Diff) != 0 {
		t.Fatalf("diff = %v, want none — the declaration did not change", up.Diff)
	}
	if got, _ := CurrentDigest("hello"); got != up.Digest {
		t.Fatalf("current = %s, want the new digest", got)
	}
	// Rollback stays a re-link: the old digest's directory and its trust both
	// stand — the operator approved that artifact and it has not changed.
	if _, err := os.Stat(filepath.Join(StoreDir(), "hello", first.Digest)); err != nil {
		t.Error("the previous digest's store directory is gone; rollback is a re-download again")
	}
	if !plugintrust.Load().Trusts(first.Digest) {
		t.Error("the previous artifact's trust was withdrawn by an upgrade")
	}
	if e, _ := LockedFor("hello"); e.Digest != up.Digest {
		t.Fatalf("lock digest = %s", e.Digest)
	}

	if _, verr := Upgrade(context.Background(), "ghost", io.Discard); verr == nil ||
		verr.Code != "plugin.upgrade.unknown" {
		t.Fatalf("upgrading the unmanaged: %v", verr)
	}
}

// The declaration diff names exactly the changes an authorization hangs off.
func TestTheDeclarationDiffSpeaksInSafetyTerms(t *testing.T) {
	old := plugin.Plugin{Name: "pg", Capabilities: []plugin.Capability{
		{ID: "pg.query", Safety: plugin.Read},
		{ID: "pg.status", Safety: plugin.Read},
		{ID: "pg.vacuum", Safety: plugin.Write, NeedsGrant: true},
	}}
	next := plugin.Plugin{Name: "pg", Capabilities: []plugin.Capability{
		{ID: "pg.query", Safety: plugin.Write},
		{ID: "pg.table.drop", Safety: plugin.Destructive, NeedsGrant: true},
		{ID: "pg.vacuum", Safety: plugin.Write},
	}}
	diff := declarationDiff(old, next)
	want := []string{
		"! pg.query  read → write",
		"! pg.vacuum  no longer needs a grant",
		"+ pg.table.drop  destructive, needs a grant",
		"- pg.status",
	}
	if len(diff) != len(want) {
		t.Fatalf("diff = %q, want %q", diff, want)
	}
	for i := range want {
		if diff[i] != want[i] {
			t.Fatalf("diff[%d] = %q, want %q", i, diff[i], want[i])
		}
	}
}

// Signature outcomes are recorded, never gates: a stated signature verified
// by a present cosign says so, a failing one says so loudly, an absent
// cosign says why nothing was checked — and none of them stops an install.
func TestSignatureOutcomesAreRecordedNeverRequired(t *testing.T) {
	sigBlock := func(dir string) string {
		for _, name := range []string{"artifact.sig", "key.pub"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return fmt.Sprintf("signature:\n  sig: %s\n  key: %s\n",
			fileURL(filepath.Join(dir, "artifact.sig")), fileURL(filepath.Join(dir, "key.pub")))
	}
	// A cosign that decides, without cosign being installed. The stub is a
	// shell script, which is the one thing here that is not portable: Windows
	// will not execute a file with a shebang, and a .bat is not something
	// CreateProcess starts either. What these two cases check — that rta
	// records the outcome it was handed rather than gating the install on it —
	// has nothing to do with the platform, and it runs on the other three.
	fakeCosign := func(t *testing.T, exit int) {
		t.Helper()
		if pluginhost.ExeSuffix != "" {
			t.Skip("the cosign stub is a shell script; the outcome recording it drives is platform-independent")
		}
		path := filepath.Join(t.TempDir(), "cosign")
		if err := os.WriteFile(path,
			[]byte(fmt.Sprintf("#!/bin/sh\nexit %d\n", exit)), 0o755); err != nil {
			t.Fatal(err)
		}
		saved := cosignBin
		cosignBin = path
		t.Cleanup(func() { cosignBin = saved })
	}

	cases := []struct {
		name  string
		setup func(t *testing.T) string // returns extra manifest lines
		want  string
	}{
		{"none stated", func(t *testing.T) string { return "" }, "none stated"},
		{"cosign absent", func(t *testing.T) string {
			saved := cosignBin
			cosignBin = "cosign-that-is-not-installed-anywhere"
			t.Cleanup(func() { cosignBin = saved })
			return sigBlock(t.TempDir())
		}, "not checked (cosign not installed)"},
		{"verified", func(t *testing.T) string {
			fakeCosign(t, 0)
			return sigBlock(t.TempDir())
		}, "verified"},
		{"failed", func(t *testing.T) string {
			fakeCosign(t, 1)
			return sigBlock(t.TempDir())
		}, "FAILED verification"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testData(t)
			bin := hello(t)
			attach(t, helloManifest(t, bin, "")+tc.setup(t))
			rep, verr := Install(context.Background(), "hello", io.Discard)
			if verr != nil {
				t.Fatalf("an install was gated on its signature outcome: %v", verr)
			}
			if rep.Signature != tc.want {
				t.Fatalf("signature = %q, want %q", rep.Signature, tc.want)
			}
			if e, _ := LockedFor("hello"); e.Signature != tc.want {
				t.Fatalf("lock records %q", e.Signature)
			}
		})
	}
}

// The artifact cap is a bound, not a suggestion.
func TestAnOversizedArtifactIsRefused(t *testing.T) {
	testData(t)
	saved := artifactCap
	artifactCap = 64
	t.Cleanup(func() { artifactCap = saved })

	big := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(big, make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := fmt.Sprintf(`name: hello
version: 0.1.0
summary: too big
platforms:
  - os: %s
    arch: %s
    url: %s
    sha256: %s
capabilities:
  - id: hello.greet
    safety: read
`, runtimeGOOS(), runtimeGOARCH(), fileURL(big), sha256Of(t, big))
	attach(t, doc)
	_, verr := Install(context.Background(), "hello", io.Discard)
	if verr == nil || !strings.Contains(verr.Message, "cap") {
		t.Fatalf("verr = %v", verr)
	}
}

// extractMember takes exactly the named member and nothing else it could be
// talked into: a missing member is the index's claim failing, a symlink
// member is refused, and ./-prefixed spellings still match.
func TestExtractMemberIsExact(t *testing.T) {
	src := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(src, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "a.tar.gz")
	tarGz(t, src, "./dist/tool", archive)

	read := func(member string) (string, error) {
		f, err := os.Open(archive)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		var out strings.Builder
		_, verr := extractMember(f, member, &out)
		if verr != nil {
			return "", fmt.Errorf("%s", verr.Message)
		}
		return out.String(), nil
	}
	if got, err := read("dist/tool"); err != nil || got != "bytes" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if _, err := read("dist/other"); err == nil ||
		!strings.Contains(err.Error(), "bin: claim") {
		t.Fatalf("a missing member: %v", err)
	}

	// A symlink under the wanted name is refused, not followed.
	linked := filepath.Join(t.TempDir(), "l.tar.gz")
	out, err := os.Create(linked)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "dist/tool", Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []io.Closer{tw, gz, out} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(linked)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var sink strings.Builder
	if _, verr := extractMember(f, "dist/tool", &sink); verr == nil ||
		!strings.Contains(verr.Message, "regular file") {
		t.Fatalf("a symlink member: %v", verr)
	}
}

// The needs check, both directions, on the function that decides. A
// credential location is the claim an operator most wants before installing
// and the one an index has most reason to leave out.
func TestAnIndexCannotMisstateWhatAPluginAsksToRead(t *testing.T) {
	declared := plugin.Plugin{
		Name:         "lab",
		Capabilities: []plugin.Capability{{ID: "lab.get", Safety: plugin.Read}},
		Needs:        []plugin.Need{plugin.NeedKubeconfig},
	}
	base := Manifest{
		Name:         "lab",
		Capabilities: []CapabilityClaim{{ID: "lab.get", Safety: "read"}},
	}
	honest := base
	honest.Needs = []plugin.Need{plugin.NeedKubeconfig}
	if verr := verifyClaims(Listed{Manifest: honest, Index: "ix"}, declared); verr != nil {
		t.Fatalf("an index stating the truth was refused: %v", verr)
	}

	// Silent about it: the operator reads "all read" and installs something
	// that wants their kubeconfig.
	verr := verifyClaims(Listed{Manifest: base, Index: "ix"}, declared)
	if verr == nil || !strings.Contains(verr.Message, "asks to read kubeconfig") {
		t.Fatalf("a hidden need was accepted: %v", verr)
	}

	// Inventing one: the operator weighed a question the artifact never asked.
	invented := base
	invented.Needs = []plugin.Need{plugin.NeedAWS}
	verr = verifyClaims(Listed{Manifest: invented, Index: "ix"}, plugin.Plugin{
		Name:         "lab",
		Capabilities: declared.Capabilities,
	})
	if verr == nil || !strings.Contains(verr.Message, "does not ask for aws") {
		t.Fatalf("an invented need was accepted: %v", verr)
	}
}
