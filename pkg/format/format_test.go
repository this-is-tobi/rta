package format

import (
	"testing"
	"time"
)

func TestBytes(t *testing.T) {
	tests := []struct {
		in   uint64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	}
	for _, tt := range tests {
		if got := Bytes(tt.in); got != tt.want {
			t.Errorf("Bytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Ago answers "is this recent?" in one unit, which is the only question a
// timestamp on a dashboard is read for.
func TestAgo(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(-200 * time.Millisecond), "just now"},
		{now.Add(-12 * time.Second), "12 seconds ago"},
		{now.Add(-time.Minute), "1 minute ago"},
		{now.Add(-90 * time.Second), "2 minutes ago"}, // rounded within the unit
		{now.Add(-59 * time.Minute), "59 minutes ago"},
		{now.Add(-3 * time.Hour), "3 hours ago"},
		{now.Add(-50 * time.Hour), "2 days ago"},
		{now.Add(-13 * 24 * time.Hour), "2 weeks ago"},
		{now.Add(-800 * 24 * time.Hour), "2 years ago"},
		{now.Add(3 * time.Minute), "in 3 minutes"},
		{time.Time{}, "never"},
	} {
		got := Ago(c.at)
		if got != c.want {
			t.Errorf("Ago(%v) = %q, want %q", c.at, got, c.want)
		}
	}
}
