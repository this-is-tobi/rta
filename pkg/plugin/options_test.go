package plugin

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

func optionCap(run Handler) Capability {
	return Capability{
		ID: "demo.token", Summary: "demo", Safety: Read,
		Inputs: []Field{
			{Name: "encoding", Type: String, Default: "hex", Options: []string{"hex", "base64"}},
			{Name: "tags", Type: StringSlice, Options: []string{"red", "blue"}},
			{Name: "free", Type: String},
		},
		Run: run,
	}
}

// A closed set is held to: a value naming none of its options is refused
// before the handler sees it, naming the input and the set.
func TestAValueOutsideItsOptionsIsRefused(t *testing.T) {
	ran := false
	guarded := GuardOptions(optionCap(func(context.Context, Request) (view.View, error) {
		ran = true
		return nil, nil
	}))
	for _, values := range []map[string]any{
		{"encoding": "b64"},
		{"tags": []string{"red", "green"}},
	} {
		_, err := guarded(context.Background(), NewRequest(values, false, false))
		verr := view.AsError(err, "test")
		if err == nil || verr.Code != "core.input.option" {
			t.Errorf("%v: err = %v, want core.input.option", values, err)
			continue
		}
		if !strings.Contains(verr.Message, "demo.token takes one of") {
			t.Errorf("message = %q", verr.Message)
		}
	}
	if ran {
		t.Error("the handler ran on a refused value")
	}
	// An empty value is not a choice, and a field without Options is not
	// checked at all.
	if _, err := guarded(context.Background(), NewRequest(map[string]any{"encoding": "", "free": "anything"}, false, false)); err != nil {
		t.Errorf("empty and free-form values were refused: %v", err)
	}
	if !ran {
		t.Error("the handler did not run on valid values")
	}
}

// Resolve rewrites an option typed in another case to the declared spelling,
// so a handler sees one spelling and `--encoding HEX` is not refused.
func TestResolveSpellsAnOptionTheWayItIsDeclared(t *testing.T) {
	out := Resolve(optionCap(nil), Inputs{Caller: map[string]any{
		"encoding": "BASE64", "tags": []string{"Red", "BLUE"}, "free": "KeepMe",
	}})
	if out["encoding"] != "base64" {
		t.Errorf("encoding = %v", out["encoding"])
	}
	if tags, _ := out["tags"].([]string); strings.Join(tags, ",") != "red,blue" {
		t.Errorf("tags = %v", out["tags"])
	}
	if out["free"] != "KeepMe" {
		t.Errorf("a free-form value was rewritten: %v", out["free"])
	}
}

func TestGuardOptionsLeavesAnUnguardedHandlerAlone(t *testing.T) {
	if GuardOptions(Capability{ID: "demo.none"}) != nil {
		t.Error("a nil handler became non-nil")
	}
}

// A default outside its own set would be refused on every call that leaves
// the input alone, so the declaration is refused instead, where its author
// sees it.
func TestADefaultOutsideItsOptionsFailsValidation(t *testing.T) {
	p := validPlugin()
	p.Capabilities[0].Inputs = []Field{{Name: "mode", Type: String, Default: "fast", Options: []string{"slow", "safe"}}}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "not one of its options") {
		t.Errorf("err = %v, want the default refused", err)
	}
}
