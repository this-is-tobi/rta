package plugin

import "testing"

// One sentence for how many capabilities need a grant, wherever one is counted:
// the install prompt, a search result, the detail page.
func TestGrantNeedLineIsTrueOfAPluginThatTakesAProfile(t *testing.T) {
	for _, c := range []struct {
		n            int
		takesProfile bool
		want         string
	}{
		{0, false, "none needs a grant"},
		{0, true, "none needs a grant until a profile names one"},
		{1, false, "1 needs a grant"},
		{1, true, "1 needs a grant"},
		{3, true, "3 need a grant"},
	} {
		if got := GrantNeedLine(c.n, c.takesProfile); got != c.want {
			t.Errorf("GrantNeedLine(%d, %v) = %q, want %q", c.n, c.takesProfile, got, c.want)
		}
	}
}
