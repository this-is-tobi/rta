package pkg

import (
	"archive/tar"
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
			"the installed version is what the binary says with --version, the latest is " +
			"the repository's latest release. Binaries placed by `go install` need no " +
			"entry — they carry their module path, and the Go module proxy knows the " +
			"latest; they are listed under the go manager in pkg.outdated.\n\n" +
			"Sources come from configuration only, never from a caller: a network " +
			"destination somebody else chose is not a free read.",
		Inputs: []plugin.Field{toolsField()},
		Run:    runTools,
	})
}

type toolState struct {
	tool      tool
	Installed string
	Latest    string
	Where     string
	Note      string
}

func (s toolState) behind() bool {
	return s.Installed != "" && s.Latest != "" && s.Installed != "-" && semverLess(s.Installed, s.Latest)
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
			st.Latest = strings.TrimPrefix(rel.Tag, "v")
		}
		out = append(out, st)
	}
	return out, nil
}

var versionRe = regexp.MustCompile(`v?(\d+\.\d+(?:\.\d+)?(?:[-+][0-9A-Za-z.-]+)?)`)

// installedVersion asks the binary itself. `--version` is the convention
// nearly every Go and Rust tool follows; `version` is the other one. The
// first thing that looks like a version in the output is the answer.
func installedVersion(ctx context.Context, bin string) string {
	for _, args := range [][]string{{"--version"}, {"version"}} {
		out, _, verr := run(ctx, bin, args...)
		if verr != nil {
			continue
		}
		if m := versionRe.FindStringSubmatch(out); m != nil {
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
	if len(raw) == 0 {
		return view.Text{Body: "No tools listed. Write the binaries you install from GitHub releases under " +
			"`plugins: pkg: tools:` as `- <bin>=github:<owner>/<repo>`; binaries from `go install` " +
			"need no entry and appear under the go manager in `rta pkg outdated`."}, nil
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
		}
		t.Rows = append(t.Rows, []string{s.tool.Bin, "github:" + s.tool.Owner + "/" + s.tool.Repo, s.Installed, s.Latest, status, s.Where})
	}
	t.Total = len(t.Rows)
	return t
}

// --- installing one ---------------------------------------------------------

// checksumNames are the assets a release publishes its digests in, in the
// order they are tried: goreleaser's default first, then the rest of the
// zoo, then the per-asset sidecar.
func checksumNames(asset string) []string {
	return []string{"checksums.txt", "sha256sums.txt", "SHA256SUMS", "sha256sum.txt", "checksums.sha256", asset + ".sha256"}
}

// pickAsset chooses the archive for this machine: the name must carry this
// OS and this architecture in one of their usual spellings, must be a
// .tar.gz or a bare binary, and must not be a checksums or signature file.
func pickAsset(rel release, bin string) (name, url string, size int64, digest string, verr *view.Error) {
	osTokens := map[string][]string{"darwin": {"darwin", "macos", "apple"}, "linux": {"linux"}}[runtime.GOOS]
	archTokens := map[string][]string{"amd64": {"amd64", "x86_64", "x64"}, "arm64": {"arm64", "aarch64"}}[runtime.GOARCH]
	var candidates []int
	for i, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		if !hasAny(lower, osTokens) || !hasAny(lower, archTokens) {
			continue
		}
		if strings.HasSuffix(lower, ".sha256") || strings.HasSuffix(lower, ".sig") || strings.HasSuffix(lower, ".pem") ||
			strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".sbom") || strings.HasSuffix(lower, ".json") {
			continue
		}
		if strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".deb") || strings.HasSuffix(lower, ".rpm") ||
			strings.HasSuffix(lower, ".pkg") || strings.HasSuffix(lower, ".dmg") || strings.HasSuffix(lower, ".apk") {
			continue
		}
		candidates = append(candidates, i)
	}
	if len(candidates) == 0 {
		return "", "", 0, "", view.Errorf("pkg.tool.noasset", "the latest release of %s has no .tar.gz or bare binary for %s/%s", bin, runtime.GOOS, runtime.GOARCH).
			WithHint("rta installs .tar.gz archives and bare binaries only — no zip, deb, rpm or dmg")
	}
	// A .tar.gz over a bare binary when both exist, since the archive is
	// what a checksums file usually names.
	best := candidates[0]
	for _, i := range candidates {
		if strings.HasSuffix(strings.ToLower(rel.Assets[i].Name), ".tar.gz") {
			best = i
			break
		}
	}
	a := rel.Assets[best]
	return a.Name, a.URL, a.Size, a.Digest, nil
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
		tmp, err := os.CreateTemp("", "rta-checksums-*")
		if err != nil {
			return "", view.Errorf("pkg.tool.fetch", "%v", err)
		}
		_, verr := plugindist.Fetch(ctx, u, tmp)
		tmp.Close()
		if verr != nil {
			os.Remove(tmp.Name())
			return "", view.Errorf("pkg.tool.fetch", "%s", verr.Message)
		}
		raw, err := os.ReadFile(tmp.Name())
		os.Remove(tmp.Name())
		if err != nil {
			return "", view.Errorf("pkg.tool.fetch", "%v", err)
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

// installTool is the upgrade of one direct binary: claims first, evidence
// second, nothing durable until they agree — plugin install's order.
func installTool(ctx context.Context, c *registryClient, t tool, unverified, dryRun bool) (view.View, *view.Error) {
	rel, found, verr := c.latestRelease(ctx, t.Owner, t.Repo)
	if verr != nil {
		return nil, verr
	}
	if !found {
		return nil, view.Errorf("pkg.tool.norelease", "github.com/%s/%s has no release", t.Owner, t.Repo)
	}
	assetName, assetURL, size, apiDigest, verr := pickAsset(rel, t.Bin)
	if verr != nil {
		return nil, verr
	}
	if !strings.HasPrefix(assetURL, "https://") {
		return nil, view.Errorf("pkg.tool.url", "the asset is not served over https: %s", assetURL)
	}
	want, verr := expectedDigest(ctx, c, rel, assetName, apiDigest)
	if verr != nil {
		return nil, verr
	}
	if want == "" && !unverified {
		return nil, view.Errorf("pkg.tool.unverified", "github.com/%s/%s publishes no digest for %s", t.Owner, t.Repo, assetName).
			WithHint("the release has neither an API digest nor a checksums file; `--unverified` installs it on your word alone")
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

	staging, err := os.MkdirTemp(filepath.Dir(dest), "."+t.Bin+"-*")
	if err != nil {
		return nil, view.Errorf("pkg.tool.place", "%v", err)
	}
	defer os.RemoveAll(staging)
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

	var binary io.Reader
	archive, err := os.Open(filepath.Join(staging, "artifact"))
	if err != nil {
		return nil, view.Errorf("pkg.tool.place", "%v", err)
	}
	defer archive.Close()
	if strings.HasSuffix(strings.ToLower(assetName), ".tar.gz") {
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
			extracted.Close()
			return nil, view.Errorf("pkg.tool.archive", "%s", verr.Message)
		}
		extracted.Close()
		f, err := os.Open(extracted.Name())
		if err != nil {
			return nil, view.Errorf("pkg.tool.place", "%v", err)
		}
		defer f.Close()
		binary = f
	} else {
		binary = archive
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
	defer gz.Close()
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
