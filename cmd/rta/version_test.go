package main

import "testing"

func TestVersionFrom(t *testing.T) {
	cases := []struct {
		name, stamp, module, want string
	}{
		{"stamped release wins", "0.23.0", "v0.23.0", "0.23.0"},
		{"stamped describe wins", "v0.23.0-2-g85eb672", "(devel)", "v0.23.0-2-g85eb672"},
		// Without the v, so it compares equal to what GoReleaser stamps into
		// the archive of the same tag.
		{"go install of a tag", "dev", "v0.23.0", "0.23.0"},
		{"go install of a branch keeps the pseudo-version", "dev", "v0.23.1-0.20260920101010-85eb672c1d2e", "0.23.1-0.20260920101010-85eb672c1d2e"},
		{"checkout build stays dev", "dev", "(devel)", "dev"},
		{"no build info stays dev", "dev", "", "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := versionFrom(c.stamp, c.module); got != c.want {
				t.Errorf("versionFrom(%q, %q) = %q, want %q", c.stamp, c.module, got, c.want)
			}
		})
	}
}
