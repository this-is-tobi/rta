package pkg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// here is this machine's OS and architecture in the spelling most release
// assets use, so a test's asset names are ones pickAsset would consider.
func here(t *testing.T) (goos, arch string) {
	t.Helper()
	goos = map[string]string{"darwin": "darwin", "linux": "linux"}[runtime.GOOS]
	arch = map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[runtime.GOARCH]
	if goos == "" || arch == "" {
		t.Skipf("no release asset is picked for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return goos, arch
}

// releaseNamed is a release whose assets carry these names, each served at
// base/dl/<name>.
func releaseNamed(t *testing.T, base string, names ...string) release {
	t.Helper()
	type doc struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	}
	assets := make([]doc, len(names))
	for i, n := range names {
		assets[i] = doc{Name: n, URL: base + "/dl/" + n}
	}
	raw, err := json.Marshal(map[string]any{"tag_name": "v1.0.0", "assets": assets})
	if err != nil {
		t.Fatal(err)
	}
	var rel release
	if err := json.Unmarshal(raw, &rel); err != nil {
		t.Fatal(err)
	}
	return rel
}

// An asset is installed only as what rta knows how to place: a .tar.gz it
// extracts the binary from, or the bare binary itself. Everything else
// that carries this machine's OS and architecture in its name — an archive
// in another format, a signature or attestation sidecar — was a candidate
// once, and the install path treated every candidate that was not a .tar.gz
// as the binary: shellcheck's .tar.xz, rust-analyzer's .gz, or a .asc listed
// before the binary it signs went onto $PATH in place of the working tool.
func TestPickAssetTakesATarGzOrABareBinaryAndNothingElse(t *testing.T) {
	goos, arch := here(t)
	for _, c := range []struct {
		names []string
		want  string
	}{
		{[]string{"shellcheck-v0.10.0." + goos + "." + arch + ".tar.xz", "shellcheck-v0.10.0.zip"}, ""},
		{[]string{"rust-analyzer-" + arch + "-" + goos + ".gz", "rust-analyzer-" + goos + "-" + arch + ".vsix"}, ""},
		{[]string{"tool-" + goos + "-" + arch + ".asc", "tool-" + goos + "-" + arch}, "tool-" + goos + "-" + arch},
		{[]string{"tool-" + goos + "-" + arch, "tool-" + goos + "-" + arch + ".asc"}, "tool-" + goos + "-" + arch},
		{[]string{"tool-" + goos + "-" + arch + ".intoto.jsonl", "tool-" + goos + "-" + arch}, "tool-" + goos + "-" + arch},
		{[]string{"tool-" + goos + "-" + arch, "tool_1.0_" + goos + "_" + arch + ".tar.gz"}, "tool_1.0_" + goos + "_" + arch + ".tar.gz"},
		{[]string{"tool-" + goos + "-" + arch + ".tgz"}, "tool-" + goos + "-" + arch + ".tgz"},
		// A version or the platform after the last dot is part of a bare
		// binary's name, not an extension.
		{[]string{"sops-v3.9.0." + goos + "." + arch}, "sops-v3.9.0." + goos + "." + arch},
		{[]string{"mkcert-v1.4.4-" + goos + "-" + arch}, "mkcert-v1.4.4-" + goos + "-" + arch},
		{[]string{"nvim-" + goos + "-" + arch + ".appimage.zsync", "nvim-" + goos + "-" + arch + ".appimage"}, "nvim-" + goos + "-" + arch + ".appimage"},
	} {
		a, verr := pickAsset(releaseNamed(t, "https://example.invalid", c.names...), "tool")
		switch {
		case c.want == "" && (verr == nil || verr.Code != "pkg.tool.noasset"):
			t.Errorf("pickAsset(%v) = %q, %v; want pkg.tool.noasset", c.names, a.Name, verr)
		case c.want != "" && (verr != nil || a.Name != c.want):
			t.Errorf("pickAsset(%v) = %q, %v; want %q", c.names, a.Name, verr, c.want)
		}
	}
}

// servedRelease serves owner/repo's latest release over TLS with one asset,
// name, holding body and published with body's digest, and points both the
// registry client and the download at the test server.
func servedRelease(t *testing.T, owner, repo, name string, body []byte) *registryClient {
	t.Helper()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + owner + "/" + repo + "/releases/latest":
			_, _ = w.Write([]byte(`{"tag_name":"v1.0.0","assets":[{"name":"` + name + `","browser_download_url":"` +
				srv.URL + `/dl/` + name + `","size":` + itoa(len(body)) + `,"digest":"sha256:` + digest + `"}]}`))
		case "/dl/" + name:
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	// plugindist.Fetch downloads with the default client, which trusts no
	// test certificate until its transport is the server's own.
	old := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = old })
	c := newRegistryClient()
	c.http = srv.Client()
	c.github = srv.URL
	return c
}

