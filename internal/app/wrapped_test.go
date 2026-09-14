package app

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The exit-code contract says a view.Error exits 1, and it said so through a
// direct type assertion — true only while nobody wrapped one with %w on its
// way up. A wrapped one exited 2 and was styled as a usage mistake.
func TestAWrappedViewErrorKeepsItsExitCodeAndRendering(t *testing.T) {
	inner := view.Errorf("demo.failed", "the thing broke").WithHint("try again")
	wrapped := fmt.Errorf("while doing something: %w", inner)
	if got := ExitCode(wrapped); got != 1 {
		t.Errorf("ExitCode(wrapped view.Error) = %d, want 1", got)
	}
	if got := ExitCode(fmt.Errorf("outer: %w", Rendered(inner))); got != 1 {
		t.Errorf("ExitCode(wrapped Rendered) = %d, want 1", got)
	}
	var buf bytes.Buffer
	if !RenderTopLevelError(&buf, NewRoot(testRegistry(t), "test"), wrapped) {
		t.Fatal("a wrapped view.Error was handed to fang instead of being rendered")
	}
	if !strings.Contains(buf.String(), "demo.failed") || !strings.Contains(buf.String(), "try again") {
		t.Errorf("rendered output lost the code or the hint:\n%s", buf.String())
	}
	if !RenderTopLevelError(&buf, nil, fmt.Errorf("outer: %w", Rendered(inner))) {
		t.Error("a wrapped, already-rendered error was rendered a second time")
	}
	if got := view.AsError(wrapped, "fallback"); got.Code != "demo.failed" {
		t.Errorf("view.AsError(wrapped) code = %q, want the inner code", got.Code)
	}
}
