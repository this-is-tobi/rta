package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The scaffold is what a stranger's first fifteen minutes actually consist
// of, so it is a working plugin rather than a skeleton with TODOs in it.
//
// The difference matters more than it sounds. A template full of `// TODO:
// implement` means the author's first run fails, and their first experience
// of the SDK is debugging generated code they did not write. A template that
// builds and answers correctly means the first run *works*, and every edit
// after that is a change to something known-good — which is also the only way
// to tell "I broke it" from "it never worked".

// namePattern is what a plugin namespace may be.
//
// It is the CLI's own grammar rather than an arbitrary restriction: the name
// becomes a cobra command (`rta <name> ...`), the prefix of every capability
// ID (`<name>.thing`), and part of a binary filename. Anything with a dot in
// it would produce a capability ID with two, which the three-segment ID rule
// already gives a different meaning.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

func checkName(name string) *view.Error {
	if namePattern.MatchString(name) {
		return nil
	}
	return view.Errorf("plugin.badname", "%q is not a usable plugin name", name).
		WithHint("lower-case letters, digits and dashes, starting with a letter — " +
			"it becomes `rta " + "<name>" + " ...`, the prefix of every capability ID, " +
			"and part of the binary filename")
}

type scaffold struct {
	Name    string // the namespace: "hello"
	Binary  string // "rta-plugin-hello"
	Module  string // go module path
	RtaPath string // local rta source for a replace directive, or ""
	RtaMod  string // rta's module path
}

const rtaModule = "github.com/this-is-tobi/rule-them-all"

