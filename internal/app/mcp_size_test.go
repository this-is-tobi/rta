package app

import (
	"encoding/json"
	"testing"

	"github.com/this-is-tobi/rta/internal/mcp"
)

// toolListCeiling is the most bytes the built-in catalogue's tools/list may
// weigh. Every session that lists the tools pays it, whether or not it calls
// one, so a tool added or reworded is a cost on every client, and the figure
// is pinned where a diff shows it rather than left to drift upwards a sentence
// at a time. It is the measured size plus two per cent, not a target: when a
// change legitimately needs more, raise it and say what the bytes bought.
//
// The list was 97,441 bytes over 96 tools before the titles, the grant
// sentence, the repeated property text and the longest descriptions were cut,
// and 80,216 after.
const toolListCeiling = 81_800

// toolListBytes is the size of the tools array a client receives, one JSON
// object per tool as the SDK writes it.
func toolListBytes(t *testing.T, opts mcp.Options) (bytes, tools int) {
	t.Helper()
	listed := listedTools(t, opts)
	for _, tl := range listed {
		raw, err := json.Marshal(tl)
		if err != nil {
			t.Fatal(err)
		}
		bytes += len(raw)
	}
	return bytes, len(listed)
}

func TestTheToolListStaysUnderItsBudget(t *testing.T) {
	bytes, tools := toolListBytes(t, mcp.Options{})
	if tools == 0 {
		t.Fatal("tools/list is empty, and a size under any ceiling says nothing")
	}
	t.Logf("tools/list carries %d tools in %d bytes", tools, bytes)
	if bytes > toolListCeiling {
		t.Errorf("tools/list is %d bytes over %d tools, over the %d it is held to: trim a description, or raise the ceiling and say what the bytes bought",
			bytes, tools, toolListCeiling)
	}
}
