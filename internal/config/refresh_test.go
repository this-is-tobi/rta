package config

import (
	"testing"
	"time"
)

func TestParseRefreshReadsOneSpellingAtEveryLevel(t *testing.T) {
	for text, want := range map[string]time.Duration{
		"":      0,
		"  ":    0,
		"30s":   30 * time.Second,
		"5m":    5 * time.Minute,
		"1h30m": 90 * time.Minute,
		"24h":   24 * time.Hour,
	} {
		got, err := ParseRefresh(text)
		if err != nil || got != want {
			t.Errorf("ParseRefresh(%q) = %v, %v; want %v", text, got, err, want)
		}
	}
	for _, text := range []string{"30", "soon", "-5s", "0s", "25h", "3600h"} {
		if _, err := ParseRefresh(text); err == nil {
			t.Errorf("ParseRefresh(%q) was accepted", text)
		}
	}
}

func TestFormatRefreshDropsTheZeroUnitsADurationPrintsWith(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:           "30s",
		5 * time.Minute:            "5m",
		90 * time.Second:           "1m30s",
		time.Hour:                  "1h",
		90 * time.Minute:           "1h30m",
		time.Hour + 30*time.Second: "1h0m30s",
	} {
		if got := FormatRefresh(d); got != want {
			t.Errorf("FormatRefresh(%v) = %q, want %q", d, got, want)
		}
	}
}
