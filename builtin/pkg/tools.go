package pkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The direct binaries: tools somebody installed from a GitHub release and
// put on $PATH, which no manager knows about.
//
// The list is configuration — `plugins: pkg: tools:` — because a source is a
// network destination and a caller may not choose one on the read tier. The
// entry grammar is the smallest that says everything: `bin=github:owner/repo`,
// the binary's name on $PATH and the repository that releases it. The asset
// is picked from the release by this machine's OS and architecture, the
// installed version is what `bin --version` prints, and the latest is the
// release's tag.

// toolsField is the config-backed list, Local so no remote caller can add a
// source to it.
func toolsField() plugin.Field {
	return plugin.Field{Name: "tools", Type: plugin.StringSlice, Config: "tools", Local: true,
		Help: "bin=github:owner/repo, repeatable — usually from `plugins: pkg: tools:`"}
}

const maxTools = 30

type tool struct {
	Bin   string
	Owner string
	Repo  string
}

// parseTools reads the micro-grammar and refuses the first entry it cannot.
func parseTools(raw []string) ([]tool, *view.Error) {
	if len(raw) > maxTools {
		return nil, view.Errorf("pkg.tools.toomany", "%d tools listed; the cap is %d", len(raw), maxTools).
			WithHint("each one is a call to the GitHub API, which allows sixty an hour unauthenticated")
	}
	var out []tool
	for _, entry := range raw {
		bin, src, ok := strings.Cut(strings.TrimSpace(entry), "=")
		if !ok || bin == "" {
			return nil, badTool(entry)
		}
		repo, ok := strings.CutPrefix(src, "github:")
		if !ok {
			return nil, badTool(entry)
		}
		owner, name, ok := strings.Cut(repo, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			return nil, badTool(entry)
		}
		if !validName(bin) || !validName(owner) || !validName(name) {
			return nil, badTool(entry)
		}
		out = append(out, tool{Bin: bin, Owner: owner, Repo: name})
	}
	return out, nil
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validName(s string) bool { return nameRe.MatchString(s) && !strings.HasPrefix(s, ".") }

func badTool(entry string) *view.Error {
	return view.Errorf("pkg.tools.entry", "%q is not bin=github:owner/repo", entry).
		WithHint("write the list under `plugins: pkg: tools:` as `- kubectl-neat=github:itaysk/kubectl-neat`")
}

func toolsCapability() plugin.Capability {
	return host(plugin.Capability{
		ID:         "pkg.tools",
		Summary:    "Your own binaries — from GitHub releases and go install — against their latest release",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "The binaries no package manager knows about. Each entry in `plugins: pkg: " +
			"tools:` names a binary on $PATH and the GitHub repository that releases it; " +
			"the installed version is what `<binary> --version` says, the latest is " +
			"the repository's latest release. Binaries placed by `go install` need no " +
			"entry — they carry their module path, and the Go module proxy knows the " +
			"latest; they are listed under the go manager in pkg.outdated.\n\n" +
			"Sources come from configuration only, never from a caller: a network " +
			"destination somebody else chose is not a free read.",
		Inputs: []plugin.Field{toolsField()},
		// The same key as the package table: a binary behind its release is one
		// `u` from up to date.
		Actions: []plugin.Action{{Key: "u", Label: "upgrade", Target: "pkg.upgrade", Source: plugin.ActionRow}},
		Run:     runTools,
	})
}

type toolState struct {
	tool      tool
	Installed string
	// Latest is the version in the latest release's tag, and Tag the tag
	// as the release spells it, which is what the table shows when there
	// is no version in it to show.
	Latest string
	Tag    string
	Where  string
	Note   string
}

func (s toolState) behind() bool {
	return s.compared() && semverLess(s.Installed, s.Latest)
}

// compared reports whether the installed version could actually be held
// against the latest one.
//
// **"ok" is a claim that the comparison happened.** behind() answers false
// both for a tool that is current and for one whose own --version output
// held nothing versionRe could read — a wrapper script, a binary that
// prints only a commit hash, a flag that means something else there — and
// toolsTable's default turned the second into the same word as the first.
func (s toolState) compared() bool {
	return s.Installed != "" && s.Installed != "-" && s.Latest != ""
}

func readTools(ctx context.Context, c *registryClient, raw []string) ([]toolState, *view.Error) {
	tools, verr := parseTools(raw)
	if verr != nil {
		return nil, verr
	}
	var out []toolState
	for _, t := range tools {
		st := toolState{tool: t, Installed: "-"}
		if p, err := lookPath(t.Bin); err == nil {
			st.Where = p
			st.Installed = installedVersion(ctx, t.Bin)
		} else {
			st.Note = "not on $PATH"
		}
		rel, found, verr := c.latestRelease(ctx, t.Owner, t.Repo)
		switch {
		case verr != nil:
			st.Note = verr.Message
		case !found:
			st.Note = "no release on GitHub"
		default:
			// The version in the tag, read the way the installed one is.
			// Tags carry more than a leading v: bun-v1.1.38, jq-1.7.1,
			// rust-v0.47.0. With only the v trimmed, the product's name
			// read as a zero, 0.0.1.38 is never newer than 1.1.20, and a
			// tool with a release it did not have read ok. A tag with no
			// version in it leaves Latest empty, which is "could not
			// compare", never "current".
			st.Tag = rel.Tag
			if m := versionRe.FindStringSubmatch(rel.Tag); m != nil {
				st.Latest = m[1]
			}
		}
		out = append(out, st)
	}
	return out, nil
}

var versionRe = regexp.MustCompile(`v?(\d+\.\d+(?:\.\d+)?(?:[-+][0-9A-Za-z.-]+)?)`)

// installedVersion asks the binary itself. `--version` is the convention
// nearly every Go and Rust tool follows; `version` is the other one. The
// first thing that looks like a version in the output is the answer,
// whatever the status: `kubectl version` and `docker version` print the
// client's and then exit 1 over a server they cannot reach, and output
// with no version in it reads as unknown, never as current.
func installedVersion(ctx context.Context, bin string) string {
	for _, args := range [][]string{{"--version"}, {"version"}} {
		st, verr := runStatus(ctx, bin, args...)
		if verr != nil {
			continue
		}
		if m := versionRe.FindStringSubmatch(st.out); m != nil {
			return m[1]
		}
	}
	return "-"
}

func runTools(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := supported(); verr != nil {
		return nil, verr
	}
	raw := req.StringSlice("tools")
	// The table even when no tool is listed, and the sentence beside it for
	// a screen: see view.Table.Empty.
	if len(raw) == 0 {
		t := toolsTable(nil)
		t.Empty = "No tools listed. Write the binaries you install from GitHub releases under " +
			"`plugins: pkg: tools:` as `- <bin>=github:<owner>/<repo>`; binaries from `go install` " +
			"need no entry and appear under the go manager in " + req.Surface().CapabilityName("pkg.outdated") + "."
		return t, nil
	}
	states, verr := readTools(ctx, newRegistryClient(), raw)
	if verr != nil {
		return nil, verr
	}
	return toolsTable(states), nil
}

func toolsTable(states []toolState) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "target"}, {Name: "Source"}, {Name: "Installed"}, {Name: "Latest"},
		{Name: "Status", Kind: view.KindStatus}, {Name: "Where"},
	}}
	for _, s := range states {
		status := "ok"
		switch {
		case s.Note != "":
			status = "info " + s.Note
		case s.behind():
			status = "outdated"
		case !s.compared() && s.Latest == "":
			status = "unknown — its release tag holds no version"
		case !s.compared():
			status = "unknown — its version could not be read"
		}
		latest := s.Latest
		if latest == "" {
			latest = s.Tag
		}
		t.Rows = append(t.Rows, []string{s.tool.Bin, "github:" + s.tool.Owner + "/" + s.tool.Repo, s.Installed, latest, status, s.Where})
	}
	t.Total = len(t.Rows)
	return t
}

