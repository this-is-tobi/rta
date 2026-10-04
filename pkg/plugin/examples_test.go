package plugin

import (
	"fmt"
	"strings"
	"testing"
)

// exampled is a capability with the inputs an example can go wrong against: a
// required positional, a bounded number, a closed set, a list, a credential
// and an input only the operator gives.
func exampled(examples ...Example) Plugin {
	return Plugin{
		Name: "demo", Summary: "demo plugin",
		Capabilities: []Capability{{
			ID: "demo.item.get", Summary: "get an item", Safety: Read, Run: noop,
			Inputs: []Field{
				{Name: "key", Type: String, Help: "the item", Required: true, Positional: true},
				{Name: "limit", Type: Int, Help: "how many", Default: 10, Min: 1, Max: 100},
				{Name: "mode", Type: String, Help: "how", Options: []string{"fast", "slow"}},
				{Name: "tags", Type: StringSlice, Help: "labels"},
				{Name: "token", Type: Secret, Help: "the token"},
				{Name: "host", Type: String, Help: "the server", Local: true},
			},
			Examples: examples,
		}},
	}
}

func TestAnExampleIsSpelledForTheSurfaceThatShowsIt(t *testing.T) {
	e := Example{Title: "two slow ones", Inputs: map[string]any{
		"tags": []string{"a", "b"}, "mode": "slow", "limit": 5, "key": "db",
	}}
	p := exampled(e)
	if err := p.Validate(); err != nil {
		t.Fatalf("a correct example was refused: %v", err)
	}
	c := p.Capabilities[0]
	for surface, want := range map[Surface]string{
		SurfaceCLI: "rta demo item get db --limit 5 --mode slow --tags a --tags b",
		SurfaceMCP: `demo_item_get {"key":"db","limit":5,"mode":"slow","tags":["a","b"]}`,
		SurfaceTUI: "demo.item.get key=db limit=5 mode=slow tags=a,b",
	} {
		if got := c.ExampleCall(surface, e); got != want {
			t.Errorf("on %q the example reads\n got %s\nwant %s", surface, got, want)
		}
	}
}

// An example is the declared text that names inputs, so it is the text that
// goes stale quietly: rename a flag and every help page still shows the old
// one. Each of these is a way that happens, refused where the author is.
func TestAnExampleThatCouldNotRunAsWrittenIsRefused(t *testing.T) {
	tests := []struct {
		name    string
		e       Example
		wantSub string
	}{
		{"no title", Example{Inputs: map[string]any{"key": "db"}}, "has no title"},
		{"a title over two lines", Example{Title: "a\nb", Inputs: map[string]any{"key": "db"}}, "one line"},
		{"an input that was renamed", Example{Title: "t", Inputs: map[string]any{"key": "db", "max": 5}},
			`gives input "max", which the capability does not declare`},
		{"an operator-only input", Example{Title: "t", Inputs: map[string]any{"key": "db", "host": "x"}}, "which is Local"},
		{"a credential", Example{Title: "t", Inputs: map[string]any{"key": "db", "token": "hunter2"}}, "in the clear"},
		{"a value of the wrong type", Example{Title: "t", Inputs: map[string]any{"key": "db", "limit": "five"}},
			"every call reading it is refused"},
		{"an option that does not exist", Example{Title: "t", Inputs: map[string]any{"key": "db", "mode": "quick"}},
			"not one of its options"},
		{"a value outside the range", Example{Title: "t", Inputs: map[string]any{"key": "db", "limit": 500}},
			"its range is from 1 to 100"},
		{"a required input left out", Example{Title: "t", Inputs: map[string]any{"limit": 5}},
			`leaves out input "key"`},
		{"a required input left empty", Example{Title: "t", Inputs: map[string]any{"key": ""}}, `leaves out input "key"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := exampled(tt.e).Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("want an error containing %q, got %v", tt.wantSub, err)
			}
		})
	}

	var many []Example
	for i := range maxExamples + 1 {
		many = append(many, Example{Title: fmt.Sprintf("call %d", i), Inputs: map[string]any{"key": "db"}})
	}
	if err := exampled(many...).Validate(); err == nil || !strings.Contains(err.Error(), "declares 5 examples") {
		t.Errorf("more examples than a help page teaches by were accepted: %v", err)
	}
}

// A required input only the operator can give leaves no call an example could
// be, and the refusal says so rather than asking for a value it would then
// refuse.
func TestAnExampleOfACapabilityNeedingALocalInputSaysItCannotBeWritten(t *testing.T) {
	p := exampled(Example{Title: "t", Inputs: map[string]any{"key": "db"}})
	p.Capabilities[0].Inputs[5].Required = true
	err := p.Validate()
	if err == nil || !strings.Contains(err.Error(), `"host" is Local, so no example can give it`) {
		t.Errorf("got %v", err)
	}
}
