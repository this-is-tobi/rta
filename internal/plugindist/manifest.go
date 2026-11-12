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
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/internal/yamlguard"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// APIVersionV1 is the one apiVersion this reader has: the shape of a manifest
// as every index holds it today. A manifest states no apiVersion at all and
// is read as this one, and stating it is accepted too.
//
// **Absent means v1, for as long as an index can hold such a file.** Every
// manifest published so far lacks the key, and so does every index pinned at an
// older commit with --ref, and the image that builds the first-party plugins
// in. Requiring it would hide all of them from every rta at once, which is
// the failure the field exists to prevent in the other direction.
//
// **The version is the schema's, not the plugin's.** Manifest.Version is the
// index's claim about a release and means nothing here, and neither does this
// field mean anything about trust: it decides whether this reader can parse a
// document, and nothing about whether to believe one. What an install trusts is
// the digest of bytes rta hashed itself, however a manifest is stamped.
//
// **When it moves.** Anything a reader must understand to decide safely — a
// claim that is compared, a restriction, a changed meaning, a removal — is a
// field of a new apiVersion, never an annotation, and a reader that does not
// know the version refuses the manifest rather than skipping what it cannot
// read. A new key that a reader may ignore without deciding any worse is what
// annotations are for. The index writer states the lowest apiVersion that
// carries every claim it makes, which for v1 is none, so that regenerating an
// index cannot hide a plugin from a release older than the one that starts to
// read the version it states (APIVersionV2, apiVersionFor).
const APIVersionV1 = "rta/v1"

// APIVersionV2 is a manifest for a plugin whose settings a reader of rta/v1
// alone misreads: one that renamed a config key (plugin.Plugin.Renamed), or
// that declares a key beside another that starts with it, `database` and
// `database.list.limit`. Such an rta ran the plugin with a key it no longer
// reads silently ignored, on the plugin's default, so a renamed TLS mode was
// the default mode, and could not read the second key at all. A reader of
// rta/v2 reads a key written flat or nested and refuses a section stating one
// nothing reads, so v2 adds no field: the version is the claim, that the
// plugin needs such a reader. An rta that reads only rta/v1 refuses the
// manifest and says to upgrade, rather than install a plugin it would run
// wrong (apiVersionFor writes it).
const APIVersionV2 = "rta/v2"

// Manifest is one plugin's entry in an index: claims about an artifact,
// written by whoever maintains the index. Krew's shape (a YAML file per
// plugin under plugins/), plus the one thing Krew structurally cannot carry —
// the plugin's declaration — because an rta plugin declares itself before it
// runs, so the claim is checkable at install.
type Manifest struct {
	// APIVersion is the schema this document is written in, first in the file
	// so that a reader which cannot parse the rest finds it at once. Empty is
	// APIVersionV1; see there for what it is and is not.
	APIVersion string `yaml:"apiVersion,omitempty"`

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
	// Claiming a need is not being granted it — `rta plugin access add` is still a
	// separate decision against the digest. This is what makes the decision an
	// informed one.
	Needs []plugin.Need `yaml:"needs,omitempty"`

	// There is no signature field. One manifest names an artifact per
	// platform, each a different binary, so one detached signature could
	// verify one of them at most, and a check that never gated an install
	// recorded "none stated" for every plugin there was. yaml.Strict refuses
	// the key at the top level, so an index still stating one hears so rather
	// than believing it is checked; under Annotations it is accepted, and
	// means exactly as little as anything else there.

	// Annotations is the one place a manifest may hold a key rta does not
	// read. Kubernetes' shape and for Kubernetes' reason: an index author can
	// keep a license, an owner or a pointer for their own tooling without a
	// release of rta to allow it, and without the strictness above, which
	// stays for everything else.
	//
	// **Inert, and nothing here may change that.** No reader of a manifest
	// compares an annotation with the binary, records one in rta.lock, shows
	// one or lets one alter what a claim says, and a key called `signature`
	// or `trusted` is a string like any other. If a reader must understand a
	// thing to decide safely, it is a field of a new apiVersion, which an
	// older reader refuses; an annotation is only ever something an older
	// reader loses nothing by missing.
	//
	// Bounded like the prose it is displayed beside, so a hostile index gets
	// no larger a surface here than in a summary: see checkAnnotations. Last
	// in the struct, so the writer's output stays in the order it always had.
	Annotations map[string]string `yaml:"annotations,omitempty"`
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

// manifestCap bounds one manifest file. A hostile index is a threat model
// here in a way a plugin's own declaration is not: an index is attached once
// and read on every search, by name, from a repository somebody else merges
// to.
const manifestCap = 1 << 20

// Caps on manifest prose, mirroring pkg/plugin's registration caps for the
// same channel: these strings are printed by search and install, and some
// reach a model through MCP-facing surfaces later.
const (
	maxManifestSummary  = 120
	maxManifestVersion  = 40
	maxManifestHomepage = 200

	// Annotations are held to a bound of their own and not to the manifest's
	// whole: sixteen short, visible lines is room for what an author keeps
	// beside a plugin, and any more is a place to put a document. The key is
	// Kubernetes' [prefix/]name, in the characters a label may carry; its 63 is
	// counted over the whole key, which is stricter than Kubernetes' per-part
	// count and can be widened later without hiding a manifest that was
	// readable, where narrowing could not.
	maxAnnotations     = 16
	maxAnnotationKey   = 63
	maxAnnotationValue = maxManifestSummary

	// maxEchoedClaim bounds how much of a value a refusal repeats back. The
	// value came from an index, so the message holds a clipped, quoted piece
	// of it and never the claim at its own length.
	maxEchoedClaim = 40
)

// CodeAPIVersion is the refusal of a manifest written for a newer rta than this
// one. It is addressed to whoever runs rta and not to the index author, who
// has done nothing wrong, and it is not a not-found code: the first-party
// offer asks to attach an index when none carries a plugin, and an index that
// carries it in a form this rta cannot read is the opposite case.
const CodeAPIVersion = "plugin.index.apiversion"

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
		// A document of a newer apiVersion fails here for whatever it changed
		// — a key this struct has no field for, a field it retyped — and that
		// symptom would blame the manifest for what is this rta being old. So
		// the version is read once more, on this path only, and a newer one
		// is what the manifest is refused for. The path a good manifest takes
		// parses once.
		if v := statedAPIVersion(raw); isNewerAPIVersion(v) {
			return m, newerAPIVersion(v)
		}
		verr := view.Errorf("plugin.index.manifest", "%s",
			strings.TrimSpace(yaml.FormatError(err, false, false)))
		var unknown *yaml.UnknownFieldError
		if errors.As(err, &unknown) {
			verr = verr.WithHint("a key rta does not read is refused, so a misspelling is heard; one meant for " +
				"other tools goes under `annotations:` at the top level, where it means nothing to rta")
		}
		return m, verr
	}
	// Before check, which is a judgement by this version's rules and has no
	// standing over a document written under another's.
	if verr := checkAPIVersion(m.APIVersion); verr != nil {
		return m, verr
	}
	if verr := m.check(); verr != nil {
		return m, verr
	}
	return m, nil
}