// --- installing one ---------------------------------------------------------

// checksumNames are the assets a release publishes its digests in, in the
// order they are tried: goreleaser's default first, then the rest of the
// zoo, then the per-asset sidecar.
func checksumNames(name string) []string {
	return []string{"checksums.txt", "sha256sums.txt", "SHA256SUMS", "sha256sum.txt", "checksums.sha256", name + ".sha256"}
}

// assetKind is what an asset's name says rta would be placing, which is
// all it knows before the download.
type assetKind int

const (
	assetOther assetKind = iota
	assetBinary
	assetTarGz
)

// kindOf reads an asset's name by what rta can place, never by what it can
// rule out.
//
// It was a deny list — checksums, signatures, zip, deb, rpm, dmg — and the
// install path took whatever survived it that was not a .tar.gz for the
// binary itself. Release pages carry more kinds of file than any list
// names: shellcheck, helix and typst ship only .tar.xz, rust-analyzer a .gz
// and a .vsix, and a binary's .asc or .intoto.jsonl sidecar listed before
// it was the one picked. Each went onto $PATH in place of the working tool.
//
// A bare binary is the name with no extension, and telling that apart from
// one with an extension is the part a naive reading gets wrong: versions
// and platforms are spelled with dots too, so sops-v3.9.0.darwin.arm64 and
// mkcert-v1.4.4-darwin-arm64 have a last "extension" of arm64 and of
// 4-darwin-arm64. What follows the last dot is part of the name when it
// names the platform or is a number, and a file type otherwise — except
// .AppImage, the one extension an executable carries as itself.
func kindOf(name string, platform []string) assetKind {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		return assetTarGz
	}
	i := strings.LastIndexByte(lower, '.')
	if i < 0 {
		return assetBinary
	}
	last := lower[i+1:]
	if last == "appimage" || (last != "" && (hasAny(last, platform) || strings.Trim(last, "0123456789") == "")) {
		return assetBinary
	}
	return assetOther
}

