package config

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The schema is hand-written beside the structs, and this is what keeps that
// honest: every yaml tag and every claimed key appears as a schema property,
// and no schema property names a field that does not exist. A field added
// without a schema entry fails here rather than silently completing to
// nothing in the operator's editor.
func TestSchemaCoversEveryClaimedKey(t *testing.T) {
	s := Schema()
	defs, ok := s["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema has no $defs")
	}
	compare(t, "root", propNames(t, s), yamlTags(t, reflect.TypeOf(Config{})))
	compare(t, "dashboard", propNames(t, s["properties"].(map[string]any)["dashboard"].(map[string]any)),
		yamlTags(t, reflect.TypeOf(Dashboard{})))
	compare(t, "tile", propNames(t, defs["tile"].(map[string]any)),
		yamlTags(t, reflect.TypeOf(Tile{})))
	compare(t, "profile", propNames(t, defs["profile"].(map[string]any)), sortedKeys(profileKeys))
	compare(t, "connection", propNames(t, defs["connection"].(map[string]any)), sortedKeys(connectionKeys))
}

// Every property carries a description — the hover text is the reason the
// schema exists, so a bare type is a field the editor cannot explain.
func TestEverySchemaPropertyIsDescribed(t *testing.T) {
	s := Schema()
	blocks := map[string]map[string]any{"root": s}
	for name, def := range s["$defs"].(map[string]any) {
		blocks[name] = def.(map[string]any)
	}
	blocks["dashboard"] = s["properties"].(map[string]any)["dashboard"].(map[string]any)
	for block, m := range blocks {
		for name, raw := range m["properties"].(map[string]any) {
			p, ok := raw.(map[string]any)
			if !ok {
				t.Errorf("%s.%s is not an object", block, name)
				continue
			}
			if d, _ := p["description"].(string); strings.TrimSpace(d) == "" {
				t.Errorf("%s.%s has no description", block, name)
			}
		}
	}
}

// The profile-name pattern is the loader's own regex, not a copy of it.
func TestSchemaProfileNamesUseTheLoadersOwnPattern(t *testing.T) {
	s := Schema()
	profiles := s["properties"].(map[string]any)["profiles"].(map[string]any)
	got := profiles["propertyNames"].(map[string]any)["pattern"]
	if got != profileName.String() {
		t.Errorf("schema pattern %q, loader pattern %q", got, profileName.String())
	}
}

// The whole schema survives a JSON round trip — what `rta config schema`
// prints is what an editor parses.
func TestSchemaMarshals(t *testing.T) {
	b, err := json.Marshal(Schema())
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", back["$schema"])
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func propNames(t *testing.T, block map[string]any) []string {
	t.Helper()
	props, ok := block["properties"].(map[string]any)
	if !ok {
		t.Fatalf("block has no properties: %v", block)
	}
	out := make([]string, 0, len(props))
	for k := range props {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// yamlTags lists a struct's yaml key names, unexported fields skipped —
// they are loader bookkeeping (trusted, unknown), not file keys.
func yamlTags(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			t.Fatalf("%s.%s has no yaml tag", typ.Name(), f.Name)
		}
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func compare(t *testing.T, block string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s schema properties = %v, structs/claimed keys say %v — "+
			"a field and its schema entry must arrive together", block, got, want)
	}
}
