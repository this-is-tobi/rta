package mcp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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

// The ledger's copy of a padded record. The arguments it keeps are cleaned
// the way a model reads them, so a kv_get on "prod/db" and a zero-width
// space was recorded as key=prod/db, and agent log showed a call on the bare
// key, the one record it did not name. The records the call named are kept
// beside them, exactly, written to the file with the character escaped, and
// agent log shows them quoted with the character named, apart from the call
// on the bare key.
func TestTheLedgerKeepsTheRecordACallNamed(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := connectWith(t, reg, Options{})
	ctx := context.Background()
	zwsp := string(rune(0x200b))
	padded := "prod/db" + zwsp
	for _, key := range []string{"prod/db", padded} {
		if _, err := s.CallTool(ctx, &sdk.CallToolParams{Name: "kv_get", Arguments: map[string]any{"key": key}}); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := agentlog.Read(2)
	if err != nil || len(entries) != 2 {
		t.Fatalf("read %d entries: %v", len(entries), err)
	}
	if !slices.Equal(entries[0].Records, []string{"prod/db"}) || !slices.Equal(entries[1].Records, []string{padded}) {
		t.Errorf("records = %q and %q, want the bare key and the padded one", entries[0].Records, entries[1].Records)
	}
	raw, err := os.ReadFile(agentlog.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), zwsp) || !strings.Contains(string(raw), `"records":[`+strconv.QuoteToASCII(padded)+`]`) {
		t.Errorf("the ledger does not hold the record escaped:\n%s", raw)
	}
	if rep, err := agentlog.Verify(); err != nil || rep.Broken != 0 {
		t.Fatalf("the ledger does not verify: %v %+v", err, rep)
	}

	c, ok := reg.Capability("agent.log")
	if !ok {
		t.Fatal("no agent.log")
	}
	v, err := c.Run(ctx, plugin.NewRequest(nil, false, false).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	table, ok := v.(view.Table)
	if !ok {
		t.Fatalf("agent log is %T", v)
	}
	col := slices.IndexFunc(table.Columns, func(c view.Column) bool { return c.Name == "record" })
	if col < 0 || len(table.Rows) != 2 {
		t.Fatalf("agent log has no record column or not two rows: %+v", table)
	}
	if bare, odd := table.Rows[0][col], table.Rows[1][col]; bare != "prod/db" || odd != strconv.Quote(padded) {
		t.Errorf("record cells = %q and %q, want prod/db and %s", bare, odd, strconv.Quote(padded))
	}
}
