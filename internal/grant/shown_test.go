package grant

import (
	"strconv"
	"testing"
)

// A grant is named as the gate compares its record: the bare record as it
// is, one that does not read as itself quoted, and none at all as the
// target alone.
func TestNamedShowsTheRecordAsCompared(t *testing.T) {
	padded := "db" + string(rune(0xa0))
	for _, c := range []struct {
		g    Grant
		want string
	}{
		{Grant{Target: "kv.get"}, "kv.get"},
		{Grant{Target: "kv.get", Scope: "db"}, "kv.get db"},
		{Grant{Target: "kv.get", Scope: padded}, "kv.get " + strconv.QuoteToASCII(padded)},
		{Grant{Target: "kv.get", Scope: " db"}, `kv.get " db"`},
	} {
		if got := c.g.Named(); got != c.want {
			t.Errorf("Named(%q) = %q, want %q", c.g.Scope, got, c.want)
		}
	}
}
