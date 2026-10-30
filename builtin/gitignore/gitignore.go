package gitignore

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// maxTemplates bounds one call. Nobody composes thirty templates into a file
// they mean to read, and the bound is what keeps a call from being thirty
// requests and a long answer by accident.
const maxTemplates = 30

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "gitignore",
		Summary: "Starter .gitignore files from github/gitignore: find templates, print them as one file",
		Capabilities: []plugin.Capability{
			listCapability(),
			showCapability(),
		},
	}
}

// Both capabilities reach off the box, so neither is a tile of the automatic
// dashboard: that runs every Read that needs no input, unasked, every few
// seconds, and a public host is not asked the same question a thousand times
// an hour on somebody's behalf. Naming one in the dashboard config is the
// asking, the way it is for eol.

func listCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "gitignore.list",
		Summary:    "List the templates github/gitignore offers",
		Safety:     plugin.Read,
		Idempotent: true,
		NoPreview:  true,
		Keywords:   []string{"ignore", "template", "generate"},
		Description: "Lists the templates in the github/gitignore repository, with the name `gitignore.show` " +
			"takes for each: the main ones for languages, frameworks and tools, the global ones for " +
			"operating systems and editors, and the community ones. A term narrows the list to " +
			"the templates whose name contains it, or to one kind when the term is main, global " +
			"or community. Without one the first templates are listed and the total says how " +
			"many there are; `limit` shows more.",
		Examples: []plugin.Example{
			{Title: "everything about node", Inputs: map[string]any{"term": "node"}},
			{Title: "the global templates", Inputs: map[string]any{"kind": "global", "limit": 100}},
		},
		Inputs: []plugin.Field{
			{Name: "term", Type: plugin.String, Positional: true,
				Help: "part of a template's name, or a kind (main, global, community) to list that kind: go, node, mac, global"},
			{Name: "kind", Type: plugin.String, Options: []string{string(kindMain), string(kindGlobal), string(kindCommunity)},
				Help: "only this kind: main (languages and tools), global (systems and editors), community"},
			// Bounded because the repository holds three hundred templates, and a
			// model that leaves the term out to see what there is pays, in
			// tokens, for all of them to read the first screen of it.
			{Name: "limit", Type: plugin.Int, Config: "list.limit", Default: 50, Min: 1, Max: 1000,
				Help: "how many templates to list"},
		},
		Run: runList,
	}
}

func showCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "gitignore.show",
		Summary:    "Print github/gitignore templates as one .gitignore file",
		Safety:     plugin.Read,
		Idempotent: true,
		NoPreview:  true,
		Keywords:   []string{"ignore", "template", "generate"},
		Description: "Fetches the named templates from github/gitignore and prints them as one file: a " +
			"section for each, headed by its name, in the order given. No line is merged, deduplicated " +
			"or dropped, so every section can be compared with upstream, and each file is checked " +
			"against the hash the repository lists for it before it is used. A file that holds a " +
			"control character other than a tab, a line feed or a carriage return is refused rather " +
			"than printed with it left out. The answer is text: the exact JSON form carries each " +
			"file as it is, and every other form leaves out the control characters a terminal would " +
			"act on, a carriage return among them, so a pattern that holds one, as macOS's Icon " +
			"does, reads differently there. Names are matched without regard to case; a qualified " +
			"name such as Global/macOS names one template when a bare name could mean two, and a " +
			"few nicknames (vscode, golang, mac) are understood. `gitignore.list` shows what exists.",
		Agent: "Prints the named github/gitignore templates as one .gitignore file, each file's text " +
			"under its own ### name ### line. `templates` takes names as gitignore.list shows them, in " +
			"any case, or a qualified name such as Global/macOS when a bare name could mean two.",
		Examples: []plugin.Example{
			{Title: "a Go project on a Mac", Inputs: map[string]any{"templates": []string{"Go", "macOS"}}},
			{Title: "a Node project edited in VS Code", Inputs: map[string]any{"templates": []string{"Node", "VisualStudioCode"}}},
		},
		Inputs: []plugin.Field{
			{Name: "templates", Type: plugin.StringSlice, Positional: true, Required: true,
				Help: "template names as `gitignore.list` shows them: Go, Node, macOS; or a qualified name, Global/macOS, when a name could mean two"},
		},
		Run: runShow,
	}
}

func runList(ctx context.Context, req plugin.Request) (view.View, error) {
	return runListAt(ctx, req, upstream)
}

func runListAt(ctx context.Context, req plugin.Request, src *source) (view.View, error) {
	cat, _, verr := src.catalogue(ctx)
	if verr != nil {
		return nil, verr
	}
	term := strings.ToLower(strings.TrimSpace(req.String("term")))
	only := req.String("kind")

	t := view.Table{Columns: []view.Column{{Name: "Template"}, {Name: "Kind"}}}
	for _, e := range cat.Entries {
		if only != "" && string(e.Kind) != only {
			continue
		}
		if term != "" && !e.matches(term) {
			continue
		}
		// A name upstream chose goes through ListedName like any name a stranger
		// chose: shown as it is when it reads as itself, quoted when it would
		// not.
		t.Rows = append(t.Rows, []string{plugin.ListedName(e.Ref), string(e.Kind)})
	}
	t.Total = len(t.Rows)
	if limit := req.Int("limit"); limit > 0 && len(t.Rows) > limit {
		t.Rows = t.Rows[:limit]
	}
	if len(t.Rows) == 0 {
		if term != "" {
			t.Empty = "no template matches " + strconv.Quote(term) + " — " +
				req.Surface().CapabilityName("gitignore.list") + " with no term lists them all"
		} else {
			t.Empty = "no template of that kind"
		}
	}
	return t, nil
}

