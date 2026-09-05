package cli

import (
	"charm.land/lipgloss/v2"

	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Markdown output exists because a finding is only worth as much as the
// hand-off it enables: an audit that a person has to re-type into a ticket
// gets re-typed wrong, or not at all.
//
// It is a *format*, not an audit feature, which is the whole point. Every one
// of the catalogue's capabilities gets it for free — `sys overview` into an
// incident write-up, `kv status` into a runbook, `note list` into a stand-up
// note — and no capability has to grow a --markdown flag of its own or learn
// what a table looks like in a second syntax.
//
// Unlike csv, which handles exactly one view type and says so, markdown
// renders the whole union. Every shape a capability can return has an obvious
// markdown equivalent, which is a decent sign the view contract is carrying
// its weight.

// renderMarkdown writes v as a self-contained markdown document.
func renderMarkdown(w io.Writer, v view.View) error {
	var b strings.Builder
	markdownView(&b, view.Redact(v), 2)
	_, err := io.WriteString(w, strings.TrimLeft(b.String(), "\n"))
	return err
}

// markdownView appends v, with any nested section titles starting at the
// given heading level.
func markdownView(b *strings.Builder, v view.View, level int) {
	switch t := v.(type) {
	case view.Text:
		markdownText(b, t)
	case view.KeyValue:
		markdownPairs(b, t)
	case view.Table:
		markdownTable(b, t)
	case view.Tree:
		markdownTree(b, t)
	case view.Chart:
		markdownChart(b, t)
	case view.Sections:
		markdownSections(b, t, level)
	}
}

// Both kinds of Text go out verbatim. A Markdown body already is markdown, and
// a plain one is prose we wrote — fencing it to preserve its line breaks would
// turn every "no notes yet" into a code sample, which reads far worse than a
// reflowed sentence does.
func markdownText(b *strings.Builder, t view.Text) {
	body := strings.TrimRight(t.Body, "\n")
	if body == "" {
		return
	}
	b.WriteString("\n" + body + "\n")
}

func markdownPairs(b *strings.Builder, kv view.KeyValue) {
	if len(kv.Pairs) == 0 {
		return
	}
	rows := make([][]string, 0, len(kv.Pairs))
	for _, p := range kv.Pairs {
		rows = append(rows, []string{p.Key, p.Value})
	}
	writeMarkdownGrid(b, []string{"Field", "Value"}, rows)
}

func markdownTable(b *strings.Builder, t view.Table) {
	if len(t.Columns) == 0 {
		return
	}
	head := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		head[i] = c.Name
	}
	writeMarkdownGrid(b, head, t.Rows)

	// A paginated table that did not say so would be read as the whole set,
	// which is the kind of quiet wrongness a report must not carry.
	if t.Total > len(t.Rows) {
		b.WriteString(fmt.Sprintf("\n_Showing %d of %d rows._\n",
			len(t.Rows), t.Total))
	}
}

func markdownTree(b *strings.Builder, t view.Tree) {
	if len(t.Roots) == 0 {
		return
	}
	b.WriteString("\n")
	var walk func(nodes []view.Node, depth int)
	walk = func(nodes []view.Node, depth int) {
		for _, n := range nodes {
			line := strings.Repeat("  ", depth) + "- " + inlineMarkdown(n.Label)
			if n.Detail != "" {
				line += " — " + inlineMarkdown(n.Detail)
			}
			b.WriteString(line + "\n")
			walk(n.Children, depth+1)
		}
	}
	walk(t.Roots, 0)
}

// Markdown has no chart, and pretending otherwise with block characters would
// produce something that renders as garbage everywhere it is pasted. The
// numbers behind the picture are what a reader can act on anyway.
func markdownChart(b *strings.Builder, c view.Chart) {
	if len(c.Series) == 0 {
		return
	}
	unit := func(f float64) string {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if c.Unit != "" {
			return s + c.Unit
		}
		return s
	}
	if c.Kind == view.ChartBar {
		rows := make([][]string, 0, len(c.Series))
		for _, s := range c.Series {
			v := ""
			if len(s.Points) > 0 {
				v = unit(s.Points[0])
			}
			rows = append(rows, []string{s.Name, v})
		}
		writeMarkdownGrid(b, []string{"Series", "Value"}, rows)
		return
	}
	// A line series is a sequence, and a report wants its shape, not its
	// every sample: the range it covered and where it ended up.
	rows := make([][]string, 0, len(c.Series))
	for _, s := range c.Series {
		if len(s.Points) == 0 {
			rows = append(rows, []string{s.Name, "", "", "", "0"})
			continue
		}
		min, max := s.Points[0], s.Points[0]
		for _, p := range s.Points[1:] {
			if p < min {
				min = p
			}
			if p > max {
				max = p
			}
		}
		rows = append(rows, []string{
			s.Name, unit(min), unit(max), unit(s.Points[len(s.Points)-1]),
			strconv.Itoa(len(s.Points)),
		})
	}
	writeMarkdownGrid(b, []string{"Series", "Min", "Max", "Last", "Points"}, rows)
}

