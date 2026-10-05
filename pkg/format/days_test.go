package format

import (
	"testing"
	"time"
)

func TestParseWindowReadsDaysAndEverythingGoReads(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"1d":      24 * time.Hour,
		"7d":      7 * 24 * time.Hour,
		"0.5d":    12 * time.Hour,
		"1d12h":   36 * time.Hour,
		"2d30m":   48*time.Hour + 30*time.Minute,
		" 3d ":    72 * time.Hour,
		"90m":     90 * time.Minute,
		"1h30m":   90 * time.Minute,
		"30s":     30 * time.Second,
		"-2h":     -2 * time.Hour,
		"0":       0,
		"1h0m30s": time.Hour + 30*time.Second,
	} {
		got, err := ParseWindow(raw)
		if err != nil || got != want {
			t.Errorf("ParseWindow(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
}

func TestParseWindowRefusesWhatIsNoWindow(t *testing.T) {
	for _, raw := range []string{"", "d", "1dd", "1d-2h", "12h1d", "1.2.3d", "d1", "tomorrow", "1x", "99999999999d", "106751d25h"} {
		if got, err := ParseWindow(raw); err == nil {
			t.Errorf("ParseWindow(%q) = %v, want an error", raw, got)
		}
	}
}
