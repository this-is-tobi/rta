package app

import (
	"bytes"
	"encoding/json"
	"testing"
)

// The command's whole contract is bytes an editor can parse: raw JSON on
// stdout, no envelope, ending in exactly one newline so a redirect writes a
// well-formed file.
func TestConfigSchemaPrintsParseableJSON(t *testing.T) {
	cmd := configSchemaCommand()
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
