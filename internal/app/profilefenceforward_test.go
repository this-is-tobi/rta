package app

import (
	"strings"
	"testing"
)

// A forward fills the address, so a list on the address holds nothing: it is
// removed, as a `set:` on it is. But a `set:` removed there was a default
// nothing read, and a fence removed there was a boundary, and the receipt for
// the first, "nothing read them", says the second cost nothing. It goes where a
// fence taken off by hand goes, in the words for a boundary.
func TestAForwardThatRemovesAFenceSaysAFenceWasRemoved(t *testing.T) {
	const before = `profiles:
  staging:
    plugins:
      db:
        set:
          port: 6432
        only:
          host: [old.internal]
`
	out, errOut, err := runWith(t, setRegistry(t), before,
		"profile", "set", "staging", "--plugin", "db", "--kube", "prod/db/svc/postgres:5432")
	if err != nil {
		t.Fatalf("adding a forward was refused over a fence it replaces: %v %q", err, errOut)
	}
	if strings.Contains(configOf(t), "old.internal") {
		t.Errorf("the fence on an address the forward fills survived:\n%s", configOf(t))
	}
	var removed, unfenced string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "removed"):
			removed = line
		case strings.Contains(line, "unfenced"):
			unfenced = line
		}
	}
	if !strings.Contains(unfenced, "only.host") {
		t.Errorf("the fence's removal is not said as one:\n%s", out)
	}
	if strings.Contains(removed, "only.host") {
		t.Errorf("a fence is reported as a value nothing read: %q", removed)
	}
	if !strings.Contains(removed, "set.port") {
		t.Errorf("the value the forward fills lost its receipt: %q\n%s", removed, out)
	}
}
