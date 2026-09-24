package codec

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// An object of distinct names each given twice cost time quadratic in their
// number, and a megabyte of it took seconds. Compared against an object of
// the same size with no repeats rather than against a clock, so a loaded
// machine slows both sides alike; the best of three runs each keeps one
// scheduling hiccup from deciding it.
func TestNamesGivenTwiceCostNoMoreThanNamesGivenOnce(t *testing.T) {
	const n = 20000
	var twice, once strings.Builder
	twice.WriteByte('{')
	once.WriteByte('{')
	for i := range n {
		if i > 0 {
			twice.WriteByte(',')
			once.WriteByte(',')
		}
		fmt.Fprintf(&twice, `"n%06d":1,"n%06d":1`, i, i)
		fmt.Fprintf(&once, `"a%06d":1,"b%06d":1`, i, i)
	}
	twice.WriteByte('}')
	once.WriteByte('}')
	best := func(doc string) time.Duration {
		fastest := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			if _, err := decodeObject([]byte(doc)); err != nil {
				t.Fatal(err)
			}
			fastest = min(fastest, time.Since(start))
		}
		return fastest
	}
	withRepeats, without := best(twice.String()), best(once.String())
	if withRepeats > 4*without+50*time.Millisecond {
		t.Errorf("%d repeated names took %v, against %v for as many distinct ones", n, withRepeats, without)
	}
	obj, err := decodeObject([]byte(twice.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(obj.dupes) != n || obj.dupes[0] != "n000000" {
		t.Errorf("dupes = %d names starting %q, want %d", len(obj.dupes), obj.dupes[0], n)
	}
}

// The walker recurses once per level, and a goroutine that runs out of stack
// takes the process with it rather than returning an error. Deeper than the
// bound encoding/json keeps is refused; an ordinary nesting reads, with an
// empty list and object shown as themselves rather than as null.
func TestNestingDeeperThanTheBoundIsRefusedRatherThanFollowed(t *testing.T) {
	deep := `{"a":` + strings.Repeat("[", maxDepth+10) + strings.Repeat("]", maxDepth+10) + `}`
	if _, err := decodeObject([]byte(deep)); err == nil || !strings.Contains(err.Error(), "levels deep") {
		t.Errorf("err = %v, want the depth named", err)
	}
	obj, err := decodeObject([]byte(`{"a":{"b":[[],{}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := compactJSON(obj.values["a"]); got != `{"b":[[],{}]}` {
		t.Errorf("a = %s", got)
	}
}
