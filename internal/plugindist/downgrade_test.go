package plugindist

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/pkg/view"
)

// strippedHello is hello built with the linker stripping symbols: other bytes,
// the same declaration, built once for the package as hello is.
var (
	strippedOnce sync.Once
	strippedPath string
	strippedErr  error
)

func strippedHello(t *testing.T) string {
	t.Helper()
	strippedOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rta-dist-stripped-*")
		if err != nil {
			strippedErr = err
			return
		}
		strippedPath = filepath.Join(dir, pluginhost.BinaryName("hello"))
		cmd := exec.Command("go", "build", "-ldflags=-s -w", "-o", strippedPath, "../../examples/plugin-hello")
		if out, err := cmd.CombinedOutput(); err != nil {
			strippedErr = fmt.Errorf("%w: %s", err, out)
		}
	})
	if strippedErr != nil {
		t.Fatalf("building the stripped example plugin: %v", strippedErr)
	}
	if sha256Of(t, strippedPath) == sha256Of(t, hello(t)) {
		t.Skip("the stripped build hashed identically; nothing to move between")
	}
	return strippedPath
}

// claiming is helloManifest for artifact, claiming version.
func claiming(t *testing.T, artifact, version string) string {
	t.Helper()
	return strings.Replace(helloManifest(t, artifact, ""), "version: 0.1.0", "version: "+version, 1)
}

// nowhere claims version for an artifact that is not there, so fetching it
// fails as a fetch: a refusal that is not that one says nothing was fetched.
func nowhere(t *testing.T, version string) string {
	t.Helper()
	gone := filepath.Join(t.TempDir(), "gone")
	return strings.Replace(claiming(t, hello(t), version), fileURL(hello(t)), fileURL(gone), 1)
}

// installedAt installs hello claiming version from an index called lab, and
// returns the report and the index's repository.
func installedAt(t *testing.T, version string) (Report, string) {
	t.Helper()
	testData(t)
	repo := gitFixture(t, map[string]string{"hello": claiming(t, hello(t), version)})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatal(verr)
	}
	r, verr := Install(context.Background(), "hello")
	if verr != nil {
		t.Fatal(verr)
	}
	return r, repo
}

// claims moves the index to say manifest for hello.
func claims(t *testing.T, repo, manifest string) {
	t.Helper()
	writeManifests(t, repo, map[string]string{"hello": manifest})
	commitAll(t, repo, "claim")
	if verr := UpdateIndex(context.Background(), "lab"); verr != nil {
		t.Fatal(verr)
	}
}

// An index that now claims an older version is not followed unasked: the
// upgrade stops before the old binary is launched or the new one fetched — an
// artifact that is not there is never reached — and nothing moves. Its preview
// reaches the same verdict the same way.
func TestUpgradeStopsAtAnOlderClaimBeforeFetchingIt(t *testing.T) {
	first, repo := installedAt(t, "1.4.0")
	claims(t, repo, nowhere(t, "1.3.2"))

	for name, move := range map[string]func(context.Context, string) (Upgraded, *view.Error){
		"Upgrade": Upgrade, "PreviewUpgrade": PreviewUpgrade,
	} {
		up, verr := move(context.Background(), "hello")
		if verr == nil || verr.Code != CodeDowngrade {
			t.Fatalf("%s = %v, want %s before any fetch", name, verr, CodeDowngrade)
		}
		if !up.Older || up.FromVersion != "1.4.0" || up.Version != "1.3.2" || up.FromDigest != first.Digest || up.Digest != "" {
			t.Errorf("%s = %+v", name, up)
		}
	}
	if e, _ := LockedFor("hello"); e.Digest != first.Digest || e.Version != "1.4.0" {
		t.Errorf("lock = %+v, want it as installed", e)
	}
	if got, _ := CurrentDigest("hello"); got != first.Digest {
		t.Errorf("current = %s, want %s", got, first.Digest)
	}
	if stored := StoredDigests("hello"); len(stored) != 1 {
		t.Errorf("stored = %v, want the installed build alone", stored)
	}
	entries, _ := os.ReadDir(filepath.Join(paths.Data(), "plugins"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Errorf("%s was left behind", e.Name())
		}
	}
}

