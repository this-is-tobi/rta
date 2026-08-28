package plugindist

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
	"github.com/this-is-tobi/rule-them-all/internal/plugintrust"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Install is a decision, and the decision is a digest (ADR 0017 §4). The
// sequence is claims first, then evidence: resolve the manifest, fetch the
// bytes, hash them, check the index's checksum claim, launch the binary in
// the same sandbox any load uses, and refuse if what it declares is not what
// the index said — each failure naming the index that made the claim. Only
// after the evidence agrees does anything become durable: the store, the
// trust entry, the lockfile.

// Report is what one install computed, for the CLI to render — every field
// observed by rta except the ones labelled claims.
type Report struct {
	Name      string
	Version   string // the index's claim
	Index     string
	URL       string
	Digest    string // computed by rta from the binary
	Signature string
	Path      string
	Declared  plugin.Plugin
}

// Install resolves spec ("name" or "index/name"), verifies, and places.
func Install(ctx context.Context, spec string, stderr io.Writer) (Report, *view.Error) {
	listed, verr := Resolve(spec)
	if verr != nil {
		return Report{}, verr
	}
	m := listed.Manifest
	if _, held := LockedFor(m.Name); held {
		return Report{}, view.Errorf("plugin.install.installed", "%s is already installed", m.Name).
			WithHint("`rta plugin upgrade " + m.Name + "` moves it; " +
				"`rta plugin remove " + m.Name + "` takes it out")
	}
	return installFrom(ctx, listed, stderr)
}

// installFrom is the shared half of Install and Upgrade: everything after
// "which manifest", up to and including the durable writes.
func installFrom(ctx context.Context, listed Listed, stderr io.Writer) (Report, *view.Error) {
	m := listed.Manifest
	plat, ok := m.PlatformFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return Report{}, view.Errorf("plugin.install.platform",
			"%s offers no %s/%s build", m.Name, runtime.GOOS, runtime.GOARCH).
			WithHint("it offers: " + m.Offered())
	}

	// Staging lives beside the store so the final rename is atomic — and in a
	// dot-directory so a crash leaves nothing a directory listing mistakes
	// for an installed plugin.
	if err := os.MkdirAll(filepath.Join(paths.Data(), "plugins"), 0o755); err != nil {
		return Report{}, view.Errorf("plugin.install.place", "%v", err)
	}
	staging, err := os.MkdirTemp(filepath.Join(paths.Data(), "plugins"), ".staging-*")
	if err != nil {
		return Report{}, view.Errorf("plugin.install.place", "%v", err)
	}
	defer os.RemoveAll(staging)

	artifact, err := os.Create(filepath.Join(staging, "artifact"))
	if err != nil {
		return Report{}, view.Errorf("plugin.install.place", "%v", err)
	}
	gotSHA, verr := fetchArtifact(ctx, plat.URL, artifact)
	if cerr := artifact.Close(); verr == nil && cerr != nil {
		verr = view.Errorf("plugin.install.fetch", "%v", cerr)
	}
	if verr != nil {
		return Report{}, verr
	}
	// The index's checksum is a claim, and this is the claim being checked —
	// what gets recorded is never this value but the binary digest below. A
	// mismatch here means the index lied or the transport swapped the bytes,
	// and either way the declaration claims are no longer worth verifying.
	if gotSHA != plat.SHA256 {
		return Report{}, view.Errorf("plugin.install.checksum",
			"the artifact's sha256 is %s and index %q claims %s — refusing the bytes",
			gotSHA[:12], listed.Index, plat.SHA256[:12]).
			WithHint("the index lied, or something between it and you rewrote the artifact; " +
				"`rta plugin index update " + listed.Index + "` may resolve a stale claim")
	}

	// The staged binary carries its final name: the filename is the
	// operator-side half of the D40 identity check, and the verification
	// launch below must see the same one the store will.
	staged := filepath.Join(staging, binaryName(m.Name))
	if plat.Bin != "" {
		archive, err := os.Open(filepath.Join(staging, "artifact"))
		if err != nil {
			return Report{}, view.Errorf("plugin.install.place", "%v", err)
		}
		out, err := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			archive.Close()
			return Report{}, view.Errorf("plugin.install.place", "%v", err)
		}
		_, verr = extractMember(archive, plat.Bin, out)
		archive.Close()
		if cerr := out.Close(); verr == nil && cerr != nil {
			verr = view.Errorf("plugin.install.place", "%v", cerr)
		}
		if verr != nil {
			return Report{}, verr
		}
	} else {
		if err := os.Rename(filepath.Join(staging, "artifact"), staged); err != nil {
			return Report{}, view.Errorf("plugin.install.place", "%v", err)
		}
		if err := os.Chmod(staged, 0o755); err != nil {
			return Report{}, view.Errorf("plugin.install.place", "%v", err)
		}
	}

	digest, verr := digestFile(staged)
	if verr != nil {
		return Report{}, verr
	}

	declared, verr := describeBinary(ctx, staged, stderr)
	if verr != nil {
		return Report{}, verr
	}
	if verr := verifyClaims(listed, declared); verr != nil {
		return Report{}, verr
	}

	sig := checkSignature(ctx, m, staged, stderr)

	dest, verr := place(m.Name, digest, staged)
	if verr != nil {
		return Report{}, verr
	}
	// Install is the trust decision: the operator typed it, against a digest
	// rta just computed and a declaration rta just verified. Recording it
	// here is what makes the plugin loadable without a second command.
	if verr := plugintrust.Add(digest, m.Name, dest); verr != nil {
		return Report{}, verr
	}
	entry := LockEntry{
		Name: m.Name, Digest: digest, Version: m.Version,
		Index: listed.Index, URL: plat.URL, Signature: sig,
		InstalledAt: time.Now().UTC().Truncate(time.Second),
	}
	if verr := recordInstall(entry); verr != nil {
		return Report{}, verr
	}
	return Report{
		Name: m.Name, Version: m.Version, Index: listed.Index, URL: plat.URL,
		Digest: digest, Signature: sig, Path: dest, Declared: declared,
	}, nil
}

