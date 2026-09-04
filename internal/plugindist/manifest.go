// Package plugindist is plugin distribution: indexes that state
// claims, a managed store, and a lockfile recording what rta computed.
//
// The one line the whole design hangs on: an index is a list of claims made
// by somebody else, and rta.lock records only what rta itself observed. A
// manifest's checksum, declaration and version are useful — they are how
// search works without downloading anything — and none of them is evidence.
// Install fetches the bytes, hashes them itself, launches the binary in the
// same sandbox any load uses, and refuses when the binary's declaration
// disagrees with the index's claim, naming the index that lied.
package plugindist

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rule-them-all/internal/textclean"
	"github.com/this-is-tobi/rule-them-all/internal/yamlguard"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Manifest is one plugin's entry in an index: claims about an artifact,
// written by whoever maintains the index. Krew's shape (a YAML file per
// plugin under plugins/), plus the one thing Krew structurally cannot carry —
// the plugin's declaration — because an rta plugin declares itself before it
// runs, so the claim is checkable at install.
type Manifest struct {
	// Name is the namespace this manifest claims, and the file must be
	// called <name>.yaml — the same rule again: where the name in a thing and
	// the name on the thing disagree, the operator-visible one wins, and here
	// the index's file layout is what an operator browses.
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	Summary string `yaml:"summary"`
	// Homepage is where a person reads more; shown, never fetched.
	Homepage string `yaml:"homepage,omitempty"`

	Platforms    []Platform        `yaml:"platforms"`
	Capabilities []CapabilityClaim `yaml:"capabilities"`

	// Needs is the credential locations the artifact declares it cannot work
	// without, and it is the one claim a person most wants before deciding
	// rather than after. A plugin that reads your kubeconfig says so in its
	// declaration; an index that carries the declaration and drops that field
	// would be describing the plugin accurately right up to the part that
	// matters. Checked at install in both directions like the capability set,
	// so an index cannot quietly omit one.
	//
	// Claiming a need is not being granted it — `rta plugin allow` is still a
	// separate decision against the digest. This is what makes the decision an
	// informed one.
	Needs []plugin.Need `yaml:"needs,omitempty"`

	// Signature names a detached signature for the artifact, verified when
	// present and recorded — never required, never a gate: a
	// valid signature on a worse plugin verifies perfectly, and the campaigns
	// Shipped malware has carried valid provenance before now.
	Signature *SignatureClaim `yaml:"signature,omitempty"`
}

// Platform is where the artifact for one os/arch lives and what the index
// claims about it.
type Platform struct {
	OS   string `yaml:"os"`
	Arch string `yaml:"arch"`
	// URL is https://, oci:// or file://. Never http: a plugin binary fetched
	// in cleartext is an install-time code injection waiting for a
	// coffee-shop network.
	//
	// An oci:// artifact is one layer of a registry manifest, fetched
	// anonymously — see oci.go. Its digest is stated twice, by the registry
	// and by SHA256 below, and rta computes a third from the bytes.
	URL string `yaml:"url"`
	// SHA256 is the index's claim about the artifact bytes — the download,
	// which for an archive is not the binary. rta checks it after fetching
	// and refuses a mismatch naming the index; what gets recorded and pinned
	// is always the digest rta computed from the binary itself.
	SHA256 string `yaml:"sha256"`
	// Bin is the binary's path inside a .tar.gz artifact; empty means the
	// artifact is the bare binary.
	Bin string `yaml:"bin,omitempty"`
}

// CapabilityClaim is what the index says one capability is. Verified against
// the binary's own declaration at install: the ID set must match exactly and
// safety and grant must agree — those are the fields an authorization hangs
// off. The summary is display, and the binary's own wins once installed.
type CapabilityClaim struct {
	ID      string `yaml:"id"`
	Summary string `yaml:"summary,omitempty"`
	Safety  string `yaml:"safety"`
	Grant   bool   `yaml:"grant,omitempty"`
}

// SignatureClaim names the detached signature and the key to verify it with.
type SignatureClaim struct {
	Sig string `yaml:"sig"`
	Key string `yaml:"key"`
}

// manifestCap bounds one manifest file. A hostile index is a threat model
// here in a way a plugin's own declaration is not: an index is attached once
// and read on every search, by name, from a repository somebody else merges
// to.
const manifestCap = 1 << 20

// Caps on manifest prose, mirroring pkg/plugin's registration caps for the
// same channel: these strings are printed by search and install, and some
// reach a model through MCP-facing surfaces later.
const (
	maxManifestSummary = 120
	maxManifestVersion = 40
)

