package mcp

import (
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/grant"
)

// A call that spends a grant, writes or destroys is not run when it cannot be
// recorded: a granted write that ran with no trace was the gap the record
// exists to close, and the one place it showed was a count on the next row
// that did get written. The refusal spends no use of the grant, so the call
// is the same call once the record can be written again, and a free read —
// which spends nothing, and without which the full disk could not be found —
// still runs.
func TestAGrantedCallIsNotRunWhenTheRecordCannotBeWritten(t *testing.T) {
	s := connect(t, Options{})
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.set", Issued: time.Now(), Expires: time.Now().Add(time.Hour), MaxUses: 1,
	}, true); verr != nil {
		t.Fatal(verr)
	}
	// A directory where the record goes: nothing can be appended to it, on any
	// platform and for any user.
	if err := os.Mkdir(agentlog.Path(), 0o700); err != nil {
		t.Fatal(err)
	}

	res := callTool(t, s, "demo_item_set", map[string]any{})
	if !res.IsError {
		t.Fatal("a granted write ran with no way to record it")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "core.record.unwritable") || !strings.Contains(text, "rta doctor") {
		t.Errorf("the refusal neither names the cause nor whose it is to fix: %s", text)
	}
	if strings.Contains(text, agentlog.Path()) {
		t.Errorf("the refusal tells the agent where the operator's record is: %s", text)
	}
	if read := callTool(t, s, "demo_item_list", map[string]any{"name": "x"}); read.IsError {
		t.Errorf("a free read was refused because the record could not be written: %s",
			read.Content[0].(*sdk.TextContent).Text)
	}

	if err := os.Remove(agentlog.Path()); err != nil {
		t.Fatal(err)
	}
	if res := callTool(t, s, "demo_item_set", map[string]any{}); res.IsError {
		t.Fatalf("the refusal spent the grant's one use: %s", res.Content[0].(*sdk.TextContent).Text)
	}
}