// describeBinary launches one explicit binary — sandboxed, exactly as a load
// would — and returns what it declares. The plugin dev path, reused: an
// install verification that skipped confinement would run somebody else's
// fresh download with fewer guards than the plugin it is vetting.
func describeBinary(ctx context.Context, path string, stderr io.Writer) (plugin.Plugin, *view.Error) {
	host := pluginhost.New(stderr)
	defer host.CloseAll()
	client, err := host.Open(ctx, path)
	if err != nil {
		return plugin.Plugin{}, view.Errorf("plugin.install.describe", "%v", err).
			WithHint("rta verifies an index's claims by running the binary and asking what " +
				"it declares; a binary that cannot answer is not installed")
	}
	return client.Declared, nil
}

// verifyClaims is the check no opaque-executable manager can perform
// (ADR 0017 §2): the index said what this plugin declares, the binary just
// declared itself, and a disagreement is an install failure naming the index.
//
// The fields checked are the ones an authorization hangs off — the ID set,
// each capability's safety class, and whether it needs a grant — in both
// directions: a capability the binary declares beyond the claim is the ADR's
// own motivating case, and one the index promised that is not there means the
// operator decided from a menu the kitchen does not serve. Summaries are
// display and are not compared; once installed, the binary's own win.
func verifyClaims(listed Listed, declared plugin.Plugin) *view.Error {
	m := listed.Manifest
	lied := func(format string, args ...any) *view.Error {
		return view.Errorf("plugin.install.claims",
			"index %q claims %s and the binary disagrees: %s",
			listed.Index, m.Name, fmt.Sprintf(format, args...)).
			WithHint("the claim in the index is wrong or the artifact is not the one it " +
				"describes; either way this is the index's to fix, and nothing was installed")
	}
	if declared.Name != m.Name {
		return lied("it declares namespace %q", declared.Name)
	}
	claimed := map[string]CapabilityClaim{}
	for _, c := range m.Capabilities {
		claimed[c.ID] = c
	}
	var extra, missing []string
	for _, c := range declared.Capabilities {
		claim, ok := claimed[c.ID]
		if !ok {
			line := c.ID + " (" + string(c.Safety)
			if c.NeedsGrant {
				line += ", needs a grant"
			}
			extra = append(extra, line+")")
			continue
		}
		if string(c.Safety) != claim.Safety {
			return lied("%s is %s, not %s", c.ID, c.Safety, claim.Safety)
		}
		if c.NeedsGrant != claim.Grant {
			if c.NeedsGrant {
				return lied("%s needs a grant, and the claim says it does not", c.ID)
			}
			return lied("%s needs no grant, and the claim says it does", c.ID)
		}
		delete(claimed, c.ID)
	}
	for id := range claimed {
		missing = append(missing, id)
	}
	sort.Strings(extra)
	sort.Strings(missing)
	if len(extra) > 0 {
		return lied("it also declares %s, which the index never mentioned", strings.Join(extra, ", "))
	}
	if len(missing) > 0 {
		return lied("it does not declare %s", strings.Join(missing, ", "))
	}
	return nil
}

