package mcp

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A padded record, end to end. An agent's kv_get on "prod/db" with a
// no-break space after it is its own record to the gate, so it parks beside
// the call on the bare key rather than riding its grant — and the queue the
// operator answers from showed the two alike, in every format a person
// reads. It shows the padded one quoted, the no-break space named, and the
// bare one as it is.
func TestAPaddedRecordIsParkedAndQueuedApartFromTheBareOne(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := connectWith(t, reg, Options{Consent: true, ConsentWait: 20 * time.Second})
	ctx := context.Background()
	padded := "prod/db" + string(rune(0xa0))
	answered := make(chan *sdk.CallToolResult, 2)
	for _, key := range []string{"prod/db", padded} {
		go func() {
			res, err := s.CallTool(ctx, &sdk.CallToolParams{Name: "kv_get", Arguments: map[string]any{"key": key}})
			if err != nil {
				t.Errorf("kv_get %q: %v", key, err)
			}
			answered <- res
		}()
	}

	var parked []consent.Request
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline) && len(parked) < 2; {
		parked, _ = consent.Pending()
		time.Sleep(10 * time.Millisecond)
	}
	if len(parked) != 2 {
		t.Fatalf("%d calls parked, want the bare and the padded one apart", len(parked))
	}
	defer func() {
		for _, r := range parked {
			if err := consent.Decide(r.ID, false, "test"); err != nil {
				t.Error(err)
			}
		}
		for range parked {
			<-answered
		}
	}()
	idOf := map[string]string{}
	for _, r := range parked {
		idOf[strings.Join(r.Scopes, "|")] = r.ID
	}
	if idOf["prod/db"] == "" || idOf[padded] == "" {
		t.Fatalf("parked %v, want one call on each record", idOf)
	}

	c, ok := reg.Capability("agent.pending")
	if !ok {
		t.Fatal("no agent.pending")
	}
	v, err := c.Run(ctx, plugin.NewRequest(nil, false, false).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []cli.Format{cli.Pretty, cli.Markdown, cli.CSV} {
		var out bytes.Buffer
		if err := cli.Render(&out, v, cli.Options{Format: format}); err != nil {
			t.Fatal(err)
		}
		row := func(id string) string {
			for _, line := range strings.Split(out.String(), "\n") {
				if strings.Contains(line, id) {
					return line
				}
			}
			t.Fatalf("-o %s: no row for request %s:\n%s", format, id, out.String())
			return ""
		}
		bare, odd := row(idOf["prod/db"]), row(idOf[padded])
		if !strings.Contains(bare, "prod/db") || strings.Contains(bare, `"prod/db`) {
			t.Errorf("-o %s: the bare record's row reads %q", format, bare)
		}
		// Quoted as Go quotes it, which names the no-break space by its
		// code point; csv doubles the quotes of a field that holds them,
		// and markdown escapes the backslash so it renders as one.
		want := strconv.Quote(padded)
		switch format {
		case cli.CSV:
			want = strings.ReplaceAll(want, `"`, `""`)
		case cli.Markdown:
			want = strings.ReplaceAll(want, `\`, `\\`)
		}
		if !strings.Contains(odd, want) {
			t.Errorf("-o %s: the padded record's row reads %q, want it to show %s", format, odd, want)
		}
	}
}