// The version somebody agreed to is the move: the lock records it and the
// build it names, bin/ points at it, and the build moved off stays stored and
// trusted, as an upgrade leaves it.
func TestDowngradeTakesTheOlderClaimItWasAgreedTo(t *testing.T) {
	first, repo := installedAt(t, "1.4.0")
	claims(t, repo, claiming(t, strippedHello(t), "1.3.2"))

	up, verr := Downgrade(context.Background(), "hello", "1.3.2")
	if verr != nil {
		t.Fatalf("downgrade: %v", verr)
	}
	if !up.Older || up.UpToDate || up.FromDigest != first.Digest || up.Digest == first.Digest {
		t.Fatalf("downgrade = %+v", up)
	}
	if e, _ := LockedFor("hello"); e.Version != "1.3.2" || e.Digest != up.Digest {
		t.Errorf("lock = %+v", e)
	}
	if got, _ := CurrentDigest("hello"); got != up.Digest {
		t.Errorf("current = %s, want %s", got, up.Digest)
	}
	if _, err := os.Stat(filepath.Join(StoreDir(), "hello", first.Digest)); err != nil {
		t.Error("the build moved off was taken out of the store")
	}
	if !plugintrust.Load().Trusts(first.Digest) {
		t.Error("the build moved off lost its trust")
	}
}

// A yes is to the version it was given for. An index that says another older
// version by the time it is acted on is refused, naming both, and nothing
// moves; one that says a newer version is simply upgraded to.
func TestDowngradeRefusesAnOlderClaimOtherThanTheOneAgreedTo(t *testing.T) {
	first, repo := installedAt(t, "1.4.0")
	stripped := strippedHello(t)
	claims(t, repo, claiming(t, stripped, "1.3.2"))

	_, verr := Downgrade(context.Background(), "hello", "1.3.1")
	if verr == nil || verr.Code != CodeDowngrade ||
		!strings.Contains(verr.Message, "1.3.2") || !strings.Contains(verr.Message, "1.3.1") {
		t.Fatalf("a yes to 1.3.1 over a claim of 1.3.2 = %v", verr)
	}
	if e, _ := LockedFor("hello"); e.Digest != first.Digest || e.Version != "1.4.0" {
		t.Errorf("lock = %+v, want it as installed", e)
	}

	claims(t, repo, claiming(t, stripped, "1.5.0"))
	up, verr := Downgrade(context.Background(), "hello", "1.3.1")
	if verr != nil || up.Older || up.Version != "1.5.0" {
		t.Fatalf("a newer claim under a yes to an older one = %+v, %v; want it upgraded", up, verr)
	}
}

// A version rta cannot order moves as it always did: nothing about it says
// back, so nothing is asked. The release moving to its own pre-release is the
// one move back this does not see, said in compareClaims.
func TestAnUnorderedClaimIsFollowedAsBefore(t *testing.T) {
	for _, tc := range []struct{ from, to string }{
		{"dev", "0.1.0"},
		{"1.4.0", "1.4.0-rc.1"},
		{"v1.2.0-3-gabc1234", "v1.2.0"},
	} {
		t.Run(tc.from+" to "+tc.to, func(t *testing.T) {
			_, repo := installedAt(t, tc.from)
			claims(t, repo, claiming(t, strippedHello(t), tc.to))
			up, verr := Upgrade(context.Background(), "hello")
			if verr != nil || up.Older || up.UpToDate {
				t.Fatalf("upgrade = %+v, %v; want it moved, not asked about", up, verr)
			}
		})
	}
}

// A sweep agrees to no older version: the plugin is held, as one that would
// gain authority is, before anything is fetched, and its record is unchanged.
func TestASweepHoldsBackAPluginItsIndexMovedBack(t *testing.T) {
	first, repo := installedAt(t, "1.4.0")
	claims(t, repo, nowhere(t, "1.3.2"))

	for name, sweep := range map[string]func(context.Context, string) []UpgradeOutcome{
		"UpgradeAll": UpgradeAll, "PreviewUpgradeAll": PreviewUpgradeAll,
	} {
		outcomes := sweep(context.Background(), "")
		if len(outcomes) != 1 {
			t.Fatalf("%s = %+v, want one outcome", name, outcomes)
		}
		o := outcomes[0]
		if !o.Skipped() || !o.Older || o.Problem != nil || len(o.Widenings) != 0 ||
			o.FromVersion != "1.4.0" || o.Version != "1.3.2" || o.FromDigest != first.Digest {
			t.Errorf("%s = %+v, want hello held, not failed", name, o)
		}
	}
	if e, _ := LockedFor("hello"); e.Digest != first.Digest || e.Version != "1.4.0" {
		t.Errorf("lock = %+v, want it as installed", e)
	}
}