// platformTokens are this machine's OS and architecture in the spellings
// release assets use for them.
func platformTokens() (osTokens, archTokens []string) {
	return map[string][]string{"darwin": {"darwin", "macos", "apple"}, "linux": {"linux"}}[runtime.GOOS],
		map[string][]string{"amd64": {"amd64", "x86_64", "x64"}, "arm64": {"arm64", "aarch64"}}[runtime.GOARCH]
}

// pickAsset chooses what to install for this machine: the name must carry
// this OS and this architecture in one of their usual spellings, and be a
// .tar.gz or a bare binary. A .tar.gz wins over a bare binary when both
// exist, since the archive is what a checksums file usually names; between
// two of a kind, the release's own order decides.
func pickAsset(rel release, bin string) (asset, *view.Error) {
	osTokens, archTokens := platformTokens()
	platform := append(append([]string{}, osTokens...), archTokens...)
	best, bestKind := -1, assetOther
	for i, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		if !hasAny(lower, osTokens) || !hasAny(lower, archTokens) {
			continue
		}
		if k := kindOf(a.Name, platform); k > bestKind {
			best, bestKind = i, k
		}
	}
	if best < 0 {
		return asset{}, view.Errorf("pkg.tool.noasset", "the latest release of %s has no .tar.gz or bare binary for %s/%s", bin, runtime.GOOS, runtime.GOARCH).
			WithHint("rta installs .tar.gz archives and bare binaries only — no .tar.xz, .gz, zip, deb, rpm or dmg")
	}
	return rel.Assets[best], nil
}

