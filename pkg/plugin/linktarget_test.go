package plugin

import "testing"

// A request no surface confined tells every link's target as it is, so a
// handler asks unconditionally; a confined one answers by the surface's rule,
// and keeps answering by it on a request a detail page composed from it.
func TestLinkTargetIsTheSurfacesToTell(t *testing.T) {
	plain := NewRequest(nil, false, false)
	if got := plain.LinkTarget("/root", "/outside/hop"); got != "/outside/hop" {
		t.Errorf("an unconfined request told %q", got)
	}
	confined := plain.WithLinkTargets(func(dir, target string) string {
		if target == "/outside/hop" {
			return "elsewhere"
		}
		return target
	})
	for _, r := range []Request{confined, confined.With(map[string]any{"path": "x"})} {
		if got := r.LinkTarget("/root", "/outside/hop"); got != "elsewhere" {
			t.Errorf("a confined request told %q, want the surface's phrase", got)
		}
		if got := r.LinkTarget("/root", "inside"); got != "inside" {
			t.Errorf("a confined request told %q for a target it allows", got)
		}
	}
}
