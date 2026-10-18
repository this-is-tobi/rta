package kv

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// **A key made for the job is a trade, and the page that offers it names the
// price.** "Needs no passphrase at all" is the whole appeal, and also the whole
// cost: the passphrase stops opening the store, and the key file is read by
// anything running as the person. `kv set` on a new store already says the second;
// the pages that steer toward the key file did not.
func TestTheOfferOfAKeyFileNamesWhatItCosts(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "first", "value": "v"}, false)

	body := recipientsEmpty(t)
	for _, want := range []string{"needs no passphrase at all", "passphrase stops opening the store",
		"anything running as you can read the key file"} {
		if !strings.Contains(body, want) {
			t.Errorf("the offer reads:\n%s\nwant it to say %q", body, want)
		}
	}
}

func TestTheRekeyToAGeneratedKeySaysThePassphraseNoLongerOpensTheStore(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "first", "value": "v"}, false)

	preview := text(t, runRekey, map[string]any{"only": true, "generate": true}, true)
	for _, want := range []string{"the passphrase would no longer open the store",
		"anything running as you can read the key file"} {
		if !strings.Contains(preview, want) {
			t.Errorf("the preview reads:\n%s\nwant it to say %q", preview, want)
		}
	}

	result := text(t, runRekey, map[string]any{"only": true, "generate": true}, false)
	for _, want := range []string{"the passphrase no longer opens the store",
		"anything running as you can read the key file"} {
		if !strings.Contains(result, want) {
			t.Errorf("the result reads:\n%s\nwant it to say %q", result, want)
		}
	}
}

// A store that was already locked to keys has no passphrase to stop mattering.
func TestTheRekeyOfAStoreAlreadyLockedToKeysDoesNotTalkOfAPassphrase(t *testing.T) {
	keyed := []string{"age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"}
	preview := rekeyPreview(true, true, []string{"age1new"}, keyed)
	summary := rekeySummary(plugin.SurfaceCLI, "/key", []string{"age1new"}, keyed, false)
	for name, body := range map[string]string{"preview": preview, "summary": summary} {
		if strings.Contains(body, "passphrase") {
			t.Errorf("a re-key of a store with no passphrase talks of one in its %s:\n%s", name, body)
		}
	}
}

// **Nothing here suggests putting the passphrase on the command line.** argv is
// in `ps` and in the shell's history; the variable is not, and a terminal asks.
func TestTheMissingPassphraseHintNeverSuggestsTheFlag(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "first", "value": "v"}, false)
	t.Setenv(passphraseEnv, "")

	r := plugin.NewRequest(map[string]any{"key": "first"}, false, false).WithSurface(plugin.SurfaceCLI)
	_, err := runGet(context.Background(), r)
	ve := view.AsError(err, "z")
	if ve.Code != "kv.passphrase.missing" {
		t.Fatalf("a read with nothing to open the store with = %+v", ve)
	}
	if ve.Hint != "run it at a terminal, or set "+passphraseEnv {
		t.Errorf("the hint reads %q", ve.Hint)
	}
	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceUnknown, plugin.SurfaceCompletion, plugin.SurfaceTUI, plugin.SurfaceMCP} {
		if strings.Contains(passphraseHint(sf), "--passphrase") {
			t.Errorf("%v: the passphrase hint %q names the flag", sf, passphraseHint(sf))
		}
	}
}
