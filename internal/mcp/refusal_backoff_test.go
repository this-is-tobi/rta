package mcp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A refused call is a row, and rows are what retention counts. Past the
// free failures a caller's refusals are answered slower, doubling, so a
// loop on a tool that refuses it cannot churn real history out of the
// record in minutes. Small steps here; the constants are the policy.
func TestRepeatedRefusalsAreAnsweredSlower(t *testing.T) {
	session := connect(t, Options{refusals: newBackoff(2, time.Minute, 40*time.Millisecond, time.Second)})
	ctx := context.Background()
	refuse := func() time.Duration {
		start := time.Now()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "demo_item_set", Arguments: map[string]any{"name": "x", "value": "y"}})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatal("a write with no grant was allowed")
		}
		return time.Since(start)
	}
	refuse()
	refuse()
	if d := refuse(); d < 40*time.Millisecond {
		t.Fatalf("third refusal answered in %s, want the first step", d)
	}
	if d := refuse(); d < 80*time.Millisecond {
		t.Fatalf("fourth refusal answered in %s, want the step doubled", d)
	}
}

// A call to a tool the server does not have is the cheapest refusal there
// is, and it is charged like the others.
func TestUnknownToolsCountAsRefusals(t *testing.T) {
	session := connect(t, Options{refusals: newBackoff(1, time.Minute, 40*time.Millisecond, time.Second)})
	ctx := context.Background()
	for i := 0; i < 1; i++ {
		_, _ = session.CallTool(ctx, &sdk.CallToolParams{Name: "no_such_tool"})
	}
	start := time.Now()
	_, _ = session.CallTool(ctx, &sdk.CallToolParams{Name: "no_such_tool"})
	if d := time.Since(start); d < 40*time.Millisecond {
		t.Fatalf("second unknown-tool call answered in %s, want the first step", d)
	}
}

// A rejected bearer is answered slower from the same address after the free
// failures, with the same generic answer. Real constants, so the sixth
// attempt is the first slow one.
func TestARejectedBearerIsAnsweredSlowerFromTheSameAddress(t *testing.T) {
	addr := startRemote(t, Options{}, StaticTokenVerifier(map[string]string{"tok-a-0123456789abcdef": "alice"}))
	attempt := func() (int, time.Duration) {
		start := time.Now()
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer tok-wrong-0123456789abcdef")
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res.StatusCode, time.Since(start)
	}
	for i := 0; i < bearerFree; i++ {
		if code, _ := attempt(); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d", i+1, code)
		}
	}
	code, d := attempt()
	if code != http.StatusUnauthorized {
		t.Fatalf("slowed attempt: status %d, want the same refusal", code)
	}
	if d < bearerStep {
		t.Fatalf("attempt %d answered in %s, want at least %s", bearerFree+1, d, bearerStep)
	}
}