// ParseManifest reads and validates one manifest. Every refusal names the
// field, because the person who can act on it is the index author reading
// their own file.
func ParseManifest(raw []byte) (Manifest, *view.Error) {
	var m Manifest
	if len(raw) > manifestCap {
		return m, view.Errorf("plugin.index.manifest", "manifest is %d bytes; the cap is %d",
			len(raw), manifestCap)
	}
	// manifestCap above bounds the bytes on disk; this bounds what they
	// expand into, and the two are not interchangeable — a 620-byte manifest
	// nesting aliases through the fields Manifest actually declares costs
	// 3.86 GB, comfortably under the 1 MB cap. yaml.Strict() does not help
	// either: it is DisallowUnknownField, so it refuses a misspelled key and
	// says nothing about a legal one whose value fans out.
	if err := yamlguard.RefuseAnchors(raw); err != nil {
		return m, view.Errorf("plugin.index.manifest", "%v", err)
	}
	if err := yaml.UnmarshalWithOptions(raw, &m, yaml.Strict()); err != nil {
		return m, view.Errorf("plugin.index.manifest", "%s",
			strings.TrimSpace(yaml.FormatError(err, false, false)))
	}
	if verr := m.check(); verr != nil {
		return m, verr
	}
	return m, nil
}

// check is every rule a manifest must satisfy before its claims are shown to
// anybody. The grammar rules are pkg/plugin's own exports, so a claim is held
// to exactly the grammar the binary will be held to at registration — one
// home per rule.
func (m Manifest) check() *view.Error {
	bad := func(format string, args ...any) *view.Error {
		return view.Errorf("plugin.index.manifest", format, args...)
	}
	if !plugin.ValidName(m.Name) {
		return bad("name %q is not a plugin namespace (lowercase [a-z0-9-])", m.Name)
	}
	if verr := cleanLine("version", m.Version, maxManifestVersion); verr != nil {
		return verr
	}
	if m.Version == "" {
		return bad("%s states no version", m.Name)
	}
	if verr := cleanLine("summary", m.Summary, maxManifestSummary); verr != nil {
		return verr
	}
	if m.Summary == "" {
		return bad("%s states no summary", m.Name)
	}
	if m.Homepage != "" {
		u, err := url.Parse(m.Homepage)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return bad("%s: homepage %q is not an https URL", m.Name, m.Homepage)
		}
	}

	if len(m.Platforms) == 0 {
		return bad("%s offers no platforms", m.Name)
	}
	seen := map[string]bool{}
	for _, p := range m.Platforms {
		if verr := p.check(m.Name); verr != nil {
			return verr
		}
		key := p.OS + "/" + p.Arch
		if seen[key] {
			return bad("%s states %s twice", m.Name, key)
		}
		seen[key] = true
	}

	if len(m.Capabilities) == 0 {
		return bad("%s claims no capabilities — there is nothing to install a plugin for", m.Name)
	}
	ids := map[string]bool{}
	for _, c := range m.Capabilities {
		if !plugin.ValidID(c.ID) {
			return bad("%s: %q is not a capability ID", m.Name, c.ID)
		}
		if !strings.HasPrefix(c.ID, m.Name+".") {
			return bad("%s claims %q, which is outside its own namespace", m.Name, c.ID)
		}
		if ids[c.ID] {
			return bad("%s claims %s twice", m.Name, c.ID)
		}
		ids[c.ID] = true
		if !plugin.ValidSafety(c.Safety) {
			return bad("%s: %s states safety %q; the classes are read, write, destructive",
				m.Name, c.ID, c.Safety)
		}
		if verr := cleanLine(c.ID+" summary", c.Summary, maxManifestSummary); verr != nil {
			return verr
		}
	}

	seenNeed := map[plugin.Need]bool{}
	for _, n := range m.Needs {
		if !plugin.KnownNeed(n) {
			return bad("%s asks for %q, which is not a location rta knows how to allow", m.Name, n)
		}
		if seenNeed[n] {
			return bad("%s asks for %s twice", m.Name, n)
		}
		seenNeed[n] = true
	}

	if s := m.Signature; s != nil {
		for what, ref := range map[string]string{"sig": s.Sig, "key": s.Key} {
			if verr := checkArtifactURL(m.Name, "signature "+what, ref); verr != nil {
				return verr
			}
		}
	}
	return nil
}

