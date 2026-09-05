package plugindist

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/pluginhost"
)

// The three things that made a plugin unloadable on Windows, held down from a
// machine that is not one. Two of them are pure data — a manifest's claim
// about a platform, and a name — so they test honestly here. The third needs
// a symlink to fail, which is what the `symlink` var is for.
//
// None of this replaces running rta on Windows, and the CI leg that does is
// the point of the exercise; these are what fail first and locally when
// somebody reintroduces one of the three.

// A manifest describes six platforms from whichever one generated it, so the
// member to extract out of a Windows archive is decided by the entry's own os
// and never by the host's. GoReleaser puts rta-plugin-<name>.exe in there;
// claiming rta-plugin-<name> fails at a stranger's install with a message
// about the index, and only on Windows, so nobody developing on Linux or
// macOS would ever have seen it.
func TestAWindowsEntryNamesTheExeInsideItsArchive(t *testing.T) {
	testData(t)
	bin := hello(t)
	_, m := generate(t, GenerateRequest{
		Binary: bin,
		Platforms: []PlatformSource{
			{OS: "windows", Arch: "amd64", URL: "https://example.com/hello_windows_amd64.tar.gz"},
			{OS: "linux", Arch: "amd64", URL: "https://example.com/hello_linux_amd64.tar.gz"},
		},
		Checksums: map[string]string{
			"hello_windows_amd64.tar.gz": strings.Repeat("a", 64),
			"hello_linux_amd64.tar.gz":   strings.Repeat("b", 64),
		},
	})
	for _, p := range m.Platforms {
		want := "rta-plugin-hello"
		if p.OS == "windows" {
			want += ".exe"
		}
		if p.Bin != want {
			t.Fatalf("%s/%s bin = %q, want %q", p.OS, p.Arch, p.Bin, want)
		}
	}
}

// The name discovery looks for carries the platform's executable suffix, and
// the namespace it reports must not. Trimming only the prefix on Windows
// yields "pg.exe", which disagrees with everything the binary declares about
// itself — so the plugin is refused, for a reason that was really this.
func TestANamespaceIsTheFilenameWithoutWhatThePlatformAdded(t *testing.T) {
	name := pluginhost.BinaryName("pg")
	if runtime.GOOS == "windows" && !strings.HasSuffix(name, ".exe") {
		t.Fatalf("BinaryName = %q, want the .exe this platform needs", name)
	}
	got, ok := pluginhost.Namespace(name)
	if !ok || got != "pg" {
		t.Fatalf("Namespace(%q) = %q %v, want pg", name, got, ok)
	}
	if _, ok := pluginhost.Namespace("rta-something-else"); ok {
		t.Fatal("a name without the plugin prefix announced a namespace")
	}
}

// Windows refuses an unprivileged symlink unless Developer Mode is on, and
// this is the last step of an install — after the fetch, the hash, the launch
// and the approval. It must not be where the install dies.
//
// The copy has to keep what the link stated: bin/ holds the current version,
// and CurrentDigest still answers which one from the layout rather than from
// the lockfile. It answers by hashing, which is the stronger claim.
func TestAnInstallSurvivesAPlatformThatWillNotSymlink(t *testing.T) {
	testData(t)
	original := symlink
	symlink = func(string, string) error { return errors.New("a privilege is not held by the client") }
	t.Cleanup(func() { symlink = original })

	staged := filepath.Join(t.TempDir(), "staged")
	body := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(staged, body, 0o755); err != nil {
		t.Fatal(err)
	}
	digest, verr := digestFile(staged)
	if verr != nil {
		t.Fatal(verr)
	}
	if _, verr := place("hello", digest, staged); verr != nil {
		t.Fatalf("place: %v", verr)
	}

	current := filepath.Join(BinDir(), binaryName("hello"))
	info, err := os.Lstat(current)
	if err != nil {
		t.Fatalf("nothing landed in bin/: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("bin/ holds a symlink, so the fallback never ran and this proves nothing")
	}
	if got, err := os.ReadFile(current); err != nil || string(got) != string(body) {
		t.Fatalf("bin/ = %q (%v), want the artifact's own bytes", got, err)
	}
	got, ok := CurrentDigest("hello")
	if !ok || got != digest {
		t.Fatalf("CurrentDigest = %q %v, want %s read back off a copy", got, ok, digest)
	}
}

// A digest that is not in the store is not "current" — otherwise anything
// dropped into bin/ by hand claims to be the installed version, which is the
// one question CurrentDigest exists to answer independently of the lockfile.
func TestAStrangerInBinIsNotTheCurrentVersion(t *testing.T) {
	testData(t)
	if err := os.MkdirAll(BinDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(BinDir(), binaryName("hello")),
		[]byte("not from the store"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := CurrentDigest("hello"); ok {
		t.Fatalf("CurrentDigest = %q, want no answer for a file the store never placed", got)
	}
}

// **A file:// artifact could not be installed on Windows at all**, which is
// the scheme a local index naming a local artifact depends on.
//
// A URL path is slash-separated and always begins with "/", so on Unix it is
// already the filesystem path and os.Open took it unchanged. The absolute
// Windows form file:///C:/dir/x has the path /C:/dir/x — a slash in front of
// a drive letter — and Windows refuses it: "The filename, directory name, or
// volume label syntax is incorrect."
//
// Both spellings are checked from whatever machine runs this, because the
// conversion is about the URL rather than about the host: a drive-letter path
// is wrong to hand to os.Open everywhere, and being able to say so here is
// what stopped this needing a Windows runner to notice a second time.
func TestAFileURLBecomesThePathThisSystemOpens(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"file:///C:/dir/x", `C:/dir/x`},
		{"file:///c:/dir/x", `c:/dir/x`},
		{"file:///srv/index/x", "/srv/index/x"},
		{"file:///x", "/x"},
	} {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parsing %q: %v", tc.raw, err)
		}
		if got := localPath(u); got != filepath.FromSlash(tc.want) {
			t.Errorf("localPath(%q) = %q, want %q", tc.raw, got, filepath.FromSlash(tc.want))
		}
	}
}

// **An install that works most of the time is the worst kind.**
//
// place moves the artifact into the store moments after describeBinary ran it,
// and Windows refuses to move a file whose image a handle still holds — a
// handle that outlives the process. The install failed with "The process
// cannot access the file because it is being used by another process", on
// roughly one run in two, from a tree byte-identical to one that had just
// passed.
//
// The retry is what is checked here rather than the platform, because the
// platform is what nobody has in reach: a move that fails and then succeeds
// must be reported as a success, and one that never succeeds must still be
// reported with the error the last attempt gave rather than a summary of the
// attempts.
func TestAMoveIsRetriedWhileTheFileIsStillHeld(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "staged")
	to := filepath.Join(dir, "placed")
	if err := os.WriteFile(from, []byte("artifact"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := moveExecutable(from, to); err != nil {
		t.Fatalf("a move nothing was holding failed: %v", err)
	}
	if _, err := os.Stat(to); err != nil {
		t.Fatalf("the artifact is not at its destination: %v", err)
	}

	// And a move that cannot ever work still reports what actually went
	// wrong, rather than swallowing it after the last attempt.
	err := moveExecutable(filepath.Join(dir, "was-never-there"), to)
	if err == nil {
		t.Fatal("moving a file that does not exist reported success")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want the real reason the move failed", err)
	}
}
