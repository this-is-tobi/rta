//go:build unix

package main_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// maxInitAllocs is how many allocations one of rta's own packages may make
// before main runs. Every command pays for all of them: a package-level
// regexp with a {0,127} repetition is 1,400 allocations on its own, and
// plugindist alone was 4,400 of them — 0.6 ms of a start that is 22 ms in all
// — for two checks that `plugin index` and `plugin install` make. The biggest
// legitimate package today is under 1,200; what this refuses is the next
// expensive table or pattern landing in an init nobody reads, and the way out
// is a sync.OnceValue at its use, as with ociTagRe.
const maxInitAllocs = 2000

var initLine = regexp.MustCompile(`^init (\S+) @[\d.]+ ms, [\d.]+ ms clock, (\d+) bytes, (\d+) allocs$`)

func TestNoPackageOfOursDoesHeavyWorkBeforeMain(t *testing.T) {
	t.Setenv("GODEBUG", "inittrace=1")
	r := run(t, "--version")
	if r.code != 0 {
		t.Fatalf("--version exited %d: %s", r.code, r.stderr)
	}
	seen := 0
	for _, line := range strings.Split(r.stderr, "\n") {
		m := initLine.FindStringSubmatch(line)
		if m == nil || !strings.HasPrefix(m[1], "github.com/this-is-tobi/rta/") {
			continue
		}
		seen++
		if allocs, _ := strconv.Atoi(m[3]); allocs > maxInitAllocs {
			t.Errorf("%s allocates %d times at load, over %d: make what it builds at init lazy (sync.OnceValue)",
				m[1], allocs, maxInitAllocs)
		}
	}
	if seen == 0 {
		t.Fatalf("no init lines from our packages in %q: GODEBUG=inittrace=1 stopped reporting, or its format changed", r.stderr)
	}
}
