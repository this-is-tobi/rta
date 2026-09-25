package codec

import (
	"fmt"
	"runtime"
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

// The walker built the path of every value as it went, and each level kept
// its own copy of every name above it: 410 KB of long names nested 4000 deep
// allocated 790 MB, and the 4 MiB an MCP request may carry asked for tens of
// gigabytes. Compared against the same names side by side, which cost what
// the input does, rather than against a number of bytes.
func TestNamesNestedDeepCostNoMoreThanNamesSideBySide(t *testing.T) {
	const depth, width = 4000, 100
	name := func(i int) string { return fmt.Sprintf(`"%0*d":`, width, i) }
	var nested, flat strings.Builder
	flat.WriteByte('{')
	for i := range depth {
		nested.WriteString("{" + name(i))
		if i > 0 {
			flat.WriteByte(',')
		}
		flat.WriteString(name(i) + "{}")
	}
	nested.WriteString("1" + strings.Repeat("}", depth))
	flat.WriteByte('}')
	allocated := func(doc string) uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if _, err := decodeObject([]byte(`{"a":` + doc + `}`)); err != nil {
			t.Fatal(err)
		}
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	if deep, wide := allocated(nested.String()), allocated(flat.String()); deep > 4*wide+16<<20 {
		t.Errorf("%d names nested allocated %d MB, against %d MB side by side", depth, deep>>20, wide>>20)
	}
	// A repeat is still placed, and one deep under long names is placed by
	// the end of its path, which is where somebody looks for it.
	deep := strings.Repeat(`{"segment":`, 300) + `{"x":1,"x":2}` + strings.Repeat("}", 300)
	obj, err := decodeObject([]byte(`{"a":` + deep + `}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(obj.dupes) != 1 || !strings.HasPrefix(obj.dupes[0], "…") || !strings.HasSuffix(obj.dupes[0], "segment.segment.x") ||
		len(obj.dupes[0]) > maxPath+len("….x") {
		t.Errorf("dupes = %q, want the one repeat placed by the end of its path", obj.dupes)
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