// runnable reports whether a file that starts with head is a program goos
// runs: its own executable format, or a script with a #! line.
//
// The check the name cannot make. A digest proves the bytes are the ones
// the release published, not that they are a binary — an asset named like
// one that held an archive or a signature hashed right, read "verified",
// and replaced a working tool with something that could not execute. The
// same holds for the member of a .tar.gz that merely shares the binary's
// name. Only the formats of the two platforms pickAsset knows: an ELF on
// macOS or a Mach-O on Linux is as unrunnable as an archive.
func runnable(goos string, head []byte) bool {
	if bytes.HasPrefix(head, []byte("#!")) {
		return true
	}
	var magic [][]byte
	switch goos {
	case "linux":
		magic = [][]byte{{0x7f, 'E', 'L', 'F'}}
	case "darwin":
		// Thin Mach-O, 32- and 64-bit, in either byte order, and the fat
		// (universal) header, 32- and 64-bit.
		magic = [][]byte{
			{0xfe, 0xed, 0xfa, 0xce}, {0xfe, 0xed, 0xfa, 0xcf},
			{0xce, 0xfa, 0xed, 0xfe}, {0xcf, 0xfa, 0xed, 0xfe},
			{0xca, 0xfe, 0xba, 0xbe}, {0xca, 0xfe, 0xba, 0xbf},
		}
	}
	for _, m := range magic {
		if bytes.HasPrefix(head, m) {
			return true
		}
	}
	return false
}

// checkRunnable refuses a file that is not a program for this machine, and
// leaves it read from the start for whoever places it.
func checkRunnable(f *os.File, what, dest string) *view.Error {
	head := make([]byte, 4)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return view.Errorf("pkg.tool.place", "%v", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return view.Errorf("pkg.tool.place", "%v", err)
	}
	if !runnable(runtime.GOOS, head[:n]) {
		return view.Errorf("pkg.tool.archive", "%s is not a %s executable or a script, so it does not go to %s", what, runtime.GOOS, dest).
			WithHint("the release's file for this machine is some other kind — an archive rta does not unpack, or a signature; install that one by hand")
	}
	return nil
}

