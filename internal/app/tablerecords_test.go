package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	yaml "github.com/goccy/go-yaml"
)

// A built-in table's records are its rows read by the names its columns carry,
// through the real command line and not a renderer handed a table: one record
// to a row, and its keys the columns[].name the same answer lists, in json and
// in yaml alike.
func TestAListingsRecordsAreItsRowsByColumnName(t *testing.T) {
	const config = "profiles:\n  staging:\n    note: the staging environment\n  prod:\n    note: production\n"
	for _, c := range []struct {
		format string
		decode func([]byte, any) error
	}{
		{"json", json.Unmarshal},
		{"yaml", yaml.Unmarshal},
	} {
		out, stderr, err := runWith(t, connRegistry(t), config, "profile", "list", "-o", c.format)
		if err != nil {
			t.Fatalf("profile list -o %s: %v\n%s", c.format, err, stderr)
		}
		var table struct {
			Columns []struct {
				Name string `json:"name" yaml:"name"`
			} `json:"columns" yaml:"columns"`
			Rows    [][]string       `json:"rows" yaml:"rows"`
			Records []map[string]any `json:"records" yaml:"records"`
		}
		if err := c.decode([]byte(out), &table); err != nil {
			t.Fatalf("-o %s does not parse: %v\n%s", c.format, err, out)
		}
		if len(table.Rows) != 2 || len(table.Records) != len(table.Rows) {
			t.Fatalf("-o %s: %d rows and %d records:\n%s", c.format, len(table.Rows), len(table.Records), out)
		}
		var names []string
		for _, column := range table.Columns {
			names = append(names, column.Name)
		}
		slices.Sort(names)
		for i, record := range table.Records {
			keys := make([]string, 0, len(record))
			for k := range record {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			if !slices.Equal(keys, names) {
				t.Errorf("-o %s: record %d has the keys %q, the columns are %q", c.format, i, keys, names)
			}
			if record["Profile"] != table.Rows[i][0] {
				t.Errorf("-o %s: record %d is %q, its row %q", c.format, i, record["Profile"], table.Rows[i][0])
			}
		}
	}
}

// …and the other formats of the same answer are the table they were.
func TestAListingInTheOtherFormatsHasNoRecords(t *testing.T) {
	for _, format := range []string{"csv", "md", "pretty"} {
		out, stderr, err := runWith(t, connRegistry(t), "profiles:\n  staging: {}\n", "profile", "list", "-o", format)
		if err != nil || !strings.Contains(out, "staging") {
			t.Fatalf("-o %s = %q, %v\n%s", format, out, err, stderr)
		}
		if strings.Contains(out, "records") {
			t.Errorf("-o %s mentions records:\n%s", format, out)
		}
	}
}
