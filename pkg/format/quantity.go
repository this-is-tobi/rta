package format

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// quantitySuffixes are the suffixes of a Kubernetes quantity, the two-letter
// binary ones first so that "Mi" is not read as an "M" after an "i". One below
// one divides by its power of ten rather than multiplying by the fraction:
// 1e9 is a float64 exactly and 1e-9 is not, so "148912532n" read as
// 148912532 × 1e-9 missed the 0.148912532 it is by the last bit.
var quantitySuffixes = []struct {
	suffix string
	scale  float64
	below  bool
}{
	{"Ki", 1 << 10, false}, {"Mi", 1 << 20, false}, {"Gi", 1 << 30, false},
	{"Ti", 1 << 40, false}, {"Pi", 1 << 50, false}, {"Ei", 1 << 60, false},
	{"n", 1e9, true}, {"u", 1e6, true}, {"m", 1e3, true},
	{"k", 1e3, false}, {"M", 1e6, false}, {"G", 1e9, false},
	{"T", 1e12, false}, {"P", 1e15, false}, {"E", 1e18, false},
}

// quantityNumber is the number a quantity's suffix follows: digits with an
// optional fraction, and an optional exponent in place of a suffix (1e3).
var quantityNumber = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// ParseQuantity reads a Kubernetes resource quantity as the number it is: a
// binary suffix counts in powers of 1024 (Ki to Ei), a decimal one in powers
// of 1000 (n, u and m below one, k to E above), and an exponent stands for
// either (1e3). "8Gi" is 8589934592, "10G" is 1e10 and "500m" is 0.5, the
// half a CPU a pod requests. "n" is in no spec, and is how metrics-server
// reports a pod's CPU ("148912532n"): kube's first parse, written against
// specs alone, read every real usage as nothing.
//
// A size is compared by this number and never by its spelling: a claim may
// ask in one suffix and its volume report in another, and a provisioner that
// rounds up to whole GiB answers a 10G claim with 10Gi, which read as text
// was a volume short of what it asked for.
//
// By hand rather than through k8s.io/apimachinery's resource.Quantity, which
// a plugin that drives kubectl links for nothing else; kube and cnpg each
// carried a copy of this. A negative quantity, one that is not a number, and
// a suffix outside Kubernetes' grammar are not one.
func ParseQuantity(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	scale, below := 1.0, false
	for _, q := range quantitySuffixes {
		if rest, ok := strings.CutSuffix(s, q.suffix); ok {
			s, scale, below = rest, q.scale, q.below
			break
		}
	}
	if !quantityNumber.MatchString(s) {
		return 0, false
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	if below {
		return n / scale, true
	}
	if math.IsInf(n*scale, 0) {
		return 0, false
	}
	return n * scale, true
}
