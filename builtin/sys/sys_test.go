package sys

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatalf("sys plugin invalid: %v", err)
	}
}

func TestAllCapabilitiesAreReadIdempotent(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.Safety != plugin.Read || !c.Idempotent {
			t.Errorf("%s: sys capabilities must be read + idempotent", c.ID)
		}
	}
}

// TestCapabilitiesReturnWellFormedViews runs each capability against the real
// host and asserts view shape, not values (values are machine-dependent).
func TestCapabilitiesReturnWellFormedViews(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, c := range Plugin().Capabilities {
		t.Run(c.ID, func(t *testing.T) {
			req := plugin.NewRequest(map[string]any{"limit": 5}, false, false)
			v, err := c.Run(ctx, req)
			if err != nil {
				// Sensors are platform-dependent: a coded, hinted error is
				// the contract-correct outcome where none are readable.
				if c.ID == "sys.temp" {
					ve := view.AsError(err, "x")
					if ve.Hint == "" || ve.Code == "x" {
						t.Fatalf("sys.temp must fail coded+hinted, got %+v", ve)
					}
					t.Skipf("no sensors here: %v", ve.Message)
				}
				t.Fatalf("run: %v", err)
			}
			switch tv := v.(type) {
			case view.KeyValue:
				if len(tv.Pairs) == 0 {
					t.Error("empty KeyValue")
				}
				for _, p := range tv.Pairs {
					if p.Key == "" || p.Value == "" {
						t.Errorf("empty pair: %+v", p)
					}
				}
			case view.Table:
				if len(tv.Columns) == 0 {
					t.Error("table without columns")
				}
				for _, row := range tv.Rows {
					if len(row) != len(tv.Columns) {
						t.Errorf("row width %d != %d columns", len(row), len(tv.Columns))
					}
				}
			default:
				t.Errorf("unexpected view type %q", view.TypeOf(v))
			}
		})
	}
}

