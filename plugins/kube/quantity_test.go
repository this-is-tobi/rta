package main

import "testing"

func TestParseCPU(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"500m", 0.5, true},
		{"1", 1, true},
		{"2.5", 2.5, true},
		{"0m", 0, true},
		{"", 0, false},
		{"nope", 0, false},
		{"m", 0, false},
	}
	for _, c := range cases {
		got, ok := parseCPU(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseCPU(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestParseBytes(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"8Gi", 8 * 1 << 30, true},
		{"512Mi", 512 * 1 << 20, true},
		{"1Ki", 1 << 10, true},
		{"1000000000", 1_000_000_000, true},
		{"8G", 8_000_000_000, true},
		{"0", 0, true},
		{"", 0, false},
		{"nope", 0, false},
		{"-5Gi", 0, false},
	}
	for _, c := range cases {
		got, ok := parseBytes(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseBytes(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPercentOf(t *testing.T) {
	cases := []struct {
		used, hard float64
		want       string
	}{
		{2, 4, "50%"},
		{4, 4, "100%"},
		{0, 4, "0%"},
		{1, 0, ""},
		{1, -1, ""},
	}
	for _, c := range cases {
		if got := percentOf(c.used, c.hard); got != c.want {
			t.Errorf("percentOf(%v, %v) = %q, want %q", c.used, c.hard, got, c.want)
		}
	}
}
