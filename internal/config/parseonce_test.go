package config

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/internal/yamlguard"
	"github.com/this-is-tobi/rta/pkg/view"
)

// threeParses is parse as it was, three parses of the text: the anchor check,
// the decode, and the raw profiles read again for the keys nothing claimed.
// Kept here as the reference the one parse is held to.
func threeParses(path string, data []byte) (Config, error) {
	var cfg Config
	data = trimBOM(data)
	if len(data) > 0 {
		if err := yamlguard.RefuseAnchors(data); err != nil {
			return cfg, view.Errorf("config.invalid", "parsing %s: %v", path, err).WithHint(parseHint(err))
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			if refusal := refuseBadSections(path, data); refusal != nil {
				return cfg, refusal
			}
			return cfg, view.Errorf("config.invalid", "parsing %s: %v", path, err).WithHint(parseHint(err))
		}
		if err := refuseBadDashboards(path, cfg.Profiles); err != nil {
			return cfg, err
		}
	}
	cfg.trusted = trustedPath()
	if len(cfg.Profiles) > 0 {
		var raw struct {
			Profiles map[string]map[string]any `yaml:"profiles"`
		}
		_ = yaml.Unmarshal(data, &raw)
		for name, p := range cfg.Profiles {
			p = cfg.Stamp(p)
			p.unknown = unclaimed(raw.Profiles[name], profileKeys)
			nested, _ := raw.Profiles[name]["plugins"].(map[string]any)
			for key, conn := range p.Plugins {
				fields, _ := nested[key].(map[string]any)
				conn.unknown = unclaimed(fields, connectionKeys)
				p.Plugins[key] = conn
			}
			cfg.Profiles[name] = p
		}
	}
	return cfg, nil
}

// One parse answers what three did: the same configuration for every document
// that reads, and the same refusal, word for word and hint for hint, for every
// one that does not.
func TestOneParseAnswersWhatThreeDid(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	for name, doc := range map[string]string{
		"empty":          "",
		"comments only":  "# nothing here\n",
		"a bare marker":  "---\n",
		"a byte mark":    "\ufeffoutput: json\n",
		"settings":       "output: json\nconfine: strict\ntheme:\n  primary: \"#112233\"\n",
		"plugins":        "plugins:\n  pg:\n    host: db\n    dump: {include: schema}\n",
		"roles":          "roles:\n  ops:\n    grants: [pg, kube]\n    ttl: 2h\n",
		"profiles":       profile("base", "b.example") + "    note: base\n",
		"unknown keys":   "profiles:\n  p:\n    plguins: {}\n    plugins:\n      pg:\n        sett: {host: x}\n        set: {host: y}\n",
		"a dashboard":    "dashboard:\n  width: 2\n  tiles:\n    - id: sys.cpu\n      with: {cores: true}\n  views:\n    ops:\n      tiles:\n        - id: sys.mem\n",
		"a profile view": "profiles:\n  p:\n    dashboard: ops\n    plugins:\n      pg:\n        set: {host: x}\n",
		"two documents":  "output: json\n---\noutput: yaml\n",
		"unclosed":       "profiles: [unclosed\n",
		"a bad indent":   "output: json\n  confine: strict\n",
		"a tab":          "output:\tjson\n\tconfine: x\n",
		"a duplicate":    "output: json\noutput: yaml\n",
		"wrong types":    "dashboard: [1, 2]\n",
		"a wrong set":    "profiles:\n  p:\n    plugins:\n      pg:\n        set: [host]\n",
		"a section":      "dashboard:\n  sections:\n    ops: [1]\n",
		"an anchor":      "plugins:\n  pg: &base {host: x}\n  redis: *base\n",
		"a deep anchor":  "output: json\n---\nx: &a [1]\n",
		"a bad view":     "profiles:\n  p:\n    dashboard: {nope: [1]}\n",
	} {
		t.Run(name, func(t *testing.T) {
			want, wantErr := threeParses("config.yaml", []byte(doc))
			got, gotErr := parse("config.yaml", []byte(doc))
			if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || hintOf(gotErr) != hintOf(wantErr) {
				t.Fatalf("refusal = %v (%s), want %v (%s)", gotErr, hintOf(gotErr), wantErr, hintOf(wantErr))
			}
			if wantErr == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("config = %+v, want %+v", got, want)
			}
		})
	}
}

func hintOf(err error) string {
	if verr, ok := err.(*view.Error); ok {
		return verr.Hint
	}
	return ""
}

// What a parse costs, three parses against one, on the text of eighty profiles.
func BenchmarkParse(b *testing.B) {
	var text strings.Builder
	text.WriteString("output: json\nprofiles:\n")
	for i := range 80 {
		fmt.Fprintf(&text, "  env-%03d:\n    plugins:\n", i)
		for _, ns := range []string{"pg", "redis", "kube", "vault", "s3"} {
			fmt.Fprintf(&text, "      %s:\n        set:\n          host: %s-%d.internal\n          tags: [a, b]\n", ns, ns, i)
		}
	}
	data := []byte(text.String())
	b.Setenv("RTA_CONFIG", filepath.Join(b.TempDir(), "config.yaml"))
	for name, parseWith := range map[string]func(string, []byte) (Config, error){"three": threeParses, "one": parse} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := parseWith("config.yaml", data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