func (p Platform) check(name string) *view.Error {
	bad := func(format string, args ...any) *view.Error {
		return view.Errorf("plugin.index.manifest", format, args...)
	}
	if !token(p.OS) || !token(p.Arch) {
		return bad("%s: platform %q/%q is not os/arch", name, p.OS, p.Arch)
	}
	if verr := checkArtifactURL(name, p.OS+"/"+p.Arch, p.URL); verr != nil {
		return verr
	}
	if !sha256Hex(p.SHA256) {
		return bad("%s (%s/%s): sha256 %q is not 64 hex characters", name, p.OS, p.Arch, p.SHA256)
	}
	if p.Bin != "" {
		// The one member install extracts. Slash-separated and confined by
		// construction: a cleaned relative path with no way up, because this
		// string decides which archive member becomes an executable.
		clean := path.Clean(p.Bin)
		if path.IsAbs(clean) || clean != p.Bin || clean == "." ||
			strings.HasPrefix(clean, "../") || strings.Contains(p.Bin, "\\") {
			return bad("%s (%s/%s): bin %q is not a clean relative path inside the archive",
				name, p.OS, p.Arch, p.Bin)
		}
	}
	return nil
}

// checkArtifactURL admits the schemes an artifact may be fetched over.
// file:// exists for local indexes — `make index` builds one — and for tests;
// oci:// names one layer of a registry manifest, which is what an index
// reaches for when a release page is the wrong place to put an artifact.
func checkArtifactURL(name, what, raw string) *view.Error {
	u, err := url.Parse(raw)
	if err != nil {
		return view.Errorf("plugin.index.manifest", "%s: %s URL %q does not parse", name, what, raw)
	}
	switch u.Scheme {
	case "https", "oci":
		if u.Host == "" {
			return view.Errorf("plugin.index.manifest", "%s: %s URL %q has no host", name, what, raw)
		}
	case "file":
		if u.Path == "" || !strings.HasPrefix(u.Path, "/") {
			return view.Errorf("plugin.index.manifest",
				"%s: %s file URL %q must carry an absolute path", name, what, raw)
		}
	case "http":
		return view.Errorf("plugin.index.manifest",
			"%s: %s URL is plain http — a binary fetched in cleartext is not fetched, "+
				"it is accepted from whoever is nearest", name, what)
	default:
		return view.Errorf("plugin.index.manifest",
			"%s: %s URL %q: the schemes are https, file and oci", name, what, raw)
	}
	return nil
}

// cleanLine refuses prose that would display as something other than what it
// is. textclean.Deceives is the predicate — control and escape sequences,
// invisibles, bidi, embedded newlines and tabs — because a manifest line is
// exactly a completion entry's situation: one line, shown to a person who
// decides from it.
func cleanLine(what, s string, max int) *view.Error {
	if s == "" {
		return nil
	}
	if n := utf8.RuneCountInString(s); n > max {
		return view.Errorf("plugin.index.manifest", "%s is %d runes; the cap is %d", what, n, max)
	}
	if textclean.Deceives(s) {
		return view.Errorf("plugin.index.manifest",
			"%s holds control or invisible characters — a claim must display as what it is", what)
	}
	return nil
}

func token(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func sha256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// PlatformFor selects the artifact for one os/arch.
func (m Manifest) PlatformFor(goos, goarch string) (Platform, bool) {
	for _, p := range m.Platforms {
		if p.OS == goos && p.Arch == goarch {
			return p, true
		}
	}
	return Platform{}, false
}

// Offered lists the platforms a manifest carries, for the refusal that has to
// say what it does offer.
func (m Manifest) Offered() string {
	out := make([]string, 0, len(m.Platforms))
	for _, p := range m.Platforms {
		out = append(out, p.OS+"/"+p.Arch)
	}
	return strings.Join(out, ", ")
}

// SafetyLine summarises the claimed classes the way the install prompt prints
// them: "all read · none needs a grant", or the counts when it is mixed.
//
// A declared credential location belongs on this line and not a column of its
// own. It is the same kind of fact as a safety class — how far the thing
// reaches — and `rta plugin search` is where somebody reads one line per
// plugin and decides which to look at properly. "all read" on a plugin that
// wants your kubeconfig is true and, alone, misleading.
func (m Manifest) SafetyLine() string {
	counts := map[string]int{}
	grants := 0
	for _, c := range m.Capabilities {
		counts[c.Safety]++
		if c.Grant {
			grants++
		}
	}
	var parts []string
	if counts["read"] == len(m.Capabilities) {
		parts = append(parts, "all read")
	} else {
		for _, s := range []string{"read", "write", "destructive"} {
			if counts[s] > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
			}
		}
	}
	switch grants {
	case 0:
		parts = append(parts, "none needs a grant")
	case 1:
		parts = append(parts, "1 needs a grant")
	default:
		parts = append(parts, fmt.Sprintf("%d need a grant", grants))
	}
	if len(m.Needs) > 0 {
		asks := make([]string, len(m.Needs))
		for i, n := range m.Needs {
			asks[i] = string(n)
		}
		parts = append(parts, "asks for "+strings.Join(asks, ", "))
	}
	return strings.Join(parts, " · ")
}
