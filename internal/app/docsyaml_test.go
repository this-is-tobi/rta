package app

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/internal/config"
)

// The profile chapter showed a connection as
//
//	plugins:
//	  pg:
//	    host: pg.example.internal
//	    set: {sslmode: verify-ca}
//
// which is not a profile — there is no `profiles: <name>:` above it, so a
// reader who pasted it got the plugin section of the config — and whose `host`
// is a key nothing reads, so one who wrapped it themselves got a profile that
// `rta profile list` marks invalid. Both sat in a fenced block, which the
// command checks beside this one never look inside: they read what a shell
// runs, and a config example is not a command.
//
// So every yaml block that is written as rta's own config — its top-level keys
// all ones the config file has — is read the way the file is: strictly, so a
// key no field claims fails the page that wrote it, and then held to what a
// profile is held to when it loads. A plugin's own section that carries a
// connection's `kube:`, `ssh:` or `secrets:` is the first example above with
// its profile missing, which strict reading cannot see: a plugin section is
// free-form, since only the plugin knows its keys. A block that is only a fragment (`secrets:`
// alone, a policy file, an index manifest) names a key the config does not have
// and is left to the reader's judgement, which is the line that keeps a gate
// like this from being turned off: it fires on what is plainly a config file,
// not on anything that happens to be yaml.
func TestEveryConfigBlockTheDocsShowLoadsAsWritten(t *testing.T) {
	repo := repoRoot(t)
	checked := 0
	for _, rel := range markdownPages(t, repo) {
		for _, block := range yamlFences(readDoc(t, repo, rel)) {
			if !isConfigBlock(block.text) {
				continue
			}
			checked++
			var cfg config.Config
			if err := yaml.UnmarshalWithOptions([]byte(block.text), &cfg, yaml.Strict()); err != nil {
				t.Errorf("%s:%d: the config block does not read as the config file: %v", rel, block.line, err)
				continue
			}
			for name, section := range cfg.Plugins {
				for key := range section {
					if connectionOnlyKeys[key] {
						t.Errorf("%s:%d: plugins.%s has %s, which is a profile's plugin entry — "+
							"written with no `profiles: <name>:` above it, it is a key the plugin's own section does not read",
							rel, block.line, name, key)
					}
				}
			}
			for name, p := range cfg.Profiles {
				where := rel + ":" + strconv.Itoa(block.line) + ": profile " + name
				if p.BadTTL() || p.BadColor() {
					t.Errorf("%s has a ttl or a color the loader refuses", where)
				}
				for key, conn := range p.Plugins {
					if bad := conn.BadSecretRefs(); len(bad) > 0 {
						t.Errorf("%s, %s: %s is not a kv: or kube: reference", where, key, strings.Join(bad, ", "))
					}
					if conn.Kube != "" && conn.SSH != "" {
						t.Errorf("%s, %s: states both kube and ssh, which a call cannot open at once", where, key)
					}
					if conn.TunnelTLS && !conn.Tunnelled() {
						t.Errorf("%s, %s: tunnelTLS with no forward to describe", where, key)
					}
				}
			}
		}
	}
	if checked < 5 {
		t.Fatalf("found only %d config blocks in the docs, which cannot be right — has the fence changed?", checked)
	}
}

// yamlBlock is one fenced yaml block and the line its first row is on.
type yamlBlock struct {
	line int
	text string
}

// yamlFences is every block fenced as yaml or yml.
func yamlFences(body string) []yamlBlock {
	var out []yamlBlock
	var cur []string
	start := 0
	in := false
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !in && (trimmed == "```yaml" || trimmed == "```yml"):
			in, cur, start = true, nil, i+2
		case in && trimmed == "```":
			out = append(out, yamlBlock{line: start, text: strings.Join(cur, "\n") + "\n"})
			in = false
		case in:
			cur = append(cur, line)
		}
	}
	return out
}

// configKeys are the top-level keys of rta's config file.
var configKeys = map[string]bool{
	"output": true, "dashboard": true, "plugins": true, "profiles": true, "theme": true, "roles": true,
}

// connectionOnlyKeys are the keys of a profile's plugin entry that a plugin's
// own configuration section never carries.
var connectionOnlyKeys = map[string]bool{
	"kube": true, "ssh": true, "tunnelTLS": true, "secrets": true, "secrets-from": true,
}

var topLevelKey = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*):`)

// isConfigBlock says whether a block is written as the config file: it has
// top-level keys and every one is a key the file has.
func isConfigBlock(text string) bool {
	keys := 0
	for _, line := range strings.Split(text, "\n") {
		m := topLevelKey.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if !configKeys[m[1]] {
			return false
		}
		keys++
	}
	return keys > 0
}
