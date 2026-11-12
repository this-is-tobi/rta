package config

import (
	"reflect"
	"testing"
)

// One key, three spellings: flat, nested, and nested with a dot left in the
// inner key. Each reads as the dotted path, and a block is never a value.
func TestAKeyIsReadHoweverItIsSpelled(t *testing.T) {
	for name, section := range map[string]map[string]any{
		"flat":          {"query.list.limit": 5},
		"nested":        {"query": map[string]any{"list": map[string]any{"limit": 5}}},
		"partly nested": {"query": map[string]any{"list.limit": 5}},
	} {
		t.Run(name, func(t *testing.T) {
			if v, ok := SectionValue(section, "query.list.limit"); !ok || v != 5 {
				t.Errorf("query.list.limit = %v, %v", v, ok)
			}
			if _, ok := SectionValue(section, "query.list"); ok {
				t.Error("a block was read as a value")
			}
			if _, ok := SectionValue(section, "query.list.limit.x"); ok {
				t.Error("a path through a value was read")
			}
			flat, twice := FlatSection(section)
			if !reflect.DeepEqual(flat, map[string]any{"query.list.limit": 5}) || twice != nil {
				t.Errorf("FlatSection = %v, %v", flat, twice)
			}
		})
	}
}

// A path stated in two spellings is reported, never settled: no reader can
// tell which of the two the operator meant.
func TestAPathStatedTwiceIsReported(t *testing.T) {
	_, twice := FlatSection(map[string]any{
		"query.limit": 9, "query": map[string]any{"limit": 7, "timeout": "5s"}, "host": "db",
	})
	if !reflect.DeepEqual(twice, []string{"query.limit"}) {
		t.Errorf("twice = %v", twice)
	}
}

// A key the section does not state is written flat, and one it states is
// replaced where it is, so a block written nested stays nested.
func TestANewKeyIsWrittenFlatAndAStatedOneWhereItIs(t *testing.T) {
	section := map[string]any{"ping": map[string]any{"count": 7}}
	SetSectionValue(section, "ping.count", 9)
	SetSectionValue(section, "ping.timeout", "5s")
	SetSectionValue(section, "host", "db")
	want := map[string]any{"ping": map[string]any{"count": 9}, "ping.timeout": "5s", "host": "db"}
	if !reflect.DeepEqual(section, want) {
		t.Fatalf("section = %v, want %v", section, want)
	}
	if got := SectionKeys(section); !reflect.DeepEqual(got, []string{"host", "ping.count", "ping.timeout"}) {
		t.Errorf("SectionKeys = %v", got)
	}
}

// The writer, given a key under a name that holds a value, replaced the value
// with a block and said only "wrote": `database: app` was gone. Written flat,
// both stay, as the plugin that declares both means them.
func TestAValueAndAKeyUnderItsNameKeepEachOther(t *testing.T) {
	section := map[string]any{"database": "app"}
	SetSectionValue(section, "database.list.limit", 5)
	if !reflect.DeepEqual(section, map[string]any{"database": "app", "database.list.limit": 5}) {
		t.Fatalf("section = %v", section)
	}
	SetSectionValue(section, "database", "other")
	if !reflect.DeepEqual(section, map[string]any{"database": "other", "database.list.limit": 5}) {
		t.Fatalf("after the reverse: %v", section)
	}
}

// A path stated twice is left with one spelling, the flat one, on a write,
// and with none on a delete; a block that leaves empty goes with it.
func TestAWriteOrADeleteSettlesAPathStatedTwice(t *testing.T) {
	section := map[string]any{"query.limit": 9, "query": map[string]any{"limit": 7}}
	SetSectionValue(section, "query.limit", 5)
	if !reflect.DeepEqual(section, map[string]any{"query.limit": 5}) {
		t.Fatalf("after the write: %v", section)
	}
	section = map[string]any{"query.limit": 9, "query": map[string]any{"limit": 7, "timeout": "5s"}}
	if !DeleteSectionValue(section, "query.limit") {
		t.Fatal("nothing deleted")
	}
	if !reflect.DeepEqual(section, map[string]any{"query": map[string]any{"timeout": "5s"}}) {
		t.Fatalf("after the delete: %v", section)
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

// A block a decoder handed back as map[any]any is read where it is and never
// rewritten by a read, which a shared configuration may be under several
// readers at once; a write converts it first, so the write lands.
func TestSectionHelpersReadTheOtherMapShapeADecoderCanReturn(t *testing.T) {
	section := map[string]any{"ping": map[any]any{"count": 7}}
	if v, ok := SectionValue(section, "ping.count"); !ok || v != 7 {
		t.Errorf("ping.count = %v, %v", v, ok)
	}
	if _, still := section["ping"].(map[any]any); !still {
		t.Error("a read rewrote the block")
	}
	SetSectionValue(section, "ping.count", 9)
	if v, _ := SectionValue(section, "ping.count"); v != 9 {
		t.Errorf("a write below a map[any]any was lost: %v", section)
	}
	if !DeleteSectionValue(section, "ping.count") {
		t.Error("a key below a map[any]any was not deleted")
	}
	if len(section) != 0 {
		t.Errorf("the emptied block was left: %v", section)
	}
}
