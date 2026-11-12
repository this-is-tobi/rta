package config

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"strings"
)

// loadsAgree fails unless the Loader answers what Load answers: the same
// configuration and the same error.
func loadsAgree(t *testing.T, l *Loader, what string) Config {
	t.Helper()
	got, gotErr := l.Load()
	want, wantErr := Load()
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: the Loader answered %+v, %v; Load answered %+v, %v", what, got, gotErr, want, wantErr)
	}
	return got
}

// A Loader is Load: for every layout the files can take, its first answer (a
// parse) and its second (a reuse of it) are Load's.
func TestALoaderAnswersWhatLoadAnswers(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T){
		"one file": func(t *testing.T) {
			layout(t, map[string]string{"config.yaml": "output: json\n" + profile("base", "b.example")})
		},
		"a file and its drop-ins": func(t *testing.T) {
			layout(t, map[string]string{
				"config.yaml":            profile("base", "b.example"),
				"config.d/10-stage.yaml": profile("stage", "s.example"),
				"config.d/20-roles.yaml": "roles:\n  ops:\n    grants: [db]\n",
			})
		},
		"a byte-order mark": func(t *testing.T) {
			layout(t, map[string]string{"config.yaml": "\ufeffoutput: yaml\n"})
		},
		"an empty file":  func(t *testing.T) { layout(t, map[string]string{"config.yaml": ""}) },
		"no file at all": func(t *testing.T) { layout(t, map[string]string{}) },
		"links": func(t *testing.T) {
			dir := layout(t, map[string]string{"real.yaml": profile("base", "b.example"),
				"stage.yaml": profile("stage", "s.example")})
			link(t, filepath.Join(dir, "real.yaml"), filepath.Join(dir, "config.yaml"))
			link(t, filepath.Join(dir, "stage.yaml"), filepath.Join(dir, "config.d", "10-stage.yaml"))
		},
		"RTA_OUTPUT": func(t *testing.T) {
			layout(t, map[string]string{"config.yaml": "output: json\n"})
			t.Setenv("RTA_OUTPUT", "yaml")
		},
		"the working directory's file": func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{".rta.yaml": "output: json\n" + profile("base", "b.example")})
			t.Setenv("RTA_CONFIG", "")
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("HOME", "")
			t.Chdir(dir)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("RTA_OUTPUT", "")
			setup(t)
			l := &Loader{}
			loadsAgree(t, l, "a parse")
			loadsAgree(t, l, "a reuse")
		})
	}
}

// What the Loader is for: a second read of the same bytes reuses the first
// parse. A source added to the reader and not to gather would make every call
// a miss — slower, never stale — and this is where that shows.
func TestAnUnchangedLoadIsAHit(t *testing.T) {
	layout(t, map[string]string{"config.yaml": profile("base", "b.example"),
		"config.d/10-stage.yaml": profile("stage", "s.example")})
	l := &Loader{}
	loadsAgree(t, l, "first")
	kept := l.last.Load()
	loadsAgree(t, l, "second")
	if l.last.Load() != kept {
		t.Error("the same bytes were parsed again")
	}
}

