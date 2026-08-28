package audit

import (
	"strings"
)

// The TOML manifests — Cargo and the pyproject family — which share a
// syntax and differ in where the dependencies live.

// tomlGraph reads the [[package]] records uv, Poetry and Cargo share, and the
// three different ways they write an edge down:
//
//	Cargo   dependencies = ["bar", "baz 1.0.0"]
//	uv      dependencies = [{ name = "click" }]
//	Poetry  [package.dependencies]\nclick = ">=8.1.3"
//
// One scanner rather than three, because the framing — a record opened by
// [[package]], a name, a source that says whether it is the project's own —
// is identical, and three near-identical scanners is how two of them come to
// disagree about what closes a record.
func tomlGraph(text, ecosystem string, flavour tomlFlavour) graph {
	g := newGraph()
	var all []string
	name := ""
	hasSource, sourceKeys, sourceType := false, []string(nil), ""
	var deps []string
	inArray, inPoetryDeps := false, false

	flush := func() {
		if name != "" {
			from := ref(ecosystem, name)
			// **Which records are "the project" is a per-format question**, and
			// answering it for all three at once got poetry exactly backwards.
			//
			// Cargo's workspace member is the entry with no source; uv's root
			// is `source = { virtual = "." }` and its workspace members are
			// `{ editable = "…" }` — for both, what the record requires is what
			// the project asked for. **poetry.lock has no such record at all**,
			// which is why this file's own table says its direct set lives in
			// pyproject.toml. Its local entries are *path dependencies* — a
			// sibling library, not the project — so treating one as the project
			// put that library's dependencies into the direct set and, worse,
			// switched on stateIndirect for the whole file, which then marked
			// every genuine top-level dependency "indirect". Both halves of the
			// answer inverted, from a file that states neither.
			project := false
			switch flavour {
			case cargoLock:
				project = !hasSource
			case uvLock:
				project = hasAny(sourceKeys, "virtual", "editable")
			}
			local := project || sourceType == "directory" || sourceType == "file" ||
				hasAny(sourceKeys, "directory", "path", "file")
			switch {
			case project:
				for _, d := range deps {
					g.direct[ref(ecosystem, d)] = true
				}
			default:
				// A local library is still a node with edges — it is how its
				// own dependencies got here — it is just not on any registry,
				// so it is not something to count or ask OSV about.
				if !local {
					all = append(all, from)
				}
				for _, d := range deps {
					g.require(from, ref(ecosystem, d))
				}
			}
		}
		name, hasSource, sourceKeys, sourceType, deps = "", false, nil, "", nil
		inArray, inPoetryDeps = false, false
	}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if inArray {
			if line == "]" || strings.HasPrefix(line, "]") {
				inArray = false
				continue
			}
			deps = append(deps, tomlDepNames(line)...)
			continue
		}
		switch {
		case line == "[[package]]":
			flush()
			continue
		case strings.HasPrefix(line, "[package.dependencies]"):
			inPoetryDeps = true
			continue
		case strings.HasPrefix(line, "[package.") || strings.HasPrefix(line, "[[package."):
			inPoetryDeps = false
			continue
		case strings.HasPrefix(line, "["):
			flush()
			continue
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if inPoetryDeps {
			// Every key in this subtable is a package name; the value is the
			// range, which is exactly what this does not try to resolve.
			deps = append(deps, strings.Trim(key, `"'`))
			continue
		}
		switch key {
		case "name":
			if name == "" {
				name = strings.Trim(val, `"'`)
			}
		case "source":
			hasSource = true
			sourceKeys = inlineKeys(val)
		case "dependencies":
			if val == "[" || val == "[]" {
				inArray = val == "["
				continue
			}
			deps = append(deps, tomlDepNames(val)...)
		case "type":
			// Poetry's [package.source] discriminator, read as a whole value.
			sourceType = strings.Trim(val, `"'`)
		case "path", "editable", "virtual", "directory":
			sourceKeys = append(sourceKeys, key)
		}
	}
	flush()
	// **Cargo and uv state the project's own dependencies; poetry does not.**
	// The gate used to be `len(g.direct) > 0`, which reads "some record in this
	// file happened to be local" as "this format states the direct set
	// exhaustively" — precisely what stateIndirect's own contract forbids. One
	// path dependency in a poetry monorepo was enough to make the whole file
	// claim a split it never states.
	if flavour != poetryLock {
		g.stateIndirect(all)
	}
	return g
}

// tomlFlavour names which of the three formats sharing this scanner is being
// read. They agree on the framing — a record opened by [[package]], a name, a
// source — and disagree about the one thing this file most has to get right:
// which record, if any, is the project itself.
type tomlFlavour int

const (
	cargoLock tomlFlavour = iota
	uvLock
	poetryLock
)

func hasAny(keys []string, want ...string) bool {
	for _, k := range keys {
		for _, w := range want {
			if k == w {
				return true
			}
		}
	}
	return false
}

// tomlDepNames reads the package names out of one line of a `dependencies`
// array, in either shape it comes in.
func tomlDepNames(line string) []string {
	line = strings.TrimSuffix(strings.TrimSpace(line), ",")
	if strings.Contains(line, "name") && strings.Contains(line, "{") {
		// uv: `{ name = "click" }`, possibly several on one line.
		var out []string
		for rest := line; ; {
			i := strings.Index(rest, "name")
			if i < 0 {
				return out
			}
			rest = rest[i+len("name"):]
			_, after, ok := strings.Cut(rest, "=")
			if !ok {
				return out
			}
			after = strings.TrimSpace(after)
			if len(after) == 0 || (after[0] != '"' && after[0] != '\'') {
				rest = after
				continue
			}
			quote := after[0]
			end := strings.IndexByte(after[1:], quote)
			if end < 0 {
				return out
			}
			if n := after[1 : 1+end]; n != "" {
				out = append(out, n)
			}
			rest = after[1+end:]
		}
	}
	// Cargo: `"bar"` or `"bar 1.0.0 (registry+https://…)"`, and an inline
	// array puts several on one line.
	var out []string
	for _, part := range strings.Split(strings.Trim(line, "[]"), ",") {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part == "" {
			continue
		}
		if fields := strings.Fields(part); len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return out
}