// placedAt makes dest the working tool's place on $PATH, holding what it
// held before the upgrade.
func placedAt(t *testing.T, bin string) (dest, before string) {
	t.Helper()
	dest = filepath.Join(t.TempDir(), "bin", bin)
	before = "#!/bin/sh\necho the working one\n"
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(before), 0o755); err != nil {
		t.Fatal(err)
	}
	install(t, &fake{bins: map[string]bool{}})
	lookPath = func(name string) (string, error) {
		if name == bin {
			return dest, nil
		}
		return "", exec.ErrNotFound
	}
	return dest, before
}

// **What lands on $PATH is a program this machine runs, whatever the
// release calls it.** The digest proves the bytes are the ones the release
// published, not that they are the binary: an asset named like one that
// holds an archive or a signature hashed correctly, read "verified", and
// replaced the working tool with something that cannot execute.
func TestInstallToolPlacesOnlyAProgramThisMachineRuns(t *testing.T) {
	goos, arch := here(t)
	name := "tool-" + goos + "-" + arch

	// shellcheck's shape: the release's only file for this machine is a
	// .tar.xz, which hashed to its published digest and went onto $PATH as
	// the binary. Refused by its name, before anything is downloaded.
	c := servedRelease(t, "o", "tool", name+".tar.xz", []byte("\xfd7zXZ\x00\x00 an xz archive, not an executable"))
	dest, before := placedAt(t, "tool")
	_, verr := installTool(context.Background(), plugin.SurfaceCLI, c, tool{Bin: "tool", Owner: "o", Repo: "tool"}, false, false)
	if verr == nil || verr.Code != "pkg.tool.noasset" {
		t.Errorf("a release with only a .tar.xz = %v, want pkg.tool.noasset", verr)
	}
	if got, _ := os.ReadFile(dest); string(got) != before {
		t.Errorf("the working tool was replaced with %q", got)
	}

	// The same bytes under a bare binary's name are refused by what they
	// are, once downloaded and before they are placed.
	c = servedRelease(t, "o", "tool", name, []byte("\xfd7zXZ\x00\x00 an xz archive, not an executable"))
	dest, before = placedAt(t, "tool")
	_, verr = installTool(context.Background(), plugin.SurfaceCLI, c, tool{Bin: "tool", Owner: "o", Repo: "tool"}, false, false)
	if verr == nil || verr.Code != "pkg.tool.archive" {
		t.Errorf("an asset that is not a program = %v, want pkg.tool.archive", verr)
	}
	if got, _ := os.ReadFile(dest); string(got) != before {
		t.Errorf("the working tool was replaced with %q", got)
	}

	archive, _ := tarGz(t, "tool-1.0.0/tool", "a man page, not the tool\n")
	c = servedRelease(t, "o", "tool", name+".tar.gz", archive)
	dest, before = placedAt(t, "tool")
	_, verr = installTool(context.Background(), plugin.SurfaceCLI, c, tool{Bin: "tool", Owner: "o", Repo: "tool"}, false, false)
	if verr == nil || verr.Code != "pkg.tool.archive" {
		t.Errorf("an archive member that is not a program = %v, want pkg.tool.archive", verr)
	}
	if got, _ := os.ReadFile(dest); string(got) != before {
		t.Errorf("the working tool was replaced with %q", got)
	}

	c = servedRelease(t, "o", "tool", name, []byte("#!/bin/sh\necho new\n"))
	dest, _ = placedAt(t, "tool")
	v, verr := installTool(context.Background(), plugin.SurfaceCLI, c, tool{Bin: "tool", Owner: "o", Repo: "tool"}, false, false)
	if verr != nil {
		t.Fatalf("a script published as the binary: %v", verr)
	}
	if got, _ := os.ReadFile(dest); string(got) != "#!/bin/sh\necho new\n" {
		t.Errorf("dest holds %q after %v", got, v)
	}
	if info, err := os.Stat(dest); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("the placed binary is not executable: %v %v", info, err)
	}
}