func markdownSections(b *strings.Builder, s view.Sections, level int) {
	for _, item := range s.Items {
		if item.View == nil {
			continue
		}
		// Title, not Key: the id is what a script addresses the section by,
		// and a report that printed "cpu_temp" where the page says
		// "Temperature" would be a worse document for the sake of a handle
		// no reader has any use for.
		// Escaped for the same reason Code just below is: a section title
		// arrives from the wire verbatim, and a newline in one ends the
		// heading and starts writing blocks of its own.
		if item.Title != "" {
			b.WriteString("\n" + strings.Repeat("#", clampLevel(level)) + " " + inlineMarkdown(item.Title) + "\n")
		}
		markdownView(b, item.View, level+1)
	}
	markdownWarnings(b, s.Warnings)
}

// A section that failed is dropped from the page, so a report missing half
// its content still reads as a finished one — and this is the format that
// gets mailed and pasted into a ticket, where nobody can re-run the command
// to find out. A blockquote is the subordinate form markdown already has,
// the same one markdownError uses for a hint: impossible to miss beside the
// headings, impossible to mistake for content.
func markdownWarnings(b *strings.Builder, warnings []view.Error) {
	if len(warnings) == 0 {
		return
	}
	b.WriteString("\n> **Warnings**\n>\n")
	for _, e := range warnings {
		// inlineMarkdown, because a newline in the message would end the
		// blockquote and leave the rest of the warnings as body prose.
		//
		// Code gets the same treatment, and the reason it looks like it
		// should not need it is the trap: an error code reads as an
		// identifier, but it is per-call output a plugin writes, not
		// declared text. Validate's identifier grammar constrains what a
		// plugin registers — capability ids, field names, config keys —
		// and never sees this, wire.ErrorFromProto copies it off the wire
		// byte for byte, and sdktest's code conventions are a conformance
		// test an author opts into rather than a gate anything passes
		// through. pkg/view/strings.go made exactly this call once already
		// when it stopped exempting Code from cleaning; this is the same
		// ruling at the renderer, where a backtick closes the span early.
		line := "> - `" + inlineMarkdown(e.Code) + "` — " + inlineMarkdown(e.Message)
		if e.Hint != "" {
			line += " (" + inlineMarkdown(e.Hint) + ")"
		}
		b.WriteString(line + "\n")
	}
}

// Markdown stops at six levels; past that a heading silently stops being one.
// Deeply nested pages flatten into the last real level rather than emitting
// text that looks like a heading and is not.
func clampLevel(level int) int {
	if level < 1 {
		return 1
	}
	if level > 6 {
		return 6
	}
	return level
}

// writeMarkdownGrid emits a GitHub-flavoured table. Cells are padded to an
// even width: the rendered result is identical either way, but the source is
// what a person reads in the pull request that carries the report.
func writeMarkdownGrid(b *strings.Builder, header []string, rows [][]string) {
	head := make([]string, len(header))
	widths := make([]int, len(header))
	for i, h := range header {
		head[i] = inlineMarkdown(h)
		widths[i] = lipgloss.Width(head[i])
	}
	cells := make([][]string, 0, len(rows))
	for _, row := range rows {
		out := make([]string, len(header))
		for i := range out {
			// Rows are not guaranteed to match the column list, the same
			// tolerance view.Redact applies. A missing cell is empty; an
			// extra one has no column to sit under and is dropped.
			if i < len(row) {
				out[i] = inlineMarkdown(row[i])
			}
			if n := lipgloss.Width(out[i]); n > widths[i] {
				widths[i] = n
			}
		}
		cells = append(cells, out)
	}

	line := func(vals []string) {
		b.WriteString("|")
		for i, v := range vals {
			b.WriteString(" " + v + strings.Repeat(" ", widths[i]-lipgloss.Width(v)) + " |")
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	line(head)
	b.WriteString("|")
	for _, w := range widths {
		b.WriteString(strings.Repeat("-", w+2) + "|")
	}
	b.WriteString("\n")
	for _, row := range cells {
		line(row)
	}
}

// inlineMarkdown makes a cell safe to sit inline in a document assembled
// around data from somewhere else — an HTTP body, a plugin's own error text,
// a database row. A pipe would end a table cell early and shift every value
// after it into the wrong column, silently, and only for the rows that
// happen to contain one; [ ] and a backtick can open a live link or a code
// span the source never intended; and < can be read as raw HTML by whatever
// renders the document next (a wiki, a static-site generator, a ticket
// tracker without its own sanitizer). Each gets the same treatment GFM uses
// for its own inline escaping: a leading backslash, so the character prints
// instead of taking on its syntactic meaning.
func inlineMarkdown(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "[", "\\[")
	s = strings.ReplaceAll(s, "]", "\\]")
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "<", "\\<")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "<br>")
	return s
}

// markdownError writes an error as markdown, so that a redirected report
// carries its failure instead of an empty file.
func markdownError(w io.Writer, e *view.Error) error {
	var b strings.Builder
	// inlineMarkdown, same as markdownWarnings just above: AsError puts a
	// foreign error's own text into Message, so a hostile server's response
	// is as much a display channel here as it is in the body of a report —
	// and this one lands unquoted, not behind a blockquote, the moment a
	// newline in that text ends the line it started on.
	b.WriteString("**Error** `" + inlineMarkdown(e.Code) + "` — " + inlineMarkdown(e.Message) + "\n")
	if e.Hint != "" {
		b.WriteString("\n> " + inlineMarkdown(e.Hint) + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
