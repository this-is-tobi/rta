package plugin

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// keyed is a plugin "db" whose capabilities read the inputs given, each by
// the ID it is keyed under.
func keyed(renamed map[string]string, inputs map[string][]Field) Plugin {
	p := Plugin{Name: "db", Summary: "db", Renamed: renamed}
	for _, id := range slices.Sorted(maps.Keys(inputs)) {
		fields := inputs[id]
		p.Capabilities = append(p.Capabilities, Capability{ID: id, Summary: "s", Safety: Read, Inputs: fields,
			Run: func(context.Context, Request) (view.View, error) { return view.Text{Body: "ok"}, nil }})
	}
	return p
}

// A key read by several capabilities means one thing to each, a list of one
// kind counting as that kind; a dotted key is one capability's own; and a
// renamed key is one nothing reads, read now as another that something does.
func TestAConfigKeyMeansOneThingToEveryCapabilityThatReadsIt(t *testing.T) {
	limit := func(def, max int) Field {
		return Field{Name: "limit", Type: Int, Config: "limit", Default: def, Min: 1, Max: max}
	}
	for _, tc := range []struct {
		name string
		p    Plugin
		says string
	}{
		{"one meaning", keyed(nil, map[string][]Field{
			"db.table.list": {limit(50, 1000)}, "db.database.list": {limit(50, 1000)},
			"db.query":  {{Name: "namespace", Type: String, Config: "namespace"}},
			"db.status": {{Name: "namespace", Type: StringSlice, Config: "namespace"}},
			"db.dump":   {{Name: "format", Type: String, Config: "dump.format", Options: []string{"sql", "csv"}}},
		}), ""},
		{"two defaults", keyed(nil, map[string][]Field{
			"db.table.list": {limit(50, 1000)}, "db.query": {limit(200, 1000)},
		}), `config key "limit" is read by`},
		{"two bounds", keyed(nil, map[string][]Field{
			"db.table.list": {limit(50, 1000)}, "db.query": {limit(50, 10000)},
		}), "bounds 1 to 10000 and 1 to 1000"},
		{"two kinds", keyed(nil, map[string][]Field{
			"db.status": {{Name: "wait", Type: Int, Config: "wait"}},
			"db.query":  {{Name: "wait", Type: Duration, Config: "wait"}},
		}), "types duration and int"},
		{"two sets of options", keyed(nil, map[string][]Field{
			"db.status": {{Name: "tls", Type: String, Config: "tls", Options: []string{"on", "off"}}},
			"db.query":  {{Name: "tls", Type: String, Config: "tls", Options: []string{"on", "off", "auto"}}},
		}), "options"},
		{"a dotted key of another capability's", keyed(nil, map[string][]Field{
			"db.products": {{Name: "limit", Type: Int, Config: "catalogue.limit"}},
		}), `"products.limit", or "limit"`},
		{"a renamed key still read", keyed(map[string]string{"sslmode": "tls"}, map[string][]Field{
			"db.status": {{Name: "tls", Type: String, Config: "tls"}, {Name: "sslmode", Type: String, Config: "sslmode"}},
		}), `renamed key "sslmode" is still read by db.status`},
		{"a key renamed to nothing", keyed(map[string]string{"sslmode": "tls"}, map[string][]Field{
			"db.status": {{Name: "mode", Type: String, Config: "mode"}},
		}), `which no input declares`},
	} {
		err := tc.p.ValidateKeys()
		switch {
		case tc.says == "" && err != nil:
			t.Errorf("%s: refused: %v", tc.name, err)
		case tc.says != "" && (err == nil || !strings.Contains(err.Error(), tc.says)):
			t.Errorf("%s: %v, want it to say %q", tc.name, err, tc.says)
		}
	}
}
