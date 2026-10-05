package config

import (
	"reflect"
	"testing"
)

func TestASectionKeyIsReadAndWrittenNested(t *testing.T) {
	section := map[string]any{}
	SetSectionValue(section, "ping.count", 7)
	SetSectionValue(section, "ping.timeout", 5)
	SetSectionValue(section, "host", "db")
	want := map[string]any{"ping": map[string]any{"count": 7, "timeout": 5}, "host": "db"}
	if !reflect.DeepEqual(section, want) {
		t.Fatalf("section = %v, want %v", section, want)
	}
	if v, ok := SectionValue(section, "ping.count"); !ok || v != 7 {
		t.Errorf("ping.count = %v, %v", v, ok)
	}
	if _, ok := SectionValue(section, "ping"); ok {
		t.Error("a block was read as a value")
	}
	if _, ok := SectionValue(section, "ping.count.x"); ok {
		t.Error("a path through a value was read")
	}
	if got := SectionKeys(section); !reflect.DeepEqual(got, []string{"host", "ping.count", "ping.timeout"}) {
		t.Errorf("SectionKeys = %v", got)
	}
}

func TestDeletingTheLastKeyOfABlockTakesTheBlockToo(t *testing.T) {
	section := map[string]any{"ping": map[string]any{"count": 7, "timeout": 5}, "host": "db"}
	if !DeleteSectionValue(section, "ping.count") {
		t.Fatal("nothing deleted")
	}
	if !reflect.DeepEqual(section, map[string]any{"ping": map[string]any{"timeout": 5}, "host": "db"}) {
		t.Fatalf("after one: %v", section)
	}
	DeleteSectionValue(section, "ping.timeout")
	if !reflect.DeepEqual(section, map[string]any{"host": "db"}) {
		t.Fatalf("an emptied block was left behind: %v", section)
	}
	if DeleteSectionValue(section, "ping.count") || DeleteSectionValue(section, "absent") {
		t.Error("deleting what is not there reported a change")
	}
}

func TestWritingBelowAValueReplacesTheValueWithTheBlock(t *testing.T) {
	section := map[string]any{"ping": "oops"}
	SetSectionValue(section, "ping.count", 3)
	if !reflect.DeepEqual(section, map[string]any{"ping": map[string]any{"count": 3}}) {
		t.Errorf("section = %v", section)
	}
}

func TestNestSectionTurnsFlatDottedKeysIntoTheBlocksTheyName(t *testing.T) {
	got := NestSection(map[string]any{
		"ping.count": 7, "ping.timeout": 5, "host": "db",
		"trace": map[string]any{"probes": 2},
	})
	want := map[string]any{
		"ping": map[string]any{"count": 7, "timeout": 5}, "host": "db",
		"trace": map[string]any{"probes": 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// What a reader sees wins over a flat key that says otherwise.
	got = NestSection(map[string]any{"ping.count": 9, "ping": map[string]any{"count": 7}})
	if v, _ := SectionValue(got, "ping.count"); v != 7 {
		t.Errorf("ping.count = %v, want the nested 7", v)
	}
}

func TestSectionHelpersReadTheOtherMapShapeADecoderCanReturn(t *testing.T) {
	section := map[string]any{"ping": map[any]any{"count": 7}}
	if v, ok := SectionValue(section, "ping.count"); !ok || v != 7 {
		t.Errorf("ping.count = %v, %v", v, ok)
	}
	SetSectionValue(section, "ping.timeout", 5)
	if v, _ := SectionValue(section, "ping.timeout"); v != 5 {
		t.Errorf("a write below a map[any]any was lost: %v", section)
	}
	if !DeleteSectionValue(section, "ping.count") {
		t.Error("a key below a map[any]any was not deleted")
	}
}