// statedAPIVersion reads the one key a failed decode can still be asked
// about, leniently: the document has already been refused, and its
// remaining keys are the reason, not the question. A document too broken to
// give one states none.
//
// Its caller has already refused anchors in these bytes; asked again here so
// the rule holds where the decode is, whoever calls it next.
func statedAPIVersion(raw []byte) string {
	if yamlguard.RefuseAnchors(raw) != nil {
		return ""
	}
	var probe struct {
		APIVersion string `yaml:"apiVersion"`
	}
	_ = yaml.Unmarshal(raw, &probe)
	return probe.APIVersion
}

// isNewerAPIVersion reports whether v is rta/v<N> for an N past the versions
// this reader has: ASCII digits only and no leading zero, so that every
// spelling is one number and "rta/v1" and "rta/v2" are the ones that are not
// newer. Compared as text because N is the index's and may be any size.
func isNewerAPIVersion(v string) bool {
	n, ok := strings.CutPrefix(v, "rta/v")
	if !ok || n == "" || n == "1" || n == "2" || n[0] == '0' {
		return false
	}
	for i := 0; i < len(n); i++ {
		if n[i] < '0' || n[i] > '9' {
			return false
		}
	}
	return true
}

// checkAPIVersion is the gate: nothing, v1 or v2 passes; a newer one is the
// reader's to act on; and anything else is a slip in the manifest.
func checkAPIVersion(v string) *view.Error {
	switch {
	case v == "" || v == APIVersionV1 || v == APIVersionV2:
		return nil
	case isNewerAPIVersion(v):
		return newerAPIVersion(v)
	}
	return view.Errorf("plugin.index.manifest",
		"apiVersion %q is not one rta knows — a manifest states %s or %s, or no apiVersion at all",
		format.Clip(v, maxEchoedClaim), APIVersionV1, APIVersionV2)
}

// apiVersionFor is the lowest apiVersion that carries every claim a manifest
// of p makes: APIVersionV2 for a declaration a reader of rta/v1 misreads, and
// none otherwise, so that regenerating an index hides no other plugin from a
// release older than the first to read rta/v2.
func apiVersionFor(p plugin.Plugin) string {
	if len(p.Renamed) > 0 {
		return APIVersionV2
	}
	keys := map[string]bool{}
	for _, c := range p.Capabilities {
		for _, f := range c.Inputs {
			if f.Config != "" {
				keys[f.Config] = true
			}
		}
	}
	for key := range keys {
		for i := strings.IndexByte(key, '.'); i > 0; i = nextDot(key, i) {
			if keys[key[:i]] {
				return APIVersionV2
			}
		}
	}
	return ""
}

// nextDot is the index of the dot in key after the one at i, -1 when none.
func nextDot(key string, i int) int {
	if j := strings.IndexByte(key[i+1:], '.'); j >= 0 {
		return i + 1 + j
	}
	return -1
}

