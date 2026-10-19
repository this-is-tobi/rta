package app

import (
	"context"
	"slices"
	"testing"

	"github.com/this-is-tobi/rta/internal/recent"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Shell completion of a Live input that holds a list. A shell never asks a
// service (docs: completion), so for such an input what completes is what the
// operator used before and what the profile's fence reaches — and what a list
// adds is the element: the part after the last comma of the word, and no name
// an earlier occurrence of the flag already gave.

func liveListRegistry(t *testing.T, called *int) *registry.Registry {
	t.Helper()
	reg := registry.New()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	suggest := func(context.Context, plugin.Request) []string {
		*called++
		return []string{"from-the-service"}
	}
	if err := reg.Register(plugin.Plugin{Name: "s3", Summary: "s3", Capabilities: []plugin.Capability{{
		ID: "s3.bucket.list", Summary: "list buckets", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "bucket", Type: plugin.StringSlice, Config: "bucket", Live: true, Suggest: suggest},
			{Name: "region", Type: plugin.String, Config: "region", Live: true, Suggest: suggest},
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	return reg
}

const fencedBuckets = `
profiles:
  staging:
    plugins:
      s3:
        only:
          bucket: [shop-assets, shop-logs, shop-audit]
          region: [eu-west-1]
`

func rememberBuckets(t *testing.T, profile string, buckets ...string) {
	t.Helper()
	withHistory(t, recent.Values{recent.Key(profile, "s3.bucket.list", "bucket"): buckets})
}

// A live list never reaches the service from a shell, whatever is on the line:
// the Suggest is the deliberate press's, and a shell word is not that.
func TestShellCompletionOfALiveListNeverAsksTheService(t *testing.T) {
	var called int
	reg := liveListRegistry(t, &called)
	rememberBuckets(t, "", "backups", "media")

	for _, line := range [][]string{
		{"--bucket", ""}, {"--bucket", "ba"}, {"--bucket", "backups,"}, {"--bucket=backups,me"},
		{"--bucket", "backups", "--bucket", ""}, {"--region", ""},
	} {
		complete(t, reg, append([]string{"s3", "bucket", "list"}, line...)...)
	}
	if called != 0 {
		t.Errorf("shell completion ran a Live Suggest %d times", called)
	}
}

// The element after the comma completes, and the elements before it are not
// offered again. What is offered is what the operator used: the listing is
// not a shell's to fetch.
func TestAShellCompletesTheElementOfALiveListAfterTheComma(t *testing.T) {
	var called int
	reg := liveListRegistry(t, &called)
	rememberBuckets(t, "", "backups", "media", "metrics")
	list := []string{"s3", "bucket", "list"}

	for _, tc := range []struct {
		name string
		line []string
		want []string
	}{
		{"an empty word", []string{"--bucket", ""}, []string{"backups", "media", "metrics"}},
		{"the first element", []string{"--bucket", "me"}, []string{"media", "metrics"}},
		{"after a comma", []string{"--bucket", "backups,"}, []string{"backups,media", "backups,metrics"}},
		{"the part after the comma", []string{"--bucket", "backups,me"}, []string{"backups,media", "backups,metrics"}},
		{"in the equals form", []string{"--bucket=backups,me"}, []string{"backups,media", "backups,metrics"}},
		{"a list already naming them", []string{"--bucket", "backups,media,"}, []string{"backups,media,metrics"}},
		// The word being completed is the box being typed in, not a value the
		// line gave: a name typed out whole is still the one to complete.
		{"a whole name", []string{"--bucket", "media"}, []string{"media"}},
		{"a whole name in the equals form", []string{"--bucket=media"}, []string{"media"}},
	} {
		if got := values(complete(t, reg, append(slices.Clone(list), tc.line...)...)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v offers %q, want %q", tc.name, tc.line, got, tc.want)
		}
	}
}

// A repeated flag is one list, so a name an earlier occurrence gave is as good
// as one chosen ahead of the comma: it is not offered again, and a comma in the
// word being completed leaves out both.
func TestAShellLeavesOutWhatAnEarlierOccurrenceOfTheFlagGave(t *testing.T) {
	var called int
	reg := liveListRegistry(t, &called)
	rememberBuckets(t, "", "backups", "media", "metrics")
	list := []string{"s3", "bucket", "list"}

	for _, tc := range []struct {
		name string
		line []string
		want []string
	}{
		{"one earlier", []string{"--bucket", "backups", "--bucket", ""}, []string{"media", "metrics"}},
		{"two earlier", []string{"--bucket", "backups", "--bucket", "media", "--bucket", ""}, []string{"metrics"}},
		{"a list in an earlier one", []string{"--bucket", "backups,media", "--bucket", ""}, []string{"metrics"}},
		{"earlier and a comma", []string{"--bucket", "backups", "--bucket", "media,"},
			[]string{"media,metrics"}},
	} {
		if got := values(complete(t, reg, append(slices.Clone(list), tc.line...)...)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v offers %q, want %q", tc.name, tc.line, got, tc.want)
		}
	}
}

// Under a fence the allowlist is what a live list completes from, as it is for
// an input with nothing of its own, and nothing it would refuse is offered:
// not what was used under the profile, not what another profile used.
func TestAShellOffersAFencedLiveListOnlyWhatTheFenceHolds(t *testing.T) {
	var called int
	reg := liveListRegistry(t, &called)
	rememberBuckets(t, "staging", "prod-assets", "shop-logs")
	list := []string{"s3", "bucket", "list", "--profile", "staging"}

	for _, tc := range []struct {
		name string
		line []string
		want []string
	}{
		{"an empty word", []string{"--bucket", ""}, []string{"shop-assets", "shop-logs", "shop-audit"}},
		{"after a comma", []string{"--bucket", "shop-assets,"}, []string{"shop-assets,shop-logs", "shop-assets,shop-audit"}},
		{"a repeated flag", []string{"--bucket", "shop-assets", "--bucket", "shop-"},
			[]string{"shop-logs", "shop-audit"}},
		{"a name the fence refuses is not completed", []string{"--bucket", "prod"}, nil},
	} {
		out, errOut, err := runWith(t, reg, fencedBuckets, append([]string{"__complete"}, append(slices.Clone(list), tc.line...)...)...)
		if err != nil {
			t.Fatalf("%s: %v %q", tc.name, err, errOut)
		}
		if got := offered(out); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v offers %q, want %q", tc.name, tc.line, got, tc.want)
		}
	}
	if called != 0 {
		t.Errorf("shell completion ran a Live Suggest %d times", called)
	}
}

// A Live input that holds one value is as it was: nothing from the service, the
// fence's entry or what was used, whole.
func TestAShellCompletesALiveStringAsItAlwaysDid(t *testing.T) {
	var called int
	reg := liveListRegistry(t, &called)
	withHistory(t, recent.Values{recent.Key("", "s3.bucket.list", "region"): {"eu-west-1", "us-east-1"}})

	if got := values(complete(t, reg, "s3", "bucket", "list", "--region", "")); !slices.Equal(got, []string{"eu-west-1", "us-east-1"}) {
		t.Errorf("--region offers %q, want what was used", got)
	}
	// Not a list: the word is the shell's to match, whole, and a value the
	// line already gave is not taken out of the offer.
	if got := values(complete(t, reg, "s3", "bucket", "list", "--region", "eu-west-1", "--region", "")); !slices.Equal(got, []string{"eu-west-1", "us-east-1"}) {
		t.Errorf("a repeated --region offers %q, want both as it always did", got)
	}
	out, _, err := runWith(t, reg, fencedBuckets, "__complete", "s3", "bucket", "list", "--profile", "staging", "--region", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := offered(out); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("a fenced --region offers %q, want the fence's entry", got)
	}
	if called != 0 {
		t.Errorf("shell completion ran a Live Suggest %d times", called)
	}
}
