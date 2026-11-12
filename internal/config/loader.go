package config

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync/atomic"
)

// Loader reads the configuration exactly as Load does, every file on every
// call, and reuses the parse of bytes it has already parsed.
//
// **Because parsing is the cost of a call, and reading the files is not.** A
// server reads the file for every call (mcp.Options.Reload), and a load spent
// about 2 ms at ten profiles and 17 ms, with 24 MB of garbage, at eighty
// parsing YAML, against 20 to 200 µs to read the bytes: most of an agent's
// call. A hit costs the read, a byte comparison and a copy: about 44 µs at ten
// profiles and 280 µs at eighty (BenchmarkReadingTheConfiguration).
//
// **What it compares, and why that is everything a parse depends on.** A parse
// is a function of each file's path, whether it exists, its exact bytes, and
// whether the path is one rta trusts (parse, trustedPath); the set is read
// with the loader's own functions, in its own order, and RTA_OUTPUT is laid
// over each answer rather than kept. So the parse kept is reused only for
// bytes identical to the ones it was made from — never for a modification
// time, a size or an inode, which a same-size edit in one clock tick, a rename
// over the file or a link pointed elsewhere all leave unchanged.
//
// **What it never does.** It never answers from a parse for a file that does
// not read now: any read fault, and any difference, runs Load's own code and
// returns Load's own answer and error, and a parse that failed is never kept.
// And it never hands out what it keeps. Set: values reach a handler by
// reference, and Go has no read-only map, so a shared parse one caller edited
// would be every later caller's connection: a hit is a deep copy (clone), and
// the copy kept is one no caller was ever given.
//
// One per process that reads often (`rta mcp serve`), and explicit there: the
// CLI reads once per command, and the writers (Mutate, the TUI's forms) read
// under the lock and must never be handed a parse from before it.
type Loader struct{ last atomic.Pointer[memo] }

// memo is one parse and the bytes it was made from.
type memo struct {
	trusted bool
	files   []source
	cfg     Config // never handed out: a hit is a clone of it
}

// source is one file as read: where, whether it was there, and what it held.
type source struct {
	path   string
	exists bool
	text   []byte
}

// Load answers what config.Load answers for the files as they are now.
func (l *Loader) Load() (Config, error) {
	files, read := gather()
	if read {
		if m := l.last.Load(); m != nil && m.same(files, trustedPath()) {
			return withEnvironment(m.cfg.clone()), nil
		}
	}
	r, err := Read()
	if err != nil {
		return r.Config(), err
	}
	// Keyed by the bytes this parse was made from, not the ones gather read
	// just before it: a file that changed between the two would otherwise
	// key one version's parse by the other's bytes.
	kept := make([]source, len(r.s.layers))
	for i, layer := range r.s.layers {
		kept[i] = source{path: layer.path, exists: layer.exists, text: layer.text}
	}
	l.last.Store(&memo{trusted: r.s.layers[0].cfg.trusted, files: kept, cfg: r.s.merged().clone()})
	return r.Config(), nil
}

// gather reads every file of the configuration as readStackWith does, with the
// same functions in the same order, and parses none. read is false at the
// first fault, which Load then answers by reading the files again as Load does.
//
// A source added to readStackWith and not here makes the two lists differ, so
// every call misses: slower, never stale (TestAnUnchangedLoadIsAHit).
func gather() (files []source, read bool) {
	main, err := ReadText()
	if err != nil {
		return nil, false
	}
	files = []source{{path: Path(), exists: main != nil, text: main}}
	dir := DropInDir()
	names, err := dropInNames(dir)
	if err != nil {
		return nil, false
	}
	for _, name := range names {
		file := filepath.Join(dir, name)
		text, err := readFile(file)
		if err != nil {
			return nil, false
		}
		files = append(files, source{path: file, exists: true, text: text})
	}
	return files, true
}

func (m *memo) same(files []source, trusted bool) bool {
	if m.trusted != trusted || len(m.files) != len(files) {
		return false
	}
	for i, f := range files {
		g := m.files[i]
		if f.path != g.path || f.exists != g.exists || !bytes.Equal(f.text, g.text) {
			return false
		}
	}
	return true
}

// withEnvironment lays the environment's overrides over cfg, as Reading.Config
// does over a read.
func withEnvironment(cfg Config) Config {
	if out := os.Getenv("RTA_OUTPUT"); out != "" {
		cfg.Output = out
	}
	return cfg
}