// Every change is seen, because the bytes are compared: an edit of the same
// size that leaves the modification time as it was, a file renamed over the
// config, a drop-in that comes and goes, and a link pointed somewhere else.
func TestALoaderSeesWhatTheClockDoesNot(t *testing.T) {
	dir := layout(t, map[string]string{"config.yaml": profile("base", "b.example"),
		"config.d/10-stage.yaml": profile("stage", "s.example")})
	l := &Loader{}
	loadsAgree(t, l, "first")

	stage := filepath.Join(dir, "config.d", "10-stage.yaml")
	info, err := os.Stat(stage)
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{"config.d/10-stage.yaml": profile("stage", "z.example")})
	if err := os.Chtimes(stage, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := loadsAgree(t, l, "same size, same time").Profiles["stage"].Plugins["db"].Set["host"]; got != "z.example" {
		t.Fatalf("host = %v, want the edit", got)
	}

	renamed := filepath.Join(dir, "next.yaml")
	writeFiles(t, dir, map[string]string{"next.yaml": profile("base", "n.example")})
	if err := os.Rename(renamed, filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	loadsAgree(t, l, "renamed over")

	writeFiles(t, dir, map[string]string{"config.d/20-prod.yaml": profile("prod", "p.example")})
	if cfg := loadsAgree(t, l, "a drop-in added"); len(cfg.Profiles) != 3 {
		t.Fatalf("profiles = %v", cfg.ProfileNames())
	}
	if err := os.Remove(filepath.Join(dir, "config.d", "20-prod.yaml")); err != nil {
		t.Fatal(err)
	}
	if cfg := loadsAgree(t, l, "a drop-in removed"); len(cfg.Profiles) != 2 {
		t.Fatalf("profiles = %v", cfg.ProfileNames())
	}

	writeFiles(t, dir, map[string]string{"a.yaml": "output: json\n", "b.yaml": "output: yaml\n"})
	main := filepath.Join(dir, "config.yaml")
	if err := os.Remove(main); err != nil {
		t.Fatal(err)
	}
	link(t, filepath.Join(dir, "a.yaml"), main)
	loadsAgree(t, l, "a link")
	if err := os.Remove(main); err != nil {
		t.Fatal(err)
	}
	link(t, filepath.Join(dir, "b.yaml"), main)
	if got := loadsAgree(t, l, "the link pointed elsewhere").Output; got != "yaml" {
		t.Fatalf("output = %q, want where the link leads now", got)
	}
}

// The environment decides which file and how far it is trusted, and RTA_OUTPUT
// is laid over each answer and never kept.
func TestALoaderFollowsTheEnvironment(t *testing.T) {
	t.Setenv("RTA_OUTPUT", "")
	layout(t, map[string]string{"config.yaml": "output: json\n" + profile("base", "b.example")})
	l := &Loader{}
	loadsAgree(t, l, "first")
	t.Setenv("RTA_OUTPUT", "yaml")
	if got := loadsAgree(t, l, "RTA_OUTPUT set").Output; got != "yaml" {
		t.Fatalf("output = %q", got)
	}
	t.Setenv("RTA_OUTPUT", "")
	if got := loadsAgree(t, l, "RTA_OUTPUT unset").Output; got != "json" {
		t.Fatalf("output = %q, want the file's again", got)
	}

	layout(t, map[string]string{"config.yaml": "output: csv\n"})
	if got := loadsAgree(t, l, "RTA_CONFIG moved").Output; got != "csv" {
		t.Fatalf("output = %q, want the other file's", got)
	}

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{".rta.yaml": "output: csv\n"})
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Chdir(dir)
	if loadsAgree(t, l, "the working directory's file").Trusted() {
		t.Fatal("a file nobody named was trusted")
	}
}

// A file that does not read is answered as Load answers it, and a parse that
// failed is never kept: the same good bytes written back are the parse already
// kept, and a broken file never answers from it.
func TestABrokenFileIsAnsweredAsLoadAnswersIt(t *testing.T) {
	dir := layout(t, map[string]string{"config.yaml": profile("base", "b.example"),
		"config.d/10-stage.yaml": profile("stage", "s.example")})
	main := filepath.Join(dir, "config.yaml")
	good := read(t, main)
	l := &Loader{}
	loadsAgree(t, l, "good")
	kept := l.last.Load()

	for name, breakIt := range map[string]func(t *testing.T){
		"does not parse": func(t *testing.T) { writeFiles(t, dir, map[string]string{"config.yaml": "profiles: [unclosed\n"}) },
		"cannot be read": func(t *testing.T) {
			if os.Geteuid() == 0 {
				t.Skip("root reads a file whatever its mode")
			}
			if err := os.Chmod(main, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(main, 0o600) })
		},
		"states a unit another states": func(t *testing.T) {
			writeFiles(t, dir, map[string]string{"config.yaml": profile("stage", "x.example")})
		},
		"is a pipe": func(t *testing.T) {
			if err := os.Remove(main); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(main, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			breakIt(t)
			done := make(chan struct{})
			go func() { defer close(done); loadsAgree(t, l, name) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the Loader waited on the file")
			}
			if _, err := l.Load(); err == nil {
				t.Fatal("a broken file was answered from the parse kept before it broke")
			}
			if l.last.Load() != kept {
				t.Error("a failed read replaced the parse kept")
			}
			_ = os.Chmod(main, 0o600)
			_ = os.Remove(main)
			writeFiles(t, dir, map[string]string{"config.yaml": good})
			loadsAgree(t, l, "good again")
			if l.last.Load() != kept {
				t.Error("the bytes the parse was made from were parsed again")
			}
		})
	}
}

