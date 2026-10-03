package headerlist

import (
	"slices"
	"testing"
)

// The list a surface cuts at commas is put back together where a piece is not a
// header of its own: `--header 'Accept: text/html, application/json'` was
// refused for "invalid header" because ` application/json` has no colon.
func TestJoinRejoinsAValueThatHasCommas(t *testing.T) {
	for _, c := range []struct {
		name   string
		pieces []string
		want   []string
	}{
		{"a list value", []string{"Accept: text/html", " application/json"}, []string{"Accept: text/html, application/json"}},
		{"no space after the comma", []string{"Cache-Control: no-cache", "no-store"}, []string{"Cache-Control: no-cache,no-store"}},
		{"a continuation with a colon in it", []string{"Via: 1.1 a", " 1.1 b:8080"}, []string{"Via: 1.1 a, 1.1 b:8080"}},
		{"three pieces", []string{"Accept: a", " b", " c"}, []string{"Accept: a, b, c"}},
		{"two headers stay two", []string{"A: 1", "B: 2"}, []string{"A: 1", "B: 2"}},
		{"a header then a list value then a header", []string{"A: 1", "B: x", " y", "C: 3"}, []string{"A: 1", "B: x, y", "C: 3"}},
		{"an IPv6 address is a value, not a header named 2001",
			[]string{"X-Forwarded-For: 203.0.113.1", " 2001:db8::1", "fe80::1%eth0", "2001:db8::/32"},
			[]string{"X-Forwarded-For: 203.0.113.1, 2001:db8::1,fe80::1%eth0,2001:db8::/32"}},
		{"a header whose value is an address stays a header", []string{"A: 1", "X-Real-Ip: ::1"}, []string{"A: 1", "X-Real-Ip: ::1"}},
		{"nothing before it to join to", []string{"nonsense"}, []string{"nonsense"}},
		{"none", nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Join(c.pieces); !slices.Equal(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