func TestCPUCoresChart(t *testing.T) {
	v, err := runCPU(context.Background(), plugin.NewRequest(map[string]any{"cores": true}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	chart, ok := v.(view.Chart)
	if !ok {
		t.Fatalf("want Chart, got %s", view.TypeOf(v))
	}
	if chart.Kind != view.ChartBar || len(chart.Series) == 0 {
		t.Errorf("chart = %+v", chart)
	}
	// The 0-100 scale is a property of the chart, not of each core: the cores
	// share one axis, so a per-series scale could only ever be a promise the
	// renderer had to break.
	if chart.Max != 100 {
		t.Errorf("per-core usage must be drawn on a fixed 0-100 scale, got Max=%v", chart.Max)
	}
	for _, s := range chart.Series {
		if len(s.Points) != 1 {
			t.Errorf("a bar series is one value: %+v", s)
		}
	}
}

func TestPSRespectsLimit(t *testing.T) {
	ctx := context.Background()
	v, err := runPS(ctx, plugin.NewRequest(map[string]any{"limit": 3}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) > 3 {
		t.Errorf("limit ignored: %d rows", len(tbl.Rows))
	}
	if tbl.Total < len(tbl.Rows) {
		t.Errorf("Total %d < rows %d", tbl.Total, len(tbl.Rows))
	}
}

func TestLoadIsNormalizedPerCore(t *testing.T) {
	v, err := runLoad(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.(view.KeyValue).Pairs {
		if !strings.Contains(p.Value, "/core") {
			t.Errorf("%s not normalized: %q", p.Key, p.Value)
		}
	}
}

func TestDiskFiltersNoiseAndHasStatus(t *testing.T) {
	v, err := runDisk(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	for _, row := range tbl.Rows {
		if pseudoFS[row[1]] || systemVolume(row[0]) {
			t.Errorf("noise row leaked through default filter: %v", row)
		}
	}
	if got := tbl.Columns[len(tbl.Columns)-1].Kind; got != view.KindStatus {
		t.Errorf("last column kind = %q, want status", got)
	}
	for _, row := range tbl.Rows {
		status := row[len(row)-1]
		if status != "ok" && !strings.HasPrefix(status, "WARN") && !strings.HasPrefix(status, "ERROR") {
			t.Errorf("unexpected status %q", status)
		}
	}
}

func TestUsageStatus(t *testing.T) {
	tests := []struct {
		pct  float64
		want string
	}{
		{10, "ok"}, {79.9, "ok"}, {80, "WARN >80%"}, {90, "ERROR >90%"},
	}
	for _, tt := range tests {
		if got := usageStatus(tt.pct); got != tt.want {
			t.Errorf("usageStatus(%v) = %q, want %q", tt.pct, got, tt.want)
		}
	}
}

func TestPSSortValidation(t *testing.T) {
	_, err := runPS(context.Background(), plugin.NewRequest(map[string]any{"limit": 3, "sort": "disk"}, false, false))
	ve := view.AsError(err, "x")
	if ve.Code != "sys.ps.badsort" || ve.Hint == "" {
		t.Errorf("want sys.ps.badsort with hint, got %+v", ve)
	}
	// Empty sort defaults to cpu (handlers may be called without CLI defaults).
	if _, err := runPS(context.Background(), plugin.NewRequest(map[string]any{"limit": 1}, false, false)); err != nil {
		t.Errorf("empty sort should default to cpu: %v", err)
	}
}

func TestPSHasRSSColumn(t *testing.T) {
	v, err := runPS(context.Background(), plugin.NewRequest(map[string]any{"limit": 3, "sort": "mem"}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	last := tbl.Columns[len(tbl.Columns)-1]
	if last.Name != "RSS" || last.Kind != view.KindBytes {
		t.Errorf("RSS column wrong: %+v", last)
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{90 * time.Minute, "1h 30m"},
		{25*time.Hour + 5*time.Minute, "1d 1h 5m"},
	}
	for _, tt := range tests {
		if got := humanDuration(tt.in); got != tt.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestOverviewGroupsSubsystems: the grouped view carries one dense line per
// subsystem; the essentials must be present on any host running the tests.
func TestOverviewGroupsSubsystems(t *testing.T) {
	v, err := runOverview(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	kv := v.(view.KeyValue)
	keys := map[string]string{}
	for _, p := range kv.Pairs {
		if _, dup := keys[p.Key]; dup {
			t.Errorf("duplicate overview line %q", p.Key)
		}
		keys[p.Key] = p.Value
	}
	for _, want := range []string{"host", "cpu", "mem", "disk"} {
		if keys[want] == "" {
			t.Errorf("overview missing %q line (got %v)", want, keys)
		}
	}
	if !strings.Contains(keys["mem"], " / ") {
		t.Errorf("mem line not used/total: %q", keys["mem"])
	}
	if !strings.Contains(keys["cpu"], "cores") {
		t.Errorf("cpu line missing core count: %q", keys["cpu"])
	}
}

func TestLoadVerdict(t *testing.T) {
	tests := []struct {
		perCore float64
		want    string
	}{
		{0.2, "ok"}, {0.69, "ok"}, {0.7, "busy"}, {0.99, "busy"}, {1.0, "overloaded"},
	}
	for _, tt := range tests {
		if got := loadVerdict(tt.perCore); got != tt.want {
			t.Errorf("loadVerdict(%v) = %q, want %q", tt.perCore, got, tt.want)
		}
	}
}

// TestDetailedOverviewComposesCapabilities: the full-page report is built
// from other capabilities' views, so it carries several view types at once.
func TestDetailedOverviewComposesCapabilities(t *testing.T) {
	v, err := runOverview(context.Background(), plugin.NewRequest(map[string]any{"detail": true}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("want Sections, got %s", view.TypeOf(v))
	}
	titles := map[string]string{}
	for _, item := range s.Items {
		if item.View == nil {
			t.Errorf("section %q has no view", item.Title)
			continue
		}
		titles[item.Title] = view.TypeOf(item.View)
	}
	// Composition reuses the capabilities' own view types.
	if titles["host"] != "keyvalue" {
		t.Errorf("host section = %q, want keyvalue", titles["host"])
	}
	if titles["cpu"] != "chart" {
		t.Errorf("cpu section = %q, want chart (per-core bars)", titles["cpu"])
	}
	if titles["storage"] != "table" {
		t.Errorf("storage section = %q, want table", titles["storage"])
	}
	if titles["top processes"] != "table" {
		t.Errorf("top processes section = %q, want table", titles["top processes"])
	}
	// The compact view stays a flat KeyValue for the tile.
	compact, err := runOverview(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := compact.(view.KeyValue); !ok {
		t.Errorf("compact overview = %s, want keyvalue", view.TypeOf(compact))
	}
}