func runShow(ctx context.Context, req plugin.Request) (view.View, error) {
	return runShowAt(ctx, req, upstream)
}

func runShowAt(ctx context.Context, req plugin.Request, src *source) (view.View, error) {
	asked := splitNames(req.StringSlice("templates"))
	if len(asked) == 0 {
		return nil, view.Errorf("gitignore.templates.none", "no template was named").
			WithHint(req.Surface().CapabilityName("gitignore.list") + " lists the ones that exist").WithInput("templates")
	}
	if len(asked) > maxTemplates {
		return nil, view.Errorf("gitignore.templates.toomany", "%d templates were named, and at most %d are printed in one file", len(asked), maxTemplates).
			WithInput("templates")
	}

	// A listing a long-lived process kept can be older than the repository: a
	// file it names is gone or no longer the file it describes. Asking once
	// more costs one request and is what the person would have to do by hand;
	// a listing read a moment ago is not asked again, since a mismatch there
	// is a host that lags behind its own listing, which more asking now does
	// not cure.
	for retried := false; ; retried = true {
		cat, cached, verr := src.catalogue(ctx)
		if verr != nil {
			return nil, verr
		}
		picked, verr := resolveAll(req, cat, asked)
		if verr != nil {
			return nil, verr
		}
		bodies, verr := fetchAll(ctx, src, picked)
		if verr == nil {
			return view.Text{Body: compose(cat, picked, bodies)}, nil
		}
		if !contradictsListing(verr) {
			return nil, verr
		}
		src.forget(cat)
		if retried || !cached {
			return nil, verr
		}
	}
}

// resolveAll is the templates the typed names mean, in the order asked and each
// once, or the refusal that says which name meant none or several.
func resolveAll(req plugin.Request, cat *catalogue, asked []string) ([]entry, *view.Error) {
	var picked []entry
	seen := map[string]bool{}
	for _, name := range asked {
		e, how, refs := cat.resolve(name)
		switch how {
		case ambiguous:
			return nil, view.Errorf("gitignore.template.ambiguous", "%q could be %s", name, strings.Join(refs, " or ")).
				WithHint("give the qualified name of the one meant, as in " + refs[0]).WithInput("templates")
		case unknown:
			hint := req.Surface().CapabilityName("gitignore.list") + " lists the ones that exist"
			if len(refs) > 0 {
				hint = "did you mean " + strings.Join(refs, ", ") + "? " + hint
			}
			return nil, view.Errorf("gitignore.template.unknown", "no template is called %q", name).WithHint(hint).WithInput("templates")
		}
		if !seen[e.Path] {
			seen[e.Path] = true
			picked = append(picked, e)
		}
	}
	return picked, nil
}

// contradictsListing is whether a failure to read a template says the listing
// it was read from is out of date: the file is not the one the listing
// described, or it is not there at all. Only a template's own request can fail
// that way here, since the listing was read before any file was asked for.
func contradictsListing(verr *view.Error) bool {
	return verr.Code == codeTemplateChanged || verr.Code == codeNotFound
}

// splitNames is the names asked for, in the order asked. A name may also be
// several, joined by commas, which is how the web services that do this take
// them and so how somebody who has used one types them.
func splitNames(given []string) []string {
	var out []string
	for _, g := range given {
		for _, n := range strings.Split(g, ",") {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// fetchAll reads the files a few at a time. The answer is the same in whatever
// order they arrive: bodies are kept by position, and when more than one fails
// it is the first of them, in the order asked, that is reported.
func fetchAll(ctx context.Context, src *source, picked []entry) ([][]byte, *view.Error) {
	const inFlight = 4
	bodies := make([][]byte, len(picked))
	errs := make([]*view.Error, len(picked))
	gate := make(chan struct{}, inFlight)
	var wg sync.WaitGroup
	for i, e := range picked {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			bodies[i], errs[i] = src.template(ctx, e)
		}()
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return bodies, nil
}

// compose is the file: two comment lines that say where it came from, then each
// template under a line that names it, as it is. A body that does not end in a
// newline is given one, since the next section's heading would otherwise land
// on its last line and turn a pattern into a different one.
//
// What the second comment line claims is only what holds on every surface. The
// answer is text, and every renderer but `-o json` removes the control
// characters a terminal would act on, a carriage return among them, so a
// section is not claimed to be the file byte for byte. Templates that hold one
// exist: macOS's has one inside the brackets of its Icon and HFS+ patterns,
// which read differently without it, and two others end their lines with it.
func compose(cat *catalogue, picked []entry, bodies [][]byte) string {
	names := make([]string, len(picked))
	for i, e := range picked {
		names[i] = e.Ref
	}
	var b strings.Builder
	b.WriteString("# github/gitignore, tree " + shortID(cat.Tree) + ": " + strings.Join(names, ", ") + "\n")
	b.WriteString("# Each section is the text of one file from that tree, with nothing merged or deduplicated.\n")
	for i, e := range picked {
		b.WriteString("\n### " + e.Ref + " ###\n")
		b.Write(bodies[i])
		if len(bodies[i]) == 0 || bodies[i][len(bodies[i])-1] != '\n' {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func shortID(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}