// newerAPIVersion is the refusal of a manifest written for a later rta. It
// stands alone, because `index add` and `outdated` print the message and not
// the hint: it says what the manifest stated, what this rta reads, which rta
// this is, and whose move it is. No text of the manifest becomes an
// instruction — the value is clipped and quoted, and "upgrade rta" is ours.
func newerAPIVersion(v string) *view.Error {
	running := ""
	if self := session.Self(); self != "" {
		running = " (" + self + ")"
	}
	return view.Errorf(CodeAPIVersion,
		"apiVersion %q is newer than %s, which this rta%s reads — upgrade rta",
		format.Clip(v, maxEchoedClaim), APIVersionV2, running)
}

// check is every rule a manifest must satisfy before its claims are shown to
// anybody. The grammar rules are pkg/plugin's own exports, so a claim is held
// to exactly the grammar the binary will be held to at registration — one
// home per rule.
func (m Manifest) check() *view.Error {
	bad := func(msg string, args ...any) *view.Error {
		return view.Errorf("plugin.index.manifest", msg, args...)
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
		// Shown to a person, from an index, so held to what a summary is. The
		// parse below admits a bidi override and an escape in a path, and a
		// link that displays as another one is what those are for.
		if verr := cleanLine("homepage", m.Homepage, maxManifestHomepage); verr != nil {
			return verr
		}
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
	return m.checkAnnotations()
}

// checkAnnotations holds the one free-form part of a manifest to the bounds
// that keep it from being a second place to put a summary. A violation
// refuses the manifest and does not drop the annotation: what is displayed
// beside a plugin must display as what it is, and a reader that quietly kept
// the manifest minus one line would be deciding what the author meant. Keys are
// walked in order so that the refusal is the same on every run.
func (m Manifest) checkAnnotations() *view.Error {
	bad := func(msg string, args ...any) *view.Error {
		return view.Errorf("plugin.index.manifest", msg, args...)
	}
	if len(m.Annotations) > maxAnnotations {
		return bad("%s carries %d annotations; at most %d are allowed", m.Name, len(m.Annotations), maxAnnotations)
	}
	for _, key := range slices.Sorted(maps.Keys(m.Annotations)) {
		shown := strconv.Quote(format.Clip(key, maxAnnotationKey+1))
		if n := utf8.RuneCountInString(key); n > maxAnnotationKey {
			return bad("annotation %s is %d characters; the cap is %d", shown, n, maxAnnotationKey)
		}
		if !annotationKey(key) {
			return bad("annotation %s is not a key: [prefix/]name, in lowercase letters, digits, "+
				"'-', '_' and '.', each part beginning and ending with a letter or digit", shown)
		}
		if verr := cleanLine("annotation "+shown+" value", m.Annotations[key], maxAnnotationValue); verr != nil {
			return verr
		}
	}
	return nil
}

// annotationKey is [prefix/]name with at most one slash, each part made of
// [a-z0-9._-] and beginning and ending with a letter or digit. By hand, like
// token: a loop over bytes holds a key of any size to a grammar this small
// without a pattern to run, on a path every manifest of every search takes.
func annotationKey(key string) bool {
	prefix, name, prefixed := strings.Cut(key, "/")
	if !prefixed {
		return annotationPart(key)
	}
	return annotationPart(prefix) && annotationPart(name)
}

func annotationPart(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if alnum {
			continue
		}
		if (c != '.' && c != '_' && c != '-') || i == 0 || i == len(s)-1 {
			return false
		}
	}
	return true
}

func (p Platform) check(name string) *view.Error {
	bad := func(msg string, args ...any) *view.Error {
		return view.Errorf("plugin.index.manifest", msg, args...)
	}
	if !token(p.OS) || !token(p.Arch) {
		return bad("%s: platform %q/%q is not os/arch", name, p.OS, p.Arch)
	}
	if verr := checkArtifactURL(name, p.OS+"/"+p.Arch, p.URL); verr != nil {
		return verr
	}
	if !IsSHA256Hex(p.SHA256) {
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

// IsSHA256Hex reports whether s is a SHA-256 digest as rta writes and
// compares one: 64 lowercase hex characters.
func IsSHA256Hex(s string) bool {
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
		// The gate's own rule (grant.Required): a capability that is not a
		// read needs a grant whether or not its declaration says so. Counting
		// only the claim read "3 write · 1 destructive · none needs a grant"
		// of a plugin whose every write is refused an agent until one is issued.
		if c.Grant || c.Safety != "read" {
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
	// A manifest claims no inputs, so it cannot say whether a capability takes
	// a profile, and "none needs a grant" is said with the clause that makes
	// it true of the one that does: a person choosing a plugin from this line
	// is told the cautious thing about a plugin that may talk to a service.
	parts = append(parts, plugin.GrantNeedLine(grants, true))
	if len(m.Needs) > 0 {
		asks := make([]string, len(m.Needs))
		for i, n := range m.Needs {
			asks[i] = string(n)
		}
		parts = append(parts, "asks for "+strings.Join(asks, ", "))
	}
	return strings.Join(parts, " · ")
}
