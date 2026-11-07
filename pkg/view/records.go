package view

import "strconv"

// addRecords adds a "records" array to the encoded table m, and to every table
// inside it if m is a page: one object per row, keyed by the column's name, so
// a script reads `.records[] | select(.Size > 1e9)` where `rows` and `values`
// made it count to the sixth cell.
//
// It reads the encoded map and not the Table, which is how one helper serves a
// table on its own and one at any depth of a page. Section.MarshalJSON has
// already encoded every nested view as a map of its own, values and all, and
// the Table it came from is gone by then; walking those maps is the only way to
// reach them without encoding each a second time. It is also why a record can
// carry nothing Redact removed: it holds only what columns, rows and values
// hold, and those are the redacted view's.
//
// Every assertion is the comma-ok form and a view that is not what it should be
// is skipped, not trusted. A nil section view encodes as {"type":"unknown"} and
// is the case that matters, since the encoder this runs in has taken the
// process down before on exactly that; the rest is the same care for a map
// that nothing else checks.
func addRecords(m map[string]any) {
	switch kind, _ := m["type"].(string); kind {
	case "table":
		m["records"] = tableRecords(m)
	case "sections":
		items, _ := m["items"].([]any)
		for _, it := range items {
			item, _ := it.(map[string]any)
			if child, ok := item["view"].(map[string]any); ok {
				addRecords(child)
			}
		}
	}
}

// tableRecords is the records of the encoded table m, an empty array and never
// nil for a table with no rows: `.records[]` yields nothing and exits 0 on the
// day a list is empty, where null would fail, as `rows` and `values` already
// guarantee.
//
// A cell is what a program can compare, and what a person reads where there is
// nothing to compare. A column of a numeric kind or a timestamp holds its value
// — the number, with every digit it was given, or the instant — and null where
// the cell has none, never its text. The text is for a person (`n/a`, `never`),
// and jq orders a string above every number, so a text in a column of numbers
// would make `select(.["Use%"] > 90)` match the very rows that have no figure.
// Every other column, a status included, holds the cell's text: a record is the
// row as the table shows it, so `Running` is what the cell says and not the
// word `values` grades it by, and the text of a status says more than its word.
//
// A cell the row does not reach is null, as it is in `values`; a cell past the
// last column has no name to be keyed by and is not here (Redact already cut it
// from every row that reaches an encoder).
func tableRecords(m map[string]any) []any {
	columns, _ := m["columns"].([]any)
	names := make([]string, len(columns))
	valued := make([]bool, len(columns))
	for i, c := range columns {
		column, _ := c.(map[string]any)
		names[i], _ = column["name"].(string)
		kind, _ := column["kind"].(string)
		valued[i] = ColumnKind(kind).numeric() || ColumnKind(kind) == KindTimestamp
	}
	keys := recordKeys(names)

	rows, _ := m["rows"].([]any)
	records := make([]any, len(rows))
	for i, r := range rows {
		cells, _ := r.([]any)
		record := make(map[string]any, len(keys))
		for j, key := range keys {
			switch {
			case valued[j]:
				record[key] = valueAt(m["values"], i, j)
			case j < len(cells):
				if text, ok := cells[j].(string); ok {
					record[key] = text
				} else {
					record[key] = nil
				}
			default:
				record[key] = nil
			}
		}
		records[i] = record
	}
	return records
}

// valueAt is the value at row i and column j of a table's encoded values, or
// nil where there is none. The same field is two types by the time it gets here:
// the envelope that is being encoded set its own as [][]any, and one a page's
// section encoded earlier arrived as the []any of []any that decoding gives.
func valueAt(values any, i, j int) any {
	switch rows := values.(type) {
	case [][]any:
		if i < len(rows) && j < len(rows[i]) {
			return rows[i][j]
		}
	case []any:
		if i < len(rows) {
			if row, ok := rows[i].([]any); ok && j < len(row) {
				return row[j]
			}
		}
	}
	return nil
}

// recordKeys names each column's field in a record: the column's name as
// columns[].name spells it, so a script that reads one meets the spelling of
// the other.
//
// **Verbatim, because there is no machine key and making one is a second
// naming layer.** A column carries a name, a kind and a priority and nothing a
// program could address it by; adding a key would touch the plugin protocol, the
// SDK and every table. Folding "CPU %" to cpu_percent instead collides with a
// column called that, mangles `°C`, `?column?` and a SQL alias, and renames the
// fields `agent log` is read by, a log shipper's among them. The cost is a
// quoted key, `.["CPU %"]`.
//
// Nothing makes names unique, though: pg.query takes them from the result set,
// so `select a.id, b.id` has two, and a table can have a column with none. A
// record with two fields of one name has one, and the other cell is silently
// gone. So the first column called a name keeps it, and each later one takes
// the first `_2`, `_3`, ... that no column is called by and no earlier one took
// (id, id, id_2 is id, id_3, id_2). Every name is reserved before any suffix is
// chosen, so a suffix never lands on a column's own name and moves it. A blank
// name is the key "", and a second blank is "_2" by the same rule. A table with
// such a column is not left without records, which would make `.records[]` die
// on the one query that most wants it.
func recordKeys(names []string) []string {
	reserved := make(map[string]bool, len(names))
	for _, n := range names {
		reserved[n] = true
	}
	taken := make(map[string]bool, len(names))
	keys := make([]string, len(names))
	for i, n := range names {
		key := n
		for k := 2; taken[key]; k++ {
			if candidate := n + "_" + strconv.Itoa(k); !reserved[candidate] {
				key = candidate
			}
		}
		taken[key] = true
		keys[i] = key
	}
	return keys
}
