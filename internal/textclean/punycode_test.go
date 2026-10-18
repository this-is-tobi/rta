package textclean

import (
	"math/rand"
	"strings"
	"testing"

	"golang.org/x/net/idna"
)

// cp is the characters with these code points: a source file may not hold a
// character that reads as one it is not, so the fixtures are planted by code
// point.
func cp(points ...rune) string { return string(points) }

// The name is the xn-- form IDNA publishes, with the mapping a request does
// first: golang.org/x/net/idna makes the same name for every host the note is
// made for. That package is what the test holds the note to, and it is not a
// dependency of the binary, which carries none of its tables.
func TestTheNoteIsTheNameIDNAMakes(t *testing.T) {
	for _, host := range []string{
		cyrillicHost, "api." + cyrillicHost, "G" + cp(0x406) + "THUB.COM", cp(0xff47) + "ithub.com",
		"github" + cp(0x3002) + "com", "b" + cp(0xfc) + "cher.example", cp(0x65e5, 0x672c, 0x8a9e) + ".jp",
		"caf" + cp(0xe9) + ".example", "m" + cp(0xfc) + "nchen-" + cp(0xe9) + "t" + cp(0xe9) + ".de",
		cp(0x440, 0x430, 0x443, 0x435, 0x43b) + ".com", "xn--gthub-n2e." + cp(0x456) + ".com",
		cp(0x4f8b, 0x3048) + "." + cp(0x30c6, 0x30b9, 0x30c8), "a" + cp(0x456) + "b.c" + cp(0x456) + "d.e" + cp(0x456) + "f",
		"x." + cp(0x456), "snow" + cp(0x2603) + "man.example", "g" + cp(0x456) + "t_hub.example",
		"g" + cp(0x456) + "thub" + cp(0xad) + ".com", "g" + cp(0x456) + "thub" + cp(0x200b) + ".com",
		cp(0xff27, 0xff29, 0xff34) + cp(0x456) + ".org",
	} {
		// IDNA's mapping and encoding without its refusals (an underscore, a
		// label that is not a name): the note says what a name would be, and
		// agrees with the mapping everywhere that is not a refusal.
		want, err := idna.New(idna.MapForLookup(), idna.StrictDomainName(false)).ToASCII(host)
		if err != nil {
			t.Errorf("idna.ToASCII(%q): %v", host, err)
			continue
		}
		if got := hostNote("https://" + host + "/"); got != want {
			t.Errorf("the note for %q is %q, want what IDNA makes: %q", host, got, want)
		}
	}
}

// The encoder is RFC 3492's: every label below comes out as the package that
// implements it makes it, the samples of the RFC's section 7.1 among them.
func TestTheEncoderIsRFC3492s(t *testing.T) {
	for _, label := range []string{
		cp(0x644, 0x64a, 0x647, 0x645, 0x627, 0x628, 0x62a, 0x643, 0x644, 0x645, 0x648, 0x634, 0x639, 0x631, 0x628, 0x64a, 0x61f),
		cp(0x4ed6, 0x4eec, 0x4e3a, 0x4ec0, 0x4e48, 0x4e0d, 0x8bf4, 0x4e2d, 0x6587),
		"Pro" + cp(0x10d) + "prost" + cp(0x11b) + "nemluv" + cp(0xed, 0x10d) + "esky",
		cp(0x5dc, 0x5de, 0x5d4, 0x5d4, 0x5dd, 0x5e4, 0x5e9, 0x5d5, 0x5d8, 0x5dc, 0x5d0, 0x5de, 0x5d3, 0x5d1, 0x5e8, 0x5d9, 0x5dd,
			0x5e2, 0x5d1, 0x5e8, 0x5d9, 0x5ea),
		"3" + cp(0x5e74) + "b" + cp(0x7d44, 0x91d1, 0x516b, 0x5148, 0x751f),
		cp(0x456), "g" + cp(0x456) + "thub", cp(0xfc), "a" + cp(0xfc), cp(0xfc) + "a", cp(0x1f600), "-" + cp(0xe9) + "-",
	} {
		want, err := idna.Punycode.ToASCII(label)
		if err != nil {
			t.Fatalf("idna.Punycode.ToASCII(%q): %v", label, err)
		}
		if got := "xn--" + punycode(label); got != want {
			t.Errorf("punycode(%q) = %q, want %q", label, got, want)
		}
	}
	if got := "xn--" + punycode("g"+cp(0x456)+"thub"); got != "xn--gthub-n2e" {
		t.Errorf("the lookalike label is %q, want xn--gthub-n2e", got)
	}
}

// A host with nothing in it to encode gets no name from the encoder: only a
// label that is not ASCII is encoded, and the rest are left as they were.
func TestOnlyALabelThatIsNotASCIIIsEncoded(t *testing.T) {
	got := asciiHost("WWW.Example." + cp(0x456) + "x.com")
	if !strings.HasPrefix(got, "www.example.xn--") || !strings.HasSuffix(got, ".com") {
		t.Errorf("asciiHost = %q, want the ASCII labels lowered and the other encoded", got)
	}
}

// And on labels nobody chose: a few thousand of random length, drawn from the
// ASCII letters and from the planes a name is written in, from one seed so a
// disagreement is the same disagreement the next run.
func TestTheEncoderAgreesOnRandomLabels(t *testing.T) {
	rng := rand.New(rand.NewSource(3492))
	ranges := [][2]rune{{'a', 'z'}, {'0', '9'}, {0xa1, 0x24f}, {0x370, 0x4ff}, {0x5d0, 0x5ea}, {0x900, 0x97f},
		{0x3040, 0x30ff}, {0x4e00, 0x9fff}, {0xac00, 0xd7a3}, {0x1f300, 0x1f5ff}}
	for range 3000 {
		label := make([]rune, 1+rng.Intn(24))
		for i := range label {
			r := ranges[rng.Intn(len(ranges))]
			label[i] = r[0] + rune(rng.Intn(int(r[1]-r[0])+1))
		}
		if string(label) == strings.ToLower(string(label)) && strings.ContainsFunc(string(label), func(r rune) bool { return r > 0x7f }) {
			want, err := idna.Punycode.ToASCII(string(label))
			if err != nil {
				continue
			}
			if got := "xn--" + punycode(string(label)); got != want {
				t.Fatalf("punycode(%q) = %q, want %q", string(label), got, want)
			}
		}
	}
}