// Nothing a caller does to an answer reaches the next one: every map and list
// reachable from it is rewritten, and the next answer, a reuse, is Load's.
func TestNothingALoaderKeepsIsHandedOut(t *testing.T) {
	layout(t, map[string]string{"config.yaml": "theme:\n  primary: \"#112233\"\n" +
		"plugins:\n  db:\n    tags: [a, b]\n    nested: {list: [x]}\n" +
		"roles:\n  ops:\n    grants: [db]\n" +
		"dashboard:\n  tiles:\n    - id: db.query\n      with: {limit: [1, 2]}\n" +
		profile("base", "b.example")})
	l := &Loader{}
	// The second answer is the one made from the parse kept, and the one a
	// caller could otherwise reach it through.
	for range 2 {
		answer, err := l.Load()
		if err != nil {
			t.Fatal(err)
		}
		scribble(reflect.ValueOf(&answer).Elem())
	}
	loadsAgree(t, l, "after callers rewrote their answers")
}

// scribble rewrites every map entry and list element reachable from v.
func scribble(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				scribble(v.Field(i))
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(v.MapIndex(k))
			scribble(e)
			if e.Kind() == reflect.String {
				e.SetString("SCRIBBLED")
			}
			v.SetMapIndex(k, e)
		}
	case reflect.Slice:
		for i := range v.Len() {
			if v.Index(i).Kind() == reflect.String {
				v.Index(i).SetString("SCRIBBLED")
			}
			scribble(v.Index(i))
		}
	case reflect.Interface:
		if !v.IsNil() {
			inner := reflect.New(v.Elem().Type()).Elem()
			inner.Set(v.Elem())
			scribble(inner)
		}
	case reflect.Pointer:
		if !v.IsNil() {
			scribble(v.Elem())
		}
	}
}

// A clone equals what it copies and shares no map, slice or pointer with it,
// for every field of every type under Config, exported or not: a field added
// later without a line in clone fails here by its path.
func TestACloneSharesNothingWithItsOriginal(t *testing.T) {
	var full Config
	fillAll(reflect.ValueOf(&full).Elem(), 0)
	copied := full.clone()
	if !reflect.DeepEqual(copied, full) {
		t.Fatal("the clone is not equal to what it copies")
	}
	if shared := sharedRefs(reflect.ValueOf(full), reflect.ValueOf(copied), "Config"); len(shared) > 0 {
		t.Fatalf("the clone shares %v", shared)
	}
}

// Every shape the YAML decoder hands back under a profile's set:, copied: the
// list of what it produces is the decoder's and not this package's, and a
// decoder bumped to a new shape is what this is here to catch.
func TestEveryShapeTheDecoderHandsBackIsCopied(t *testing.T) {
	layout(t, map[string]string{"config.yaml": `profiles:
  zoo:
    plugins:
      db:
        set:
          string: text
          int: 7
          big: 18446744073709551615
          negative: -3
          float: 1.5
          inf: .inf
          bool: true
          null: ~
          time: 2026-11-12T10:00:00Z
          binary: !!binary aGVsbG8=
          list: [a, [b, c], {d: e}]
          map: {k: {l: [m]}}
          numbers: {1: one, true: yes}
          merged:
            <<: {inherited: x}
            own: y
          set: !!set {a, b}
          tagged: !custom value
          block: |
            two
            lines
`})
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	copied := cfg.clone()
	if shared := sharedRefs(reflect.ValueOf(cfg), reflect.ValueOf(copied), "Config"); len(shared) > 0 {
		t.Fatalf("the clone shares %v", shared)
	}
	if !reflect.DeepEqual(copied, cfg) {
		t.Fatal("the clone is not equal to what it copies")
	}
	if v, _ := copied.Profiles["zoo"].Plugins["db"].Set["inf"].(float64); !math.IsInf(v, 1) {
		t.Errorf("inf = %v", copied.Profiles["zoo"].Plugins["db"].Set["inf"])
	}
}

// Loads that run while the file is replaced each see one whole version or an
// error, never one version's text under the other's parse, and what each caller
// does to its answer is its own; once the writer stops, the answer is Load's.
func TestLoadsInParallelSeeWholeVersions(t *testing.T) {
	dir := layout(t, map[string]string{"config.yaml": "output: json\n" + profile("base", "one.example")})
	path := filepath.Join(dir, "config.yaml")
	versions := map[string]string{"json": "one.example", "yaml": "two.example"}
	texts := [][]byte{[]byte(read(t, path)), []byte("output: yaml\n" + profile("base", "two.example"))}
	l := &Loader{}
	ctx, stop := context.WithCancel(context.Background())
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; ctx.Err() == nil; i++ {
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, texts[i%2], 0o600); err != nil {
				return
			}
			_ = os.Rename(tmp, path)
		}
	}()
	var readers sync.WaitGroup
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 200 {
				cfg, err := l.Load()
				if err != nil {
					continue
				}
				host, _ := cfg.Profiles["base"].Plugins["db"].Set["host"].(string)
				if want, ok := versions[cfg.Output]; !ok || host != want {
					t.Errorf("output %q beside host %q: two versions in one answer", cfg.Output, host)
					return
				}
				cfg.Profiles["base"].Plugins["db"].Set["host"] = "mine"
			}
		}()
	}
	readers.Wait()
	stop()
	writer.Wait()
	loadsAgree(t, l, "after the writer stopped")
}

