package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The command's whole contract is bytes an editor can parse: raw JSON on
// stdout, no envelope, ending in exactly one newline so a redirect writes a
// well-formed file.
func TestConfigSchemaPrintsParseableJSON(t *testing.T) {
	cmd := configSchemaCommand(testRegistry(t))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if got["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", got["$schema"])
	}
	if _, ok := got["properties"].(map[string]any)["profiles"]; !ok {
		t.Error("schema does not describe profiles")
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("}\n")) || bytes.HasSuffix(buf.Bytes(), []byte("\n\n")) {
		t.Error("output does not end in exactly one newline")
	}
}

// The schema carries each registered plugin's keys, from the declarations
// everything else reads: the type, the widest range any capability reading the
// key takes, a nested key as a nested block, and options as examples — never as
// an enum, since a run reads an option in any case.
func TestConfigSchemaDescribesEachPluginsKeys(t *testing.T) {
	cmd := configSchemaCommand(configRegistry(t))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	plugins := got["properties"].(map[string]any)["plugins"].(map[string]any)
	net := plugins["patternProperties"].(map[string]any)["^net(@[0-9a-f]+)?$"].(map[string]any)
	if net["additionalProperties"] != false {
		t.Error("a key net does not read is not flagged")
	}
	props := net["properties"].(map[string]any)
	timeout := props["timeout"].(map[string]any)
	if timeout["type"] != "integer" || timeout["minimum"] != float64(1) || timeout["maximum"] != float64(300) ||
		timeout["default"] != float64(10) {
		t.Errorf("timeout = %v, want the widest range of net.ping and net.port", timeout)
	}
	if d := timeout["description"].(string); !strings.Contains(d, "seconds to wait") ||
		!strings.Contains(d, "net.ping") || !strings.Contains(d, "net.port") {
		t.Errorf("timeout is described as %q", d)
	}
	count := props["ping"].(map[string]any)["properties"].(map[string]any)["count"].(map[string]any)
	if count["type"] != "integer" || count["maximum"] != float64(100) {
		t.Errorf("a nested key is not a nested block: %v", props["ping"])
	}
	encoding := props["encoding"].(map[string]any)
	if _, closed := encoding["enum"]; closed || len(encoding["examples"].([]any)) != 2 {
		t.Errorf("an option is offered as an example, not an enum: %v", encoding)
	}
	if props["resolve"].(map[string]any)["type"] != "boolean" || props["tags"].(map[string]any)["type"] != "array" {
		t.Errorf("types: %v %v", props["resolve"], props["tags"])
	}
	if _, offered := props["token"]; offered {
		t.Error("a credential is offered as a config key")
	}
	if _, offered := props["name"]; offered {
		t.Error("an input that is not a config key is offered")
	}
	// A plugin this machine has not got stays an open object.
	if plugins["additionalProperties"].(map[string]any)["type"] != "object" {
		t.Error("an unknown plugin's section is no longer accepted")
	}
}

// What the schema says an editor accepts is what `rta config check` accepts: a
// key the schema offers for a plugin is one set takes.
func TestEveryKeyTheSchemaOffersIsOneConfigSetTakes(t *testing.T) {
	reg := configRegistry(t)
	for _, p := range reg.Plugins() {
		section := pluginSchema(reg, p)
		if section == nil {
			continue
		}
		var walk func(block map[string]any, prefix string)
		walk = func(block map[string]any, prefix string) {
			for name, raw := range block["properties"].(map[string]any) {
				child := raw.(map[string]any)
				if child["type"] == "object" {
					walk(child, prefix+name+".")
					continue
				}
				if _, verr := resolveConfigKey(reg, "plugins."+p.Name+"."+prefix+name); verr != nil {
					t.Errorf("the schema offers %s.%s%s, and config set refuses it: %v", p.Name, prefix, name, verr.Message)
				}
			}
		}
		walk(section, "")
	}
}

// A length of time is text with a grammar, and no schema keyword holds a range
// for text: its bounds are said in the description, and `minimum` and `maximum`
// stay what JSON Schema makes them, numbers. The built-ins' timeouts are
// durations, and a schema that held "1s" as a minimum was not one an editor or
// `helm lint` could compile.
func TestConfigSchemaDescribesADurationAsTextWithAGrammar(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(configSchemaFor(reg))
	if err != nil {
		t.Fatal(err)
	}
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	var walk func(path string, node any)
	walk = func(path string, node any) {
		switch n := node.(type) {
		case map[string]any:
			for key, child := range n {
				if _, isNumber := child.(float64); !isNumber && (key == "minimum" || key == "maximum") {
					t.Errorf("%s.%s = %v (%T), want a number", path, key, child, child)
				}
				walk(path+"."+key, child)
			}
		case []any:
			for _, child := range n {
				walk(path, child)
			}
		}
	}
	walk("schema", schema)

	var http plugin.Plugin
	for _, p := range reg.Plugins() {
		if p.Name == "http" {
			http = p
		}
	}
	timeout := pluginSchema(reg, http)["properties"].(map[string]any)["timeout"].(map[string]any)
	if timeout["type"] != "string" || timeout["pattern"] != plugin.DurationPattern || timeout["default"] != "30s" {
		t.Errorf("http timeout = %v, want a string with the duration grammar defaulting to 30s", timeout)
	}
	if d := timeout["description"].(string); !strings.Contains(d, "30s") || !strings.Contains(d, "from 1s to 10m") {
		t.Errorf("http timeout is described as %q, want the unit and the range said", d)
	}
}
