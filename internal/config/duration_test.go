package config

import (
	"testing"
	"time"
)

// A day is `1d` in a profile's deadline and in a tile's pace as it is in a lock, a
// grant and a plugin's Duration input: there is one grammar (format.ParseDuration)
// and the schema's pattern is the one that reads it.
func TestAProfilesDeadlineIsWrittenWithTheSameUnitsAsEverywhereElse(t *testing.T) {
	for text, want := range map[string]time.Duration{"8h": 8 * time.Hour, "1d": 24 * time.Hour, "1d12h": 36 * time.Hour, "1w": 7 * 24 * time.Hour} {
		p := Profile{TTL: text}
		got, ok := p.Window()
		if !ok || got != want || p.BadTTL() {
			t.Errorf("ttl %q = %v, %v (bad: %v), want %v", text, got, ok, p.BadTTL(), want)
		}
	}
	for _, text := range []string{"30", "soon", "-2h"} {
		if !(Profile{TTL: text}).BadTTL() {
			t.Errorf("ttl %q was accepted", text)
		}
	}
}

func TestAPaceTakesADayAndIsWrittenBackAsOne(t *testing.T) {
	d, err := ParseRefresh("1d")
	if err != nil || d != 24*time.Hour {
		t.Fatalf("ParseRefresh(1d) = %v, %v", d, err)
	}
	if got := FormatRefresh(d); got != "1d" {
		t.Errorf("FormatRefresh(24h) = %q, want 1d", got)
	}
	if _, err := ParseRefresh("2d"); err == nil {
		t.Error("two days is more than a tile can wait and was accepted")
	}
}