// What a call pays for the configuration, read with Load and with a Loader
// whose files have not changed, at a small and a large number of profiles.
func BenchmarkReadingTheConfiguration(b *testing.B) {
	for _, n := range []int{10, 80} {
		var text strings.Builder
		text.WriteString("output: json\nprofiles:\n")
		for i := range n {
			fmt.Fprintf(&text, "  env-%03d:\n    note: environment %d\n    plugins:\n", i, i)
			for _, ns := range []string{"pg", "redis", "kube", "vault", "s3"} {
				fmt.Fprintf(&text, "      %s:\n        set:\n          host: %s-%d.internal\n"+
					"          tags: [a, b, c]\n        only:\n          database: [app, audit]\n", ns, ns, i)
			}
		}
		dir := b.TempDir()
		b.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
		b.Setenv("RTA_OUTPUT", "")
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(text.String()), 0o600); err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("Load/%d-profiles", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Load(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("Loader/%d-profiles", n), func(b *testing.B) {
			l := &Loader{}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := l.Load(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// link makes newname a symbolic link to target, making its directory.
func link(t *testing.T, target, newname string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(newname), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, newname); err != nil {
		t.Fatal(err)
	}
}

// fillAll gives every field, exported or not, a value: one entry in every map,
// one element in every slice, an element behind every pointer, and a nested
// map, list and byte string in every interface.
func fillAll(v reflect.Value, depth int) {
	if depth > 20 {
		return
	}
	if !v.CanSet() {
		v = reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			fillAll(v.Field(i), depth+1)
		}
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k := reflect.New(v.Type().Key()).Elem()
		fillAll(k, depth+1)
		e := reflect.New(v.Type().Elem()).Elem()
		fillAll(e, depth+1)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillAll(s.Index(0), depth+1)
		v.Set(s)
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fillAll(p.Elem(), depth+1)
		v.Set(p)
	case reflect.Interface:
		v.Set(reflect.ValueOf(map[string]any{
			"m": []any{"x", map[string]any{"y": 1}}, "b": []byte("z"),
			"k": map[any]any{1: []any{"w"}}, "p": &[]string{"q"},
		}))
	case reflect.String:
		v.SetString("s")
	case reflect.Int, reflect.Int64:
		v.SetInt(1)
	case reflect.Bool:
		v.SetBool(true)
	}
}

// sharedRefs lists the paths at which a and b hold the same map, slice backing
// array or pointer. A *time.Location is immutable, so it is never one.
func sharedRefs(a, b reflect.Value, path string) []string {
	var out []string
	switch a.Kind() {
	case reflect.Struct:
		if a.Type() == reflect.TypeFor[time.Time]() {
			return nil
		}
		for i := range a.NumField() {
			out = append(out, sharedRefs(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name)...)
		}
	case reflect.Map:
		if a.IsNil() || a.Len() == 0 {
			return nil
		}
		if a.UnsafePointer() == b.UnsafePointer() {
			out = append(out, path)
		}
		for it := a.MapRange(); it.Next(); {
			out = append(out, sharedRefs(it.Value(), b.MapIndex(it.Key()), path+"["+fmt.Sprint(it.Key())+"]")...)
		}
	case reflect.Slice:
		if a.IsNil() || a.Len() == 0 {
			return nil
		}
		if a.UnsafePointer() == b.UnsafePointer() {
			out = append(out, path)
		}
		for i := range a.Len() {
			out = append(out, sharedRefs(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i))...)
		}
	case reflect.Pointer:
		if a.IsNil() {
			return nil
		}
		if a.UnsafePointer() == b.UnsafePointer() {
			out = append(out, path)
		}
		out = append(out, sharedRefs(a.Elem(), b.Elem(), "*"+path)...)
	case reflect.Interface:
		if a.IsNil() {
			return nil
		}
		out = append(out, sharedRefs(a.Elem(), b.Elem(), path)...)
	}
	return out
}