// cosignBin is overridable in tests; signature checking is recorded, never
// required (ADR 0017 §5).
var cosignBin = "cosign"

// checkSignature verifies the manifest's detached signature when one is
// stated and cosign is installed, and reports the outcome as a sentence for
// the lockfile and the install report. It never refuses anything: a valid
// signature on a worse plugin verifies perfectly, so the mechanism that does
// the work is the digest and the declaration check — this records provenance
// for the operator who wants it. A failure is spelled loudly all the same.
func checkSignature(ctx context.Context, m Manifest, binary string, stderr io.Writer) string {
	if m.Signature == nil {
		return "none stated"
	}
	if _, err := exec.LookPath(cosignBin); err != nil {
		return "not checked (cosign not installed)"
	}
	dir, err := os.MkdirTemp("", "rta-sig-*")
	if err != nil {
		return "not checked (" + err.Error() + ")"
	}
	defer os.RemoveAll(dir)
	fetchTo := func(name, url string) (string, bool) {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			return "", false
		}
		_, verr := fetchArtifact(ctx, url, f)
		if cerr := f.Close(); cerr != nil || verr != nil {
			return "", false
		}
		return f.Name(), true
	}
	sig, ok := fetchTo("artifact.sig", m.Signature.Sig)
	if !ok {
		return "not checked (signature unfetchable)"
	}
	key, ok := fetchTo("key.pub", m.Signature.Key)
	if !ok {
		return "not checked (key unfetchable)"
	}
	cmd := exec.CommandContext(ctx, cosignBin, "verify-blob",
		"--key", key, "--signature", sig, "--", binary)
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return "FAILED verification"
	}
	return "verified"
}

// Removed is what an uninstall leaves for the CLI to say: what went, and the
// statements elsewhere that now point at nothing — named rather than
// silently orphaned, because nothing else knows an uninstall happened
// (ADR 0017's own walkthrough finding).
type Removed struct {
	Name    string
	Digests []string
	// Orphans are config locations still naming this plugin.
	Orphans []string
}

// Remove uninstalls a managed plugin: the store, the trust entries for every
// stored digest, and the lockfile record.
func Remove(name string) (Removed, *view.Error) {
	if !plugin.ValidName(name) {
		return Removed{}, view.Errorf("plugin.remove.spec", "%q is not a plugin name", name)
	}
	digests := StoredDigests(name)
	_, locked := LockedFor(name)
	if len(digests) == 0 && !locked {
		return Removed{}, view.Errorf("plugin.remove.unknown", "%s is not managed by rta", name).
			WithHint("`rta plugin list` shows what is loadable; a plugin you copied onto " +
				"$PATH yourself is yours to remove the same way")
	}
	// By digest, never by name: untrusting by name would also revoke an
	// unmanaged same-named binary the operator trusted deliberately.
	for _, d := range digests {
		if _, verr := plugintrust.Remove(d); verr != nil {
			return Removed{}, verr
		}
	}
	if verr := removeStored(name); verr != nil {
		return Removed{}, verr
	}
	if verr := recordRemoval(name); verr != nil {
		return Removed{}, verr
	}
	return Removed{Name: name, Digests: digests, Orphans: orphanedConfig(name)}, nil
}

