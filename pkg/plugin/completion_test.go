package plugin

import (
	"context"
	"testing"
)

// The two completion channels and the line between them: the keystroke
// channel never runs a live Suggest and never carries a credential; the
// deliberate channel hands the pinned plugin what the run would.

// A live Suggest never answers the keystroke channel.
//
// Candidates is what the TUI re-evaluates as siblings change and what shell
// completion calls with a credential-less request. A live Suggest reaching
// it would put a service read on a typing cadence — and, called with the
// stripped request, would fail against exactly the connections profiles
// exist for, teaching the operator that completion is broken rather than
// gated.
func TestALiveSuggestNeverAnswersTheKeystrokeChannel(t *testing.T) {
	called := 0
	f := Field{Name: "bucket", Type: String, Live: true,
		Suggest: func(context.Context, Request) []string {
			called++
			return []string{"backups"}
		}}
	if got := f.Candidates(context.Background(), NewRequest(nil, false, false)); got != nil {
		t.Errorf("Candidates = %v for a live field, want nothing offered", got)
	}
	if called != 0 {
		t.Errorf("the live Suggest ran %d times on the keystroke channel", called)
	}
	// The same field without the mark answers as any Suggest does — the gate
	// is the mark, not the function.
	f.Live = false
	if got := f.Candidates(context.Background(), NewRequest(nil, false, false)); len(got) != 1 {
		t.Errorf("Candidates = %v for the unmarked field, want the suggestion through", got)
	}
}

// LiveRequest carries what a run would, and CompletionRequest still does not.
//
// The pair is the whole design: same recipient, different cadence. A
// deliberate press hands the pinned binary the credentials it receives at
// Run moments later; a keystroke hands it nothing, because that delivery is
// early and unasked for — the incident this package's CompletionRequest
// comment records.
func TestLiveRequestCarriesWhatARunWould(t *testing.T) {
	c := Capability{ID: "s3.object.list", Summary: "list", Safety: Read,
		Inputs: []Field{
			{Name: "bucket", Type: String},
			{Name: "secret-key", Type: Secret, Local: true},
		},
	}
	values := map[string]any{"bucket": "backups", "secret-key": "sk-live-999"}

	live := LiveRequest(values)
	if got := live.String("secret-key"); got != "sk-live-999" {
		t.Errorf("secret-key = %q in a live request, want it carried whole — without it the "+
			"plugin cannot authenticate the listing", got)
	}
	if live.Surface() != SurfaceCompletion {
		t.Errorf("surface = %v, want SurfaceCompletion — a Suggest must be able to tell "+
			"this call from a run", live.Surface())
	}

	stripped := CompletionRequest(c, values)
	if got := stripped.String("secret-key"); got != "" {
		t.Errorf("secret-key = %q in a keystroke request, want it stripped", got)
	}
	if got := stripped.String("bucket"); got != "backups" {
		t.Errorf("bucket = %q in a keystroke request, want non-secrets kept", got)
	}
}
