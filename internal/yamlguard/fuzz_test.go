package yamlguard

import "testing"

// A file rta reads before trusting it: the anchor check must answer the same
// way twice and never panic, whatever the bytes — it runs before the decoder
// that would otherwise expand a billion-laughs document.
func FuzzRefuseAnchors(f *testing.F) {
	for _, seed := range []string{
		"a: 1\n", "a: &x 1\nb: *x\n", "a: &x [*x]\n", "- &a [&b [&c [1]]]\n", "", "{", "\xff",
		"a: !!binary |\n  AAAA\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		first := RefuseAnchors(data)
		second := RefuseAnchors(data)
		if (first == nil) != (second == nil) {
			t.Fatalf("RefuseAnchors answered %v then %v for the same bytes", first, second)
		}
	})
}