// Upgraded is one upgrade's outcome: the move, and the declaration diff that
// is the supply-chain event worth reading (ADR 0017 §4).
type Upgraded struct {
	Report
	UpToDate    bool
	FromDigest  string
	FromVersion string
	Diff        []string
}

// Upgrade moves one installed plugin to what its index now claims, printing
// the declaration diff. The previous digest's store directory is kept, so
// rollback stays a re-link rather than a re-download; its trust entry stands
// with it, because the operator's approval named that artifact and the
// artifact has not changed.
func Upgrade(ctx context.Context, name string, stderr io.Writer) (Upgraded, *view.Error) {
	locked, held := LockedFor(name)
	if !held {
		return Upgraded{}, view.Errorf("plugin.upgrade.unknown", "%s is not managed by rta", name).
			WithHint("`rta plugin install " + name + "` installs it; a copy you put on $PATH " +
				"yourself upgrades the same way it arrived")
	}
	listed, verr := Resolve(locked.Index + "/" + name)
	if verr != nil {
		return Upgraded{}, verr
	}

	// The old declaration, read before anything changes — from the installed
	// binary itself, which is the only honest source: the manifest's old
	// claim may never have matched and the declaration cache is a cache.
	oldPath := filepath.Join(StoreDir(), name, locked.Digest, binaryName(name))
	oldDecl, verr := describeBinary(ctx, oldPath, stderr)
	if verr != nil {
		return Upgraded{}, view.Errorf("plugin.upgrade.old",
			"the installed %s cannot describe itself: %s", name, verr.Message).
			WithHint("`rta plugin remove " + name + "` and a fresh install replace it")
	}

	report, verr := installFrom(ctx, listed, stderr)
	if verr != nil {
		return Upgraded{}, verr
	}
	if report.Digest == locked.Digest {
		return Upgraded{Report: report, UpToDate: true,
			FromDigest: locked.Digest, FromVersion: locked.Version}, nil
	}
	return Upgraded{
		Report: report, FromDigest: locked.Digest, FromVersion: locked.Version,
		Diff: declarationDiff(oldDecl, report.Declared),
	}, nil
}

// declarationDiff lists what changed between two declarations, in the three
// dimensions an authorization hangs off. A capability changing safety class,
// or a destructive one appearing, is the supply-chain event that matters —
// and precisely what a signature does not tell you.
func declarationDiff(old, new plugin.Plugin) []string {
	was := map[string]plugin.Capability{}
	for _, c := range old.Capabilities {
		was[c.ID] = c
	}
	var lines []string
	for _, c := range new.Capabilities {
		prev, existed := was[c.ID]
		if !existed {
			line := "+ " + c.ID + "  " + string(c.Safety)
			if c.NeedsGrant {
				line += ", needs a grant"
			}
			lines = append(lines, line)
			continue
		}
		if prev.Safety != c.Safety {
			lines = append(lines, "! "+c.ID+"  "+string(prev.Safety)+" → "+string(c.Safety))
		}
		if !prev.NeedsGrant && c.NeedsGrant {
			lines = append(lines, "! "+c.ID+"  now needs a grant")
		}
		if prev.NeedsGrant && !c.NeedsGrant {
			lines = append(lines, "! "+c.ID+"  no longer needs a grant")
		}
		delete(was, c.ID)
	}
	removed := make([]string, 0, len(was))
	for id := range was {
		removed = append(removed, "- "+id)
	}
	sort.Strings(removed)
	sort.Strings(lines)
	return append(lines, removed...)
}