func TestRunnableKnowsEachPlatformsExecutables(t *testing.T) {
	for _, c := range []struct {
		goos string
		head string
		want bool
	}{
		{"linux", "\x7fELF\x02", true},
		{"linux", "\xcf\xfa\xed\xfe", false},
		{"darwin", "\xcf\xfa\xed\xfe", true},
		{"darwin", "\xfe\xed\xfa\xcf", true},
		{"darwin", "\xca\xfe\xba\xbe", true},
		{"darwin", "\x7fELF\x02", false},
		{"darwin", "#!/usr/bin/env python3\n", true},
		{"linux", "#!/bin/sh\n", true},
		{"linux", "\x1f\x8b\x08", false},
		{"linux", "\xfd7zXZ\x00", false},
		{"linux", "-----BEGIN PGP SIGNATURE-----", false},
		{"linux", "", false},
	} {
		if got := runnable(c.goos, []byte(c.head)); got != c.want {
			t.Errorf("runnable(%s, %q) = %v, want %v", c.goos, c.head, got, c.want)
		}
	}
}

// A release's checksums file costs the memory one holds, and no more. It was
// read whole before its size was looked at, bounded only by the 256 MiB any
// download is, so a release publishing a checksums asset that size had every
// upgrade of its tool take that much memory to be told the file was too big:
// a checksums file names one digest per asset, a few kilobytes for any real
// release.
func TestAnOversizedChecksumsFileIsRefusedWithoutBeingRead(t *testing.T) {
	goos, arch := here(t)
	name := "tool-" + goos + "-" + arch
	huge := bytes.Repeat([]byte("x"), 32<<20)
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/tool/releases/latest":
			_, _ = w.Write([]byte(`{"tag_name":"v1.0.0","assets":[` +
				`{"name":"` + name + `","browser_download_url":"` + srv.URL + `/dl/` + name + `","size":64},` +
				`{"name":"checksums.txt","browser_download_url":"` + srv.URL + `/dl/checksums.txt","size":` +
				itoa(len(huge)) + `}]}`))
		case "/dl/checksums.txt":
			_, _ = w.Write(huge)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = old })
	c := newRegistryClient()
	c.http = srv.Client()
	c.github = srv.URL
	placedAt(t, "tool")

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, verr := installTool(context.Background(), plugin.SurfaceCLI, c, tool{Bin: "tool", Owner: "o", Repo: "tool"}, false, false)
	runtime.ReadMemStats(&after)
	if verr == nil || verr.Code != "pkg.tool.checksums" {
		t.Fatalf("a checksums file of %d bytes = %v, want pkg.tool.checksums", len(huge), verr)
	}
	if taken := after.TotalAlloc - before.TotalAlloc; taken > 8<<20 {
		t.Errorf("refusing a %d-byte checksums file took %d bytes", len(huge), taken)
	}
}
