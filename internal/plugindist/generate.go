package plugindist

import (
	"context"
	"io"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Writing a manifest by hand is transcribing a declaration, and the
// transcription is graded — at somebody else's install, by verifyClaims,
// which refuses the artifact and names the index that got it wrong. Every
// field an author would copy across is already in the binary and already
// readable by rta the same sandboxed way install reads it, so copying is the
// only step that can introduce an error, and it is the one step nothing
// needed.
//
// So the manifest is derived. Name, version, summary, every capability's ID,
// safety class and grant flag, and every declared credential need come out of
// the artifact. What an author supplies is the part the artifact cannot know:
// where its bytes will be published. Nothing else.
//
// The output is parsed back through ParseManifest before it is returned, so
// this cannot emit a file an index would refuse — the generator is held to
// the grammar it generates for.

// PlatformSource is one os/arch and where that platform's artifact will live.
type PlatformSource struct {
	OS   string
	Arch string
	// URL is where the artifact will be fetched from: https for a published
	// one, file for a local rehearsal. The basename is also the key looked up
	// in a checksums file.
	URL string
	// Bin is the binary's path inside a .tar.gz artifact. Empty means the
	// artifact is the bare binary; Generate fills it in for an archive.
	Bin string
}

// GenerateRequest is one manifest's worth of input.
type GenerateRequest struct {
	// Binary is the artifact to read the declaration out of. It has to be one
	// this machine can run, because reading a declaration means running it —
	// the same launch install performs, sandbox included.
	Binary string
	// Version overrides what the binary declares. A release tag is a fact
	// about the release and the declaration's own version is a fact about the
	// source, and they are allowed to differ; when this is empty the
	// declaration's stands.
	Version  string
	Homepage string

	Platforms []PlatformSource
	// Checksums maps artifact filename to hex sha256 — GoReleaser's
	// checksums.txt, parsed. Consulted for any platform whose artifact is not
	// a local file.
	Checksums map[string]string
}

// Generate reads the binary's declaration and returns the manifest it implies,
// as YAML ready to commit and as the parsed value.
func Generate(ctx context.Context, req GenerateRequest, stderr io.Writer) ([]byte, Manifest, *view.Error) {
	if len(req.Platforms) == 0 {
		return nil, Manifest{}, view.Errorf("plugin.manifest.platforms",
			"a manifest with no platform describes a plugin nobody can install").
			WithHint("`--platform <os>/<arch>=<url>`, once per artifact you publish")
	}
	declared, verr := describeBinary(ctx, req.Binary, stderr)
	if verr != nil {
		return nil, Manifest{}, verr
	}
	// Both of these are refused by ParseManifest at the end anyway, and its
	// message would be about the manifest. The person reading this one has a
	// plugin to fix, so the message is about the plugin.
	if declared.Summary == "" {
		return nil, Manifest{}, view.Errorf("plugin.manifest.declaration",
			"%s declares no summary, and an index entry is mostly its summary", declared.Name).
			WithHint("add Summary to the plugin's declaration and rebuild")
	}
	version := req.Version
	if version == "" {
		version = declared.Version
	}
	if version == "" {
		return nil, Manifest{}, view.Errorf("plugin.manifest.declaration",
			"%s declares no version and none was given", declared.Name).
			WithHint("`--version <v>` states one, or add Version to the declaration")
	}

	// Two checks the round trip below would also make, hoisted here because
	// its message is about the manifest and these two are about a flag. "the
	// generated manifest is wrong" is the right sentence for a bug in this
	// file and the wrong one for a value somebody typed.
	if req.Homepage != "" {
		if u, err := url.Parse(req.Homepage); err != nil || u.Scheme != "https" || u.Host == "" {
			return nil, Manifest{}, view.Errorf("plugin.manifest.homepage",
				"%q is not an https URL", req.Homepage).
				WithHint("`--homepage` is where a person reads more, and an index only " +
					"carries https for it")
		}
	}
	seenPlatform := map[string]bool{}
	for _, src := range req.Platforms {
		key := src.OS + "/" + src.Arch
		if seenPlatform[key] {
			return nil, Manifest{}, view.Errorf("plugin.manifest.platform",
				"%s is given twice", key).
				WithHint("one `--platform` per os/arch — a manifest states each once, " +
					"and there would be no way to say which artifact wins")
		}
		seenPlatform[key] = true
	}

	m := Manifest{
		Name:     declared.Name,
		Version:  version,
		Summary:  declared.Summary,
		Homepage: req.Homepage,
		Needs:    declared.Needs,
	}
	for _, c := range declared.Capabilities {
		m.Capabilities = append(m.Capabilities, CapabilityClaim{
			ID:      c.ID,
			Summary: c.Summary,
			Safety:  string(c.Safety),
			Grant:   c.NeedsGrant,
		})
	}
	for _, src := range req.Platforms {
		plat, verr := req.platform(ctx, src, artifactName(declared.Name))
		if verr != nil {
			return nil, Manifest{}, verr
		}
		m.Platforms = append(m.Platforms, plat)
	}
	// Sorted so that regenerating an unchanged plugin produces an unchanged
	// file. An index is a git repository and a diff that is all reordering is
	// a diff nobody reads.
	sort.Slice(m.Platforms, func(i, j int) bool {
		if m.Platforms[i].OS != m.Platforms[j].OS {
			return m.Platforms[i].OS < m.Platforms[j].OS
		}
		return m.Platforms[i].Arch < m.Platforms[j].Arch
	})

	raw, err := yaml.Marshal(m)
	if err != nil {
		return nil, Manifest{}, view.Errorf("plugin.manifest.write", "%v", err)
	}
	doc := append([]byte(header(declared.Name)), raw...)
	// The generator held to its own grammar. If this ever fires it is a bug
	// here rather than anything the author did, and it fires before the file
	// is written rather than at a stranger's install.
	parsed, verr := ParseManifest(doc)
	if verr != nil {
		return nil, Manifest{}, view.Errorf("plugin.manifest.write",
			"generated a manifest rta would refuse: %s", verr.Message).WithHint(verr.Hint)
	}
	return doc, parsed, nil
}

// FileName is where a manifest belongs inside an index. The index's layout is
// the operator-visible claim about a plugin's name, so this is not a
// suggestion — Manifests refuses a file whose name and content disagree.
func FileName(m Manifest) string { return path.Join("plugins", m.Name+".yaml") }

func header(name string) string {
	return "# " + artifactName(name) + ", as rta read it out of the binary.\n" +
		"#\n" +
		"# Everything here except the platform URLs came from the artifact's own\n" +
		"# declaration, so this file cannot disagree with the plugin it describes.\n" +
		"# `rta plugin install` derives all of it again from the bytes it downloads\n" +
		"# and refuses the install if the two differ, naming this index.\n" +
		"#\n" +
		"# Regenerate with `rta plugin manifest` rather than editing.\n"
}

// platform resolves one source into a claim: the checksum of the artifact,
// and the member to extract when it is an archive.
func (req GenerateRequest) platform(ctx context.Context, src PlatformSource, member string) (Platform, *view.Error) {
	bad := func(format string, args ...any) *view.Error {
		return view.Errorf("plugin.manifest.platform", format, args...)
	}
	u, err := url.Parse(src.URL)
	if err != nil || u.Scheme == "" {
		return Platform{}, bad("%s/%s: %q is not a URL", src.OS, src.Arch, src.URL)
	}
	base := path.Base(u.Path)
	if strings.HasSuffix(base, ".zip") {
		// Refused here rather than discovered at an install. rta extracts one
		// member from a .tar.gz and has no zip reader at all, so a manifest
		// naming a .zip is a manifest whose Windows entry cannot be installed
		// by the tool the manifest is for.
		return Platform{}, bad("%s/%s: %s is a zip, and rta extracts .tar.gz only",
			src.OS, src.Arch, base).
			WithHint("publish this platform as .tar.gz or as the bare binary — " +
				"including on Windows, where GoReleaser's default is zip")
	}

	plat := Platform{OS: src.OS, Arch: src.Arch, URL: src.URL, Bin: src.Bin}

	// An OCI reference answers both questions a checksums file was invented
	// to answer, and answers them better: the registry that will serve these
	// bytes states their digest, and its media type says whether they are an
	// archive or a bare binary. So no `--checksums` for an oci:// entry, and
	// the digest recorded is the one the serving registry published rather
	// than one copied out of a file beside the build.
	//
	// It costs one small request per platform, which is the trade the https
	// path deliberately does not make — there the alternative is downloading
	// the whole artifact, and here it is a few hundred bytes of JSON.
	if u.Scheme == "oci" {
		layer, verr := ociResolve(ctx, src.URL)
		if verr != nil {
			return Platform{}, verr
		}
		plat.SHA256 = strings.TrimPrefix(layer.Digest, "sha256:")
		if plat.Bin == "" && layer.IsArchive() {
			plat.Bin = member
			if src.OS == "windows" {
				plat.Bin += ".exe"
			}
		}
		return plat, nil
	}

	if plat.Bin == "" && strings.HasSuffix(base, ".tar.gz") {
		plat.Bin = member
		// Per platform, never per host. A Windows archive holds
		// rta-plugin-pg.exe — the Go toolchain and GoReleaser both put it
		// there, and nothing on Windows would run it under any other name —
		// so the member to extract differs by the OS the entry describes and
		// not by the OS generating the manifest. Getting this from the host
		// would produce a manifest that is right for whoever ran the command
		// and wrong for the other five platforms.
		if src.OS == "windows" {
			plat.Bin += ".exe"
		}
	}

	local := ""
	if u.Scheme == "file" {
		local = localPath(u)
	}
	if local != "" {
		sum, verr := digestFile(local)
		if verr != nil {
			return Platform{}, bad("%s/%s: hashing %s: %s", src.OS, src.Arch, local, verr.Message)
		}
		plat.SHA256 = sum
		// The one claim that is otherwise a guess, checked while the artifact
		// is in reach. `bin:` naming a member the archive does not hold fails
		// at install with a message about the index, which is a long way from
		// the person who can fix it.
		if plat.Bin != "" {
			if verr := memberExists(local, plat.Bin); verr != nil {
				return Platform{}, verr
			}
		}
		return plat, nil
	}
	sum, ok := req.Checksums[base]
	if !ok {
		return Platform{}, bad("%s/%s: nothing states the sha256 of %s", src.OS, src.Arch, base).
			WithHint("`--checksums <file>` reads the `<sha256>  <file>` lines a release " +
				"publishes; a file:// URL is hashed on the spot instead")
	}
	plat.SHA256 = sum
	return plat, nil
}

// memberExists proves a bin: claim against the archive it describes.
func memberExists(archive, member string) *view.Error {
	f, err := os.Open(archive)
	if err != nil {
		return view.Errorf("plugin.manifest.platform", "%v", err)
	}
	defer f.Close()
	if _, verr := extractMember(f, member, io.Discard); verr != nil {
		return view.Errorf("plugin.manifest.platform", "%s: %s", path.Base(archive), verr.Message).
			WithHint("checked while the artifact is in reach, so it cannot fail at " +
				"somebody else's install instead; `--bin <path inside the archive>` names it")
	}
	return nil
}

// checksumLine is a line of the `sha256sum` output every release tool writes:
// the hash, whitespace, an optional binary-mode asterisk, then the filename.
var checksumLine = regexp.MustCompile(`^([0-9a-f]{64})\s+\*?(\S.*)$`)

// checksumsCap bounds the file. It is the operator's own and not an index's,
// so this is a mistake-catcher rather than a defence: a gigabyte named where a
// checksums file was meant should say so instead of being read.
const checksumsCap = 1 << 20

// ParseChecksums reads a checksums file into filename → sha256. Keyed by
// basename, because that is what a URL's last segment is.
func ParseChecksums(raw []byte) (map[string]string, *view.Error) {
	if len(raw) > checksumsCap {
		return nil, view.Errorf("plugin.manifest.checksums",
			"the checksums file is %d bytes; the cap is %d", len(raw), checksumsCap)
	}
	out := map[string]string{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := checksumLine.FindStringSubmatch(line)
		if fields == nil {
			return nil, view.Errorf("plugin.manifest.checksums",
				"line %d is not `<sha256>  <file>`: %q", i+1, line)
		}
		out[path.Base(fields[2])] = fields[1]
	}
	if len(out) == 0 {
		return nil, view.Errorf("plugin.manifest.checksums", "the checksums file is empty")
	}
	return out, nil
}