// clone is a copy of c that shares no map, slice or pointer with it, so what a
// caller does to one is nothing to the other. Written field by field, and held
// to every field of every type under Config by TestACloneSharesNothingWithItsOriginal,
// which names the path of any it misses: a field added without a line here
// fails that test rather than sharing.
func (c Config) clone() Config {
	out := c
	out.Dashboard = c.Dashboard.clone()
	if c.Plugins != nil {
		out.Plugins = make(map[string]map[string]any, len(c.Plugins))
		for k, v := range c.Plugins {
			out.Plugins[k] = cloneValues(v)
		}
	}
	if c.Profiles != nil {
		out.Profiles = make(map[string]Profile, len(c.Profiles))
		for k, p := range c.Profiles {
			out.Profiles[k] = p.clone()
		}
	}
	out.Theme = maps.Clone(c.Theme)
	if c.Roles != nil {
		out.Roles = make(map[string]Role, len(c.Roles))
		for k, r := range c.Roles {
			r.Grants = slices.Clone(r.Grants)
			out.Roles[k] = r
		}
	}
	return out
}

func (d Dashboard) clone() Dashboard {
	out := d
	out.Tiles, out.Add = cloneTiles(d.Tiles), cloneTiles(d.Add)
	out.Hidden, out.Order = slices.Clone(d.Hidden), slices.Clone(d.Order)
	out.Sections = maps.Clone(d.Sections)
	if d.Views != nil {
		out.Views = make(map[string]View, len(d.Views))
		for k, v := range d.Views {
			out.Views[k] = v.clone()
		}
	}
	return out
}

func (v View) clone() View {
	out := v
	out.Tiles, out.Add = cloneTiles(v.Tiles), cloneTiles(v.Add)
	out.Hidden, out.Order = slices.Clone(v.Hidden), slices.Clone(v.Order)
	out.Sections = maps.Clone(v.Sections)
	return out
}

func cloneTiles(tiles []Tile) []Tile {
	if tiles == nil {
		return nil
	}
	out := make([]Tile, len(tiles))
	for i, t := range tiles {
		t.With = cloneValues(t.With)
		out[i] = t
	}
	return out
}

func (p Profile) clone() Profile {
	out := p
	if p.Plugins != nil {
		out.Plugins = make(map[string]Connection, len(p.Plugins))
		for k, c := range p.Plugins {
			c.Set, c.Only = cloneValues(c.Set), cloneValues(c.Only)
			c.Secrets, c.unknown, c.twice = maps.Clone(c.Secrets), slices.Clone(c.unknown), slices.Clone(c.twice)
			out.Plugins[k] = c
		}
	}
	out.Theme = maps.Clone(p.Theme)
	out.Dashboard.Names = slices.Clone(p.Dashboard.Names)
	if p.Dashboard.Inline != nil {
		v := p.Dashboard.Inline.clone()
		out.Dashboard.Inline = &v
	}
	if p.Dashboard.bad != nil {
		b := *p.Dashboard.bad
		out.Dashboard.bad = &b
	}
	out.unknown = slices.Clone(p.unknown)
	return out
}

// cloneValues is a copy of a block of YAML values, all the way down.
func cloneValues(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

// cloneValue copies one decoded value. The decoder's own shapes are copied by
// type; anything else that holds a reference is copied by its kind, so a shape
// a later decoder hands back, and nobody listed here, is copied rather than
// shared (TestEveryShapeTheDecoderHandsBackIsCopied).
func cloneValue(v any) any {
	switch t := v.(type) {
	case nil, string, bool, int, int64, uint64, float64:
		return v
	case map[string]any:
		return cloneValues(t)
	case []any:
		if t == nil {
			return t
		}
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	case map[any]any:
		if t == nil {
			return t
		}
		out := make(map[any]any, len(t))
		for k, e := range t {
			out[k] = cloneValue(e)
		}
		return out
	case []byte:
		return slices.Clone(t)
	}
	return cloneByKind(reflect.ValueOf(v)).Interface()
}

func cloneByKind(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(cloneByKind(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		for it := v.MapRange(); it.Next(); {
			out.SetMapIndex(it.Key(), cloneByKind(it.Value()))
		}
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(cloneByKind(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(cloneByKind(v.Elem()))
		return out
	}
	return v
}