func hasAny(s string, tokens []string) bool {
	for _, t := range tokens {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

// expectedDigest is the digest the release publishes for the asset: the
// API's own digest field when GitHub has computed one, else the first
// checksums asset that names it. Empty with no error means the release
// publishes nothing, which is the caller's decision to refuse or override.
func expectedDigest(ctx context.Context, c *registryClient, rel release, assetName, apiDigest string) (string, *view.Error) {
	if strings.HasPrefix(apiDigest, "sha256:") {
		return strings.TrimPrefix(apiDigest, "sha256:"), nil
	}
	byName := map[string]string{}
	for _, a := range rel.Assets {
		byName[a.Name] = a.URL
	}
	for _, name := range checksumNames(assetName) {
		u, ok := byName[name]
		if !ok {
			continue
		}
		raw, verr := fetchChecksums(ctx, u)
		if verr != nil {
			return "", verr
		}
		sums, verr := plugindist.ParseChecksums(raw)
		if verr != nil {
			return "", view.Errorf("pkg.tool.checksums", "%s: %s", name, verr.Message)
		}
		if d, ok := sums[assetName]; ok {
			return d, nil
		}
		// A per-asset sidecar holds one line and may not name the file.
		if strings.HasSuffix(name, ".sha256") && len(sums) == 1 {
			for _, d := range sums {
				return d, nil
			}
		}
	}
	return "", nil
}

// fetchChecksums downloads a release's checksums file through a temporary
// file, and removes that file whichever way the download ends — a forced exit
// taken during it included. The download is not held off such an exit, and
// the removal it skips was the only one: the partial file stayed in the
// temporary directory, where nothing of rta's looks again. Made, and its
// removal registered, under a brief hold of their own, as the tool's staging
// directory is (installTool).
func fetchChecksums(ctx context.Context, u string) ([]byte, *view.Error) {
	release := shutdown.Hold()
	tmp, err := os.CreateTemp("", "rta-checksums-*")
	if err != nil {
		release()
		return nil, view.Errorf("pkg.tool.fetch", "%v", err)
	}
	defer shutdown.OnExit(func() { _ = os.Remove(tmp.Name()) })()
	defer os.Remove(tmp.Name())
	release()
	_, verr := plugindist.Fetch(ctx, u, tmp)
	closeErr := tmp.Close()
	if verr != nil {
		return nil, view.Errorf("pkg.tool.fetch", "%s", verr.Message)
	}
	if closeErr != nil {
		return nil, view.Errorf("pkg.tool.fetch", "%v", closeErr)
	}
	raw, err := os.ReadFile(tmp.Name())
	if err != nil {
		return nil, view.Errorf("pkg.tool.fetch", "%v", err)
	}
	return raw, nil
}

// installTool is the upgrade of one direct binary: claims first, evidence
// second, nothing durable until they agree — plugin install's order.
func installTool(ctx context.Context, sf plugin.Surface, c *registryClient, t tool, unverified, dryRun bool) (view.View, *view.Error) {
	rel, found, verr := c.latestRelease(ctx, t.Owner, t.Repo)
	if verr != nil {
		return nil, verr
	}
	if !found {
		return nil, view.Errorf("pkg.tool.norelease", "github.com/%s/%s has no release", t.Owner, t.Repo)
	}
	picked, verr := pickAsset(rel, t.Bin)
	if verr != nil {
		return nil, verr
	}
	assetName, assetURL, size, apiDigest := picked.Name, picked.URL, picked.Size, picked.Digest
	if !strings.HasPrefix(assetURL, "https://") {
		return nil, view.Errorf("pkg.tool.url", "the asset is not served over https: %s", assetURL)
	}
	want, verr := expectedDigest(ctx, c, rel, assetName, apiDigest)
	if verr != nil {
		return nil, verr
	}
	if want == "" && !unverified {
		return nil, view.Errorf("pkg.tool.unverified", "github.com/%s/%s publishes no digest for %s", t.Owner, t.Repo, assetName).
			WithHint("the release has neither an API digest nor a checksums file; " + sf.InputName("unverified") +
				" installs it on your word alone")
	}
	dest, verr := toolDestination(t.Bin)
	if verr != nil {
		return nil, verr
	}
	if dryRun {
		how := "verified against " + want[:min(12, len(want))]
		if want == "" {
			how = "UNVERIFIED — the release publishes no digest"
		}
		return view.Text{Body: fmt.Sprintf("would install %s %s (%s, %d bytes, %s) into %s", t.Bin, rel.Tag, assetName, size, how, dest)}, nil
	}

	// The download into staging is not held off a forced exit (the hold is
	// atomicfile's, on the write that places the tool), and os.Exit skips a
	// deferred removal, so an exit taken during it left the partial download
	// beside the tool for good: a dot-directory in $PATH's own directory,
	// which nothing lists and nothing ever removed. The exit removes it
	// instead, as it does plugin install's. Made, and its removal registered,
	// under a brief hold of their own so that no exit falls between the two,
	// and unregistered only after the upgrade's own removal has run.
	release := shutdown.Hold()
	staging, err := os.MkdirTemp(filepath.Dir(dest), "."+t.Bin+"-*")
	if err != nil {
		release()
		return nil, view.Errorf("pkg.tool.place", "%v", err)
	}
	defer shutdown.OnExit(func() { _ = os.RemoveAll(staging) })()
	defer os.RemoveAll(staging)
	release()
	artifact, err := os.Create(filepath.Join(staging, "artifact"))
	if err != nil {
		return nil, view.Errorf("pkg.tool.place", "%v", err)
	}
	got, verr := plugindist.Fetch(ctx, assetURL, artifact)
	if cerr := artifact.Close(); verr == nil && cerr != nil {
		verr = view.Errorf("pkg.tool.fetch", "%v", cerr)
	}
	if verr != nil {
		return nil, view.Errorf("pkg.tool.fetch", "%s", verr.Message)
	}
	if want != "" && got != want {
		return nil, view.Errorf("pkg.tool.checksum", "%s hashed to %s and the release says %s — refusing the bytes", assetName, got[:12], want[:12]).
			WithHint("the release was replaced, or something between GitHub and you rewrote the download")
	}

	var binary *os.File
	what := assetName
	archive, err := os.Open(filepath.Join(staging, "artifact"))
	if err != nil {
		return nil, view.Errorf("pkg.tool.place", "%v", err)
	}
	defer func() { _ = archive.Close() }()
	if kindOf(assetName, nil) == assetTarGz {
		member, verr := memberNamed(archive, t.Bin)
		if verr != nil {
			return nil, verr
		}
		if _, err := archive.Seek(0, io.SeekStart); err != nil {
			return nil, view.Errorf("pkg.tool.place", "%v", err)
		}
		extracted, err := os.Create(filepath.Join(staging, "extracted"))
		if err != nil {
			return nil, view.Errorf("pkg.tool.place", "%v", err)
		}
		if _, verr := plugindist.ExtractMember(archive, member, extracted); verr != nil {
			_ = extracted.Close()
			return nil, view.Errorf("pkg.tool.archive", "%s", verr.Message)
		}
		// A written file: its close is where a full disk reports itself.
		if err := extracted.Close(); err != nil {
			return nil, view.Errorf("pkg.tool.place", "%v", err)
		}
		f, err := os.Open(extracted.Name())
		if err != nil {
			return nil, view.Errorf("pkg.tool.place", "%v", err)
		}
		defer func() { _ = f.Close() }()
		binary, what = f, member+" in "+assetName
	} else {
		binary = archive
	}
	if verr := checkRunnable(binary, what, dest); verr != nil {
		return nil, verr
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, view.Errorf("pkg.tool.place", "%v", err)
	}
	if err := atomicfile.WriteFrom(dest, binary, 0o755); err != nil {
		return nil, view.Errorf("pkg.tool.place", "%v", err)
	}
	placed, verr := plugindist.DigestFile(dest)
	if verr != nil {
		return nil, view.Errorf("pkg.tool.place", "%s", verr.Message)
	}
	verified := "verified"
	if want == "" {
		verified = "UNVERIFIED"
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "installed", Value: t.Bin + " " + rel.Tag},
		{Key: "from", Value: assetURL},
		{Key: "into", Value: dest},
		{Key: "digest", Value: placed + " (" + verified + ")"},
	}}, nil
}

// toolDestination is where the binary already lives, so the upgrade replaces
// it in place, or ~/.local/bin for a first install.
func toolDestination(bin string) (string, *view.Error) {
	if p, err := lookPath(bin); err == nil {
		abs, err := filepath.Abs(p)
		if err == nil {
			return abs, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", view.Errorf("pkg.tool.place", "no home directory: %v", err)
	}
	return filepath.Join(home, ".local", "bin", bin), nil
}

// memberNamed finds the regular file in a .tar.gz whose base name is bin,
// at any depth: release archives put the binary at the root or under one
// directory, and ExtractMember wants the exact path.
func memberNamed(archive io.Reader, bin string) (string, *view.Error) {
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return "", view.Errorf("pkg.tool.archive", "not a gzip archive: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", view.Errorf("pkg.tool.archive", "the archive holds no file named %s", bin).
				WithHint("the binary's name in the archive differs from its name on $PATH; this v1 matches by name only")
		}
		if err != nil {
			return "", view.Errorf("pkg.tool.archive", "reading the archive: %v", err)
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == bin {
			return path.Clean(strings.TrimPrefix(hdr.Name, "./")), nil
		}
	}
}
