package config

import (
	"fmt"
	"sort"
	"strings"
)

// A plugin declares the key it reads as a dotted path — `password.symbols`,
// `ping.count` — and a plugins: section holds it nested: `password:` with
// `symbols:` under it. Written as one key with a dot in it, the value is never
// read, which is the trap `rta explain` springs by printing the dotted form.
// What follows is the one place that goes between the two spellings, so a
// writer cannot put the flat one in a file and a reader cannot look for it.

// SectionValue is the value stated at a dotted key in a plugins: section, and
// whether there is one. A nested block is a namespace and not a value, so it
// answers false.
func SectionValue(section map[string]any, key string) (any, bool) {
	cur := any(section)
	for _, seg := range strings.Split(key, ".") {
		next, ok := child(cur, seg)
		if !ok {
			return nil, false
		}
		cur = next
	}
	if asMap(cur) != nil {
		return nil, false
	}
	return cur, true
}

// SetSectionValue states v at a dotted key, making the blocks on the way.
// A block on the way that holds a value instead is replaced by the block: the
// key being written is the one the operator named.
func SetSectionValue(section map[string]any, key string, v any) {
	segs := strings.Split(key, ".")
	cur := section
	for _, seg := range segs[:len(segs)-1] {
		next := asMap(cur[seg])
		if next == nil {
			next = map[string]any{}
		}
		cur[seg] = next
		cur = next
	}
	cur[segs[len(segs)-1]] = v
}

// DeleteSectionValue removes the key at a dotted path, and the blocks it
// leaves empty on the way up. It reports whether there was anything to remove.
func DeleteSectionValue(section map[string]any, key string) bool {
	segs := strings.Split(key, ".")
	chain := []map[string]any{section}
	for _, seg := range segs[:len(segs)-1] {
		parent := chain[len(chain)-1]
		next := asMap(parent[seg])
		if next == nil {
			return false
		}
		parent[seg] = next
		chain = append(chain, next)
	}
	last := segs[len(segs)-1]
	if _, there := chain[len(chain)-1][last]; !there {
		return false
	}
	delete(chain[len(chain)-1], last)
	for i := len(chain) - 1; i > 0; i-- {
		if len(chain[i]) > 0 {
			break
		}
		delete(chain[i-1], segs[i-1])
	}
	return true
}

// SectionKeys lists the dotted keys of a plugins: section that hold a value,
// sorted: the leaves, however deep.
func SectionKeys(section map[string]any) []string {
	var out []string
	var walk func(m map[string]any, prefix string)
	walk = func(m map[string]any, prefix string) {
		for k, v := range m {
			if inner := asMap(v); inner != nil {
				walk(inner, prefix+k+".")
				continue
			}
			out = append(out, prefix+k)
		}
	}
	walk(section, "")
	sort.Strings(out)
	return out
}

// NestSection turns a section holding flat dotted keys into one holding the
// blocks they name: {"ping.count": 7} becomes {"ping": {"count": 7}}. What is
// already nested is kept, and a flat key that disagrees with a nested one it
// would merge into loses to the nested one, which is what a reader sees.
func NestSection(flat map[string]any) map[string]any {
	out := map[string]any{}
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if inner := asMap(flat[k]); inner != nil {
			merged := NestSection(inner)
			if existing := asMap(out[k]); existing != nil {
				for ik, iv := range merged {
					if _, taken := existing[ik]; !taken {
						existing[ik] = iv
					}
				}
				continue
			}
			SetSectionValue(out, k, merged)
			continue
		}
		if _, taken := SectionValue(out, k); taken {
			continue
		}
		SetSectionValue(out, k, flat[k])
	}
	return out
}

func child(cur any, seg string) (any, bool) {
	m := asMap(cur)
	if m == nil {
		return nil, false
	}
	v, ok := m[seg]
	return v, ok
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