// write renders the scaffold into dir, refusing to overwrite anything.
func (s scaffold) write(dir string) error {
	files := map[string]string{
		"main.go":      mainTemplate,
		"main_test.go": testTemplate,
		"go.mod":       goModTemplate,
		"README.md":    readmeTemplate,
		".gitignore":   binaryIgnoreTemplate,
	}
	// Every file is checked before any is written. A scaffold that creates
	// three files and then refuses on the fourth leaves a directory that is
	// neither empty nor a plugin, and the author has to work out which half
	// is theirs.
	for name := range files {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return view.Errorf("plugin.exists", "%s already exists", filepath.Join(dir, name)).
				WithHint("nothing was written; pass --dir to scaffold somewhere else")
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return view.Errorf("plugin.write", "creating %s: %v", dir, err)
	}
	for name, text := range files {
		tmpl, err := template.New(name).Parse(text)
		if err != nil {
			return view.Errorf("plugin.template", "%s: %v", name, err)
		}
		var b strings.Builder
		if err := tmpl.Execute(&b, s); err != nil {
			return view.Errorf("plugin.template", "%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
			return view.Errorf("plugin.write", "writing %s: %v", name, err)
		}
	}
	return nil
}

// localRta finds an rta source tree to point a `replace` directive at.
//
// It exists because rta is not published yet: `go mod tidy` in a scaffolded
// plugin cannot resolve the module, so a go.mod without a replace produces a
// plugin that does not build — which would be the first thing a stranger
// hits, in the fifteen minutes the milestone is measured on.
//
// Walking up from the working directory finds it when somebody scaffolds
// inside or beside the rta checkout, which is the case that exists today. It
// returns "" otherwise, and the caller says what to do rather than emitting a
// replace pointing at a path that is not there.
func localRta(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			if strings.Contains(string(data), "module "+rtaModule) {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// findRta locates an rta source tree for the replace directive.
//
// Two attempts, because the two ways this gets used are different. Scaffolding
// inside or beside the rta checkout is found by walking up from the working
// directory. Scaffolding somewhere else entirely, with an rta built from
// source, is found by walking up from the binary — which covers `go build -o
// ~/bin/rta ./cmd/rta` only when the binary is still in the tree, and that is
// the honest limit of guessing.
//
// It returns "" rather than a wrong answer, and the caller says what to do.
// An emitted `replace` pointing at a path that is not there would turn a
// missing convenience into a confusing build failure.
func findRta() string {
	if dir := localRta("."); dir != "" {
		return dir
	}
	if exe, err := os.Executable(); err == nil {
		if dir := localRta(filepath.Dir(exe)); dir != "" {
			return dir
		}
	}
	return ""
}

// nextSteps is what to print after scaffolding. It names the exact commands,
// because "build it and put it on your PATH" is the sentence that turns a
// fifteen-minute task into an hour.
func nextSteps(s scaffold, dir string) string {
	install := filepath.Join(os.Getenv("HOME"), ".local", "bin")
	var b strings.Builder
	fmt.Fprintf(&b, "Created %s\n\n", dir)
	fmt.Fprintf(&b, "  cd %s\n", dir)
	fmt.Fprintf(&b, "  rta plugin dev              # build it and check what rta sees\n")
	fmt.Fprintf(&b, "  rta plugin dev -- %s greet world\n\n", s.Name)
	fmt.Fprintf(&b, "To install it so every rta invocation finds it:\n\n")
	fmt.Fprintf(&b, "  go build -o %s/%s .\n\n", install, s.Binary)
	fmt.Fprintf(&b, "Anything named %s* on $PATH is a plugin; the part after the\n", pluginhost.Prefix)
	fmt.Fprintf(&b, "prefix is only a filename — the namespace comes from what the plugin declares.\n")
	if s.RtaPath == "" {
		fmt.Fprintf(&b, "\nNote: rta is not published yet and no local checkout was found, so\n")
		fmt.Fprintf(&b, "go.mod has no `replace` for %s and the build\n", rtaModule)
		fmt.Fprintf(&b, "will fail. Re-run with --rta-source <path-to-your-rta-checkout>.\n")
	}
	return b.String()
}

const mainTemplate = `// Command {{.Binary}} is an rta plugin.
//
// Build it and put it on your $PATH as {{.Binary}}; rta finds it there.
package main

import (
	"context"
	"fmt"
	"strings"

	"{{.RtaMod}}/pkg/plugin"
	"{{.RtaMod}}/pkg/sdk"
	"{{.RtaMod}}/pkg/view"
)

func main() { sdk.Serve(Plugin()) }

// Plugin returns the declaration. This is the whole contract with rta: you
// return data, and rta renders it as a CLI command, a TUI form, an MCP tool
// and JSON. Nothing below mentions which one is asking.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "{{.Name}}",
		Summary: "TODO: one line on what this plugin is for",
		Version: "0.1.0",
		Capabilities: []plugin.Capability{
			{
				ID:      "{{.Name}}.greet",
				Summary: "Greet somebody",
				// Read means this changes nothing. It is what decides
				// whether an AI agent may call it without an operator's
				// --allow-write, so it is a claim about blast radius rather
				// than a label. Write and Destructive are the others.
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "Replace this with what the capability actually does. This text is " +
					"published to AI agents as the tool description, so write it for somebody " +
					"deciding whether to call it.",
				Inputs: []plugin.Field{
					{
						Name: "name", Type: plugin.String, Positional: true, Required: true,
						Help: "who to greet",
					},
					{Name: "shout", Type: plugin.Bool, Help: "upper-case the result"},
				},
				Run: greet,
			},
		},
	}
}

func greet(_ context.Context, req plugin.Request) (view.View, error) {
	msg := fmt.Sprintf("Hello, %s!", req.String("name"))
	if req.Bool("shout") {
		msg = strings.ToUpper(msg)
	}
	// Return a view, not a formatted string: rta turns this into terminal
	// output, a TUI pane, CSV, markdown or an MCP payload. view.Table,
	// view.KeyValue, view.Tree, view.Chart and view.Sections are the others.
	return view.Text{Body: msg}, nil
}
`

const goModTemplate = `module {{.Module}}

go 1.25
{{if .RtaPath}}
// rta is not published yet, so this points at your local checkout.
// Remove it once you are building against a released version.
replace {{.RtaMod}} => {{.RtaPath}}
{{end}}
require {{.RtaMod}} v0.0.0
`

const readmeTemplate = `# {{.Binary}}

An [rta](https://github.com/this-is-tobi/rule-them-all) plugin.

## Try it

    rta plugin dev                        # build and check what rta sees
    rta plugin dev -- {{.Name}} greet world     # build and run it

## Install

    go build -o ~/.local/bin/{{.Binary}} .

Anything named ` + "`" + `rta-plugin-*` + "`" + ` on your $PATH is a plugin. The name after the
prefix is only a filename; the namespace comes from what the plugin declares,
so rta validates it and refuses a collision with an existing one.

## What you get for free

One declaration in ` + "`" + `main.go` + "`" + ` becomes:

- a CLI command — ` + "`" + `rta {{.Name}} greet world --shout` + "`" + `
- a TUI form, with completion for any input that declares ` + "`" + `Options` + "`" + ` or ` + "`" + `Suggest` + "`" + `
- an MCP tool for AI agents, gated by the capability's ` + "`" + `Safety` + "`" + `
- ` + "`" + `-o json|yaml|csv|md` + "`" + `, from the same ` + "`" + `view.View` + "`" + ` you returned

## Rules worth knowing

- **` + "`" + `Safety` + "`" + ` is a claim about blast radius**, not a label. ` + "`" + `Read` + "`" + ` is exposed to
  agents by default; ` + "`" + `Write` + "`" + ` needs the operator's ` + "`" + `--allow-write {{.Name}}` + "`" + `;
  ` + "`" + `Destructive` + "`" + ` needs an explicit per-capability allowlist and a human-issued grant.
- **Return a ` + "`" + `view.Error` + "`" + `, not a bare error**, when you can. The code is stable
  enough to branch on and the hint is what the person does next.
- **Your process is confined on macOS.** It cannot read or write rta's own data
  directory, and cannot read the usual credential locations. ` + "`" + `rta doctor` + "`" + ` prints
  the exact set.
- **Your stdin is /dev/null.** The protocol owns the real one. Ask for a secret
  with a ` + "`" + `plugin.Secret` + "`" + ` input instead of prompting.
`

const testTemplate = `package main

import (
	"testing"

	"{{.RtaMod}}/pkg/sdk/sdktest"
)

// The conformance suite rta holds its own built-ins to: the shared verb
// vocabulary, every declared view rendering in every format it claims, dry-run
// honesty on anything that writes, and the invariants that are easy to break
// by accident.
//
// It ships with the scaffold rather than being something to discover later,
// because a test that arrives with the code is a test that gets run. It found
// a built-in sending real bytes on --dry-run the first time it was pointed at
// rta's own catalogue.
func TestPlugin(t *testing.T) { sdktest.Check(t, Plugin()) }
`

// binaryIgnoreTemplate covers two different names because "go build" does:
// {{.Binary}} is what `go build -o .../{{.Binary}} .` (README's own install
// line) produces, but a bare `go build ./...` — what `make plugins` runs for
// every module under plugins/ — names its output after the directory
// instead, and a plugin scaffolded straight into plugins/<name> (the
// convention this repo's own first-party plugins use, short name rather
// than the full {{.Binary}}) gets a *different* binary from that command,
// one this file never named. Found by running `make plugins` against a real
// scaffold rather than by inspecting the template: plugins/pg had carried
// its own build artifact as a tracked file since it was written, silently,
// for exactly this reason.
const binaryIgnoreTemplate = `{{.Binary}}
{{.Name}}
`
