package main

import (
	"strconv"
	"strings"
)

// Kubernetes resource quantities ("500m", "8Gi", "45m" for CPU millicores),
// parsed by hand rather than by importing k8s.io/apimachinery's
// resource.Quantity.
//
// That package is small on its own, but it is still a k8s.io/* import, and
// this plugin's one dependency decision (kubectl.go) already turned down
// client-go for the same reason at a much larger scale: "the number is worse
// here, not better" for a plugin that shells out instead of linking a
// client. A parser covering the suffixes that actually appear in a
// ResourceQuota, LimitRange or PVC spec — decimal SI and binary — is a few
// lines and pulls in nothing.

// parseCPU reads a CPU quantity in cores. "500m" is 0.5 cores; a bare
// number is already cores, never millicores — that distinction is the
// suffix, not the magnitude.
func parseCPU(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if milli, ok := strings.CutSuffix(s, "m"); ok {
		n, err := strconv.ParseFloat(milli, 64)
		if err != nil {
			return 0, false
		}
		return n / 1000, true
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// binarySuffixes and decimalSuffixes are Kubernetes' own quantity suffixes
// for byte-valued resources (memory, storage). Binary is what every real
// manifest uses ("8Gi"); decimal ("8G") is valid and rare, kept for
// correctness rather than because it is expected to fire.
var binarySuffixes = map[string]float64{
	"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30,
	"Ti": 1 << 40, "Pi": 1 << 50, "Ei": 1 << 60,
}

var decimalSuffixes = map[string]float64{
	"k": 1e3, "M": 1e6, "G": 1e9, "T": 1e12, "P": 1e15, "E": 1e18,
}

// parseBytes reads a byte-valued quantity ("8Gi", "512Mi", "1000000000",
// "8G") as a plain byte count.
func parseBytes(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// Longest suffix first: "Ki" must not match on the trailing "i" of a
	// two-character binary suffix as if it were the single-character decimal
	// one.
	for suffix, mult := range binarySuffixes {
		if rest, ok := strings.CutSuffix(s, suffix); ok {
			return scaledBytes(rest, mult)
		}
	}
	for suffix, mult := range decimalSuffixes {
		if rest, ok := strings.CutSuffix(s, suffix); ok {
			return scaledBytes(rest, mult)
		}
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return uint64(n), true
}

func scaledBytes(numeral string, mult float64) (uint64, bool) {
	n, err := strconv.ParseFloat(numeral, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return uint64(n * mult), true
}

// percent renders used/hard as a whole-number percentage, or "" when hard is
// zero or unparseable — a quota with no limit set has no percentage to
// report, and reporting "0%" would read as "plenty of headroom" for the
// opposite reason.
func percentOf(used, hard float64) string {
	if hard <= 0 {
		return ""
	}
	return strconv.Itoa(int(used/hard*100)) + "%"
}
