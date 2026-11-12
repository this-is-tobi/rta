package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// A plugin declares the key it reads as a dotted path, `password.symbols` or
// `query.limit`, and a plugins: section, a profile's `set:` and its `only:`
// state it either way: as one key with the dots in it, the way `rta explain`
// and every writer print it, or nested, one block per segment. Both are the
// same key. What follows is the one place that goes between the spellings, so
// no reader looks for one and misses the other.
//
// Both, because neither alone carries every key. Nested only, the way a
// section was read until now, a plugin cannot declare `database` beside
// `database.list.limit`: `database:` would be a value and a block at once, and
// the writer, given the second, replaced the first with a block and said only
// "wrote". Flat only would have refused every section written nested until
// then. No input takes a map, so a block is never a value, and the two
// spellings never disagree about what a line means.

// FlatSection is every value a settings block states, by the dotted path to
// it, however each is spelled: `query.limit: 500` and `query: {limit: 500}`
// are both the key query.limit. twice lists, sorted, each path the block
// states in more than one spelling. No reader can choose between those, so a
// caller refuses the block rather than pick one.
func FlatSection(section map[string]any) (flat map[string]any, twice []string) {
	flat = map[string]any{}
	stated := map[string]int{}
	for _, leaf := range SectionLeaves(section) {
		stated[leaf.Key]++
		flat[leaf.Key] = leaf.Value
	}
	for key, n := range stated {
		if n > 1 {
			twice = append(twice, key)
		}
	}
	sort.Strings(twice)
	return flat, twice
}

// SectionValue is the value stated at a dotted key in a settings block, in
// whichever spelling, and whether there is one. A block is never a value.
func SectionValue(section map[string]any, key string) (any, bool) {
	spots := spellings(section, key)
	if len(spots) == 0 {
		return nil, false
	}
	return spots[0].holder()[spots[0].key], true
}

// SetSectionValue states v at a dotted key. A value the block states once for
// the key is replaced where it is, so a block written nested stays nested. A
// key the block does not state is written flat, as one key: a nested write
// makes a block of a name that may already hold a value, `database: app`
// beside `database.list.limit`, which is how the value was lost. A key stated
// in several spellings is left with one, the flat one.
func SetSectionValue(section map[string]any, key string, v any) {
	normalize(section)
	spots := spellings(section, key)
	if len(spots) == 1 {
		spots[0].holder()[spots[0].key] = v
		return
	}
	for _, s := range spots {
		s.remove()
	}
	section[key] = v
}

// DeleteSectionValue removes the key at a dotted path in every spelling the
// block states it in, and the blocks that leaves empty on the way up. It
// reports whether there was anything to remove.
func DeleteSectionValue(section map[string]any, key string) bool {
	normalize(section)
	spots := spellings(section, key)
	for _, s := range spots {
		s.remove()
	}
	return len(spots) > 0
}

// SectionLeaf is one value of a settings block, by the dotted path to it.
type SectionLeaf struct {
	Key   string
	Value any
}

// SectionLeaves lists every value a settings block states, sorted by its
// dotted path, however deep and however spelled. A path stated in two
// spellings is listed twice, as the file says it (FlatSection reports it).
func SectionLeaves(section map[string]any) []SectionLeaf {
	var out []SectionLeaf
	var walk func(m map[string]any, prefix string)
	walk = func(m map[string]any, prefix string) {
		for k, v := range m {
			if inner := asMap(v); inner != nil {
				walk(inner, prefix+k+".")
				continue
			}
			out = append(out, SectionLeaf{Key: prefix + k, Value: v})
		}
	}
	walk(section, "")
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// SectionKeys is SectionLeaves without the values.
func SectionKeys(section map[string]any) []string {
	leaves := SectionLeaves(section)
	out := make([]string, len(leaves))
	for i, l := range leaves {
		out[i] = l.Key
	}
	return out
}

// spot is one place a settings block states a dotted key: the blocks from the
// section down to the one holding the value, the key of each in the one
// before it, and the value's key in the last.
type spot struct {
	chain []map[string]any
	steps []string
	key   string
}

// holder is the block the spot's value sits in.
func (s spot) holder() map[string]any { return s.chain[len(s.chain)-1] }

// remove deletes the spot's value, and each block that leaves empty, from the
// innermost out.
func (s spot) remove() {
	delete(s.holder(), s.key)
	for i := len(s.chain) - 1; i > 0 && len(s.chain[i]) == 0; i-- {
		delete(s.chain[i-1], s.steps[i-1])
	}
}

// spellings is every place section states key: the key split anywhere between
// its segments into blocks and one key, `query.limit` as itself or as `limit`
// under `query`. Ordered from the flattest, the order SectionValue prefers.
//
// It reads and never writes. A block a decoder handed back as map[any]any is
// walked as a copy, so a writer normalizes the section first, and its spots
// are then the section's own blocks.
func spellings(section map[string]any, key string) []spot {
	var out []spot
	var find func(chain []map[string]any, steps, segs []string)
	find = func(chain []map[string]any, steps, segs []string) {
		m := chain[len(chain)-1]
		for i := len(segs); i >= 1; i-- {
			k := strings.Join(segs[:i], ".")
			v, ok := m[k]
			if !ok {
				continue
			}
			inner := asMap(v)
			if i == len(segs) {
				if inner == nil {
					out = append(out, spot{chain: slices.Clone(chain), steps: slices.Clone(steps), key: k})
				}
				continue
			}
			if inner != nil {
				find(append(slices.Clone(chain), inner), append(slices.Clone(steps), k), segs[i:])
			}
		}
	}
	find([]map[string]any{section}, nil, strings.Split(key, "."))
	return out
}

// normalize converts every block under section that a decoder handed back as
// map[any]any into a map[string]any, in place, so a write lands in the
// section and not in a copy of one of its blocks.
func normalize(section map[string]any) {
	for k, v := range section {
		if inner := asMap(v); inner != nil {
			section[k] = inner
			normalize(inner)
		}
	}
}

// asMap reads both map shapes a decoder can hand back, and nil for anything
// that is not a block.
func asMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprint(k)] = val
		}
		return out
	}
	return nil
}
