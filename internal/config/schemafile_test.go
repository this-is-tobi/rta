package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The header of a file rta creates points an editor at config.schema.json, and
// the file is there from the first write: an editor that cannot load the schema
// it is told to use reports it on line 1 of a file nothing is wrong with.
func TestAFileRtaCreatesHasTheSchemaItsHeaderNames(t *testing.T) {
	path := configAt(t)
	if err := Mutate(func(c Config) (Config, bool) { c.Output = "json"; return c, true }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), SchemaFile))
	if err != nil {
		t.Fatalf("the schema the header names was not written: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil || schema["$schema"] == nil {
		t.Errorf("it is not a JSON Schema: %v\n%s", err, raw)
	}
}

// The one `rta config edit` wrote knows the plugins' keys, and a later write
// does not put the plainer one over it.
func TestAWriteLeavesTheSchemaThatIsAlreadyThere(t *testing.T) {
	path := configAt(t)
	schemaPath := filepath.Join(filepath.Dir(path), SchemaFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath, []byte(`{"mine":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Mutate(func(c Config) (Config, bool) { c.Output = "json"; return c, true }); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(schemaPath); string(raw) != `{"mine":true}` {
		t.Errorf("the schema was replaced:\n%s", raw)
	}
}

// A file somebody wrote by hand has no header to point anywhere, and gets no
// second file beside it.
func TestAFileWithNoRtaHeaderGetsNoSchemaBesideIt(t *testing.T) {
	path := configAt(t)
	if err := os.WriteFile(path, []byte("output: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Mutate(func(c Config) (Config, bool) { c.Dashboard.Columns = 2; return c, true }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), SchemaFile)); !os.IsNotExist(err) {
		t.Errorf("a schema was written beside a hand-written file: %v", err)
	}
}
