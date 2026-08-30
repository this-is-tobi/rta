// Package cli renders views for the terminal and for pipes. It owns all
// styling decisions — views themselves carry only data and semantic hints.
package cli

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/goccy/go-yaml"
	"github.com/guptarohit/asciigraph"

	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Format selects an output encoding.
type Format string

const (
	Pretty Format = "pretty" // human output, styled when attached to a TTY
	JSON   Format = "json"
	YAML   Format = "yaml"
	CSV    Format = "csv" // tables only
	// Markdown renders every view type, for reports that leave the terminal:
	// a ticket, a pull request, a hand-off to whoever has to fix it.
	Markdown Format = "md"
)

// Formats is every value --output accepts, with what each is for.
//
// Beside ParseFormat rather than written out at the flag, so the list somebody
// is offered and the list that is accepted are the same list. Tab-separated
// descriptions, the convention Field.Suggest uses.
func Formats() []string {
	return []string{
		string(Pretty) + "\tstyled for a terminal",
		string(JSON) + "\tfor jq and scripts",
		string(YAML) + "\tfor a config file or a diff",
		string(CSV) + "\ttables only, for a spreadsheet",
		string(Markdown) + "\tfor a ticket or a pull request",
	}
}

// ParseFormat validates a --output value.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case Pretty, JSON, YAML, CSV, Markdown:
		return Format(s), nil
	case "markdown":
		// The long spelling is the one people type first.
		return Markdown, nil
	case "":
		return Pretty, nil
	default:
		return "", fmt.Errorf("unknown output format %q (want pretty|json|yaml|csv|md)", s)
	}
}

// Options configures rendering.
type Options struct {
	Format  Format
	NoColor bool
	// Width, when > 0, constrains pretty output to that many cells: tables
	// shrink, charts adapt, nothing wraps raggedly. 0 means natural width
	// (the terminal wraps). TUI panes always set it.
	Width int
	// Fill turns Width from a ceiling into a target: a table narrower than
	// the space it was given grows to use it.
	//
	// It is a host's decision, not a table's. A command writing to a pipe or
	// a scrollback wants its natural width — a four-column table stretched
	// across a 200-column terminal is harder to read, not easier. A pane in
	// a bordered box is the opposite: the box is drawn at a fixed size
	// whatever the content does, so a table floating at half its width is
	// just wasted room, and it is exactly the room a task title needs.
	Fill bool
	// Highlight, when > 0, accents that table row (1-based). Interactive
	// hosts use it for row selection; 0 renders tables uniformly.
	Highlight int
	// Notes is where the renderer reports things that must not pollute the
	// output stream itself. nil discards them, which is the right default
	// for a host that has nowhere sensible to put them (a TUI pane).
	//
	// It exists because csv had no way to say a result was truncated. The
	// count cannot go in the body: a trailing "# 3 of 744 rows" line is a
	// row, and every consumer that does not know the convention parses it as
	// data. So stdout stays pure csv and the count goes here, which the CLI
	// wires to stderr.
	Notes io.Writer
}

// Render writes v to w in the requested format.
func Render(w io.Writer, v view.View, opts Options) error {
	// json is the one byte-exact channel; see sanitize.
	if opts.Format != JSON {
		v = sanitize(v)
	}
	switch opts.Format {
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(view.Envelope{View: view.Redact(v)})
	case YAML:
		m, err := view.ToMap(view.Redact(v))
		if err != nil {
			return err
		}
		// yamlSafe, because the encoder writes a tab into a plain scalar and
		// every parser folds it away — see yamlsafe.go.
		out, err := yaml.Marshal(yamlSafe(m))
		if err != nil {
			return err
		}
		_, err = w.Write(out)
		return err
	case CSV:
		return renderCSV(w, v, opts)
	case Markdown:
		return renderMarkdown(w, v)
	default:
		return renderPretty(profiled(w, opts), v, opts)
	}
}

// profiled wraps terminal writers so truecolor styles downgrade to the
// terminal's actual color profile. Buffers (the TUI render path) pass
// through untouched — bubbletea downsamples its own output.
func profiled(w io.Writer, opts Options) io.Writer {
	if opts.NoColor {
		return w
	}
	if f, ok := w.(*os.File); ok {
		return colorprofile.NewWriter(f, os.Environ())
	}
	return w
}

func renderCSV(w io.Writer, v view.View, opts Options) error {
	// Redact before the type assertion, not after: every path that turns a
	// View into bytes a caller can read runs it, and a format that opts out
	// is a format that leaks the moment a table starts carrying a secret.
	t, ok := view.Redact(v).(view.Table)
	if !ok {
		return fmt.Errorf("csv output only supports table views, got %q", view.TypeOf(v))
	}
	cw := csv.NewWriter(w)
	header := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = c.Name
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	if err := cw.WriteAll(t.Rows); err != nil {
		return err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	// Pretty output prints "3 of 744 rows" as a footer and json, yaml and
	// mcp all carry `total`; csv used to write the three rows and stop, so a
	// page of a paginated result came out byte-identical to a complete
	// three-row answer. The format whose only reason to exist is being fed to
	// another program was the one that could not tell that program it had
	// been handed a fraction of the data.
	if t.Total > len(t.Rows) && opts.Notes != nil {
		// A failed note must not fail the render: the rows are already out
		// and correct, and a closed stderr is not a reason to report the
		// query as broken.
		_, _ = fmt.Fprintf(opts.Notes, "# %d of %d rows\n", len(t.Rows), t.Total)
	}
	if more := continues(t); more != "" && opts.Notes != nil {
		_, _ = fmt.Fprintf(opts.Notes, "# %s\n", more)
	}
	return nil
}

func renderPretty(w io.Writer, v view.View, opts Options) error {
	st := newStyles(opts.NoColor)
	st.width = opts.Width
	st.fill = opts.Fill
	switch t := view.Redact(v).(type) {
	case view.Text:
		if t.Markdown {
			return prettyMarkdown(w, t.Body, st)
		}
		_, err := fmt.Fprintln(w, wrap(strings.TrimRight(t.Body, "\n"), st.width, ""))
		return err
	case view.KeyValue:
		return prettyKeyValue(w, t, st)
	case view.Table:
		return prettyTable(w, t, st, opts.Highlight)
	case view.Tree:
		return prettyTree(w, t, st)
	case view.Chart:
		return prettyChart(w, t, st)
	case view.Sections:
		return prettySections(w, t, st, opts)
	case *view.Error:
		return RenderError(w, t, opts)
	default:
		return fmt.Errorf("no pretty renderer for view %q", view.TypeOf(v))
	}
}

// prettySections renders a composite view: each section gets an accented
// heading rule, then its own view rendered by the same pipeline. Sections
// nest, so composites of composites just work.
func prettySections(w io.Writer, s view.Sections, st styles, opts Options) error {
	for i, item := range s.Items {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		// Title, not Key: the id exists so a script can address a section
		// whose wording is free to change, and printing it here would show
		// the reader "cpu_temp" where the page says "Temperature".
		title := strings.ToUpper(item.Title)
		// A heading rule that stops at the available width, so sections read
		// as bands rather than floating labels.
		rule := ""
		if st.width > 0 {
			if pad := st.width - lipgloss.Width(title) - 1; pad > 0 {
				rule = " " + strings.Repeat("─", pad)
			}
		}
		if _, err := fmt.Fprintln(w, st.header.Render(title)+st.faint.Render(rule)); err != nil {
			return err
		}
		if item.View == nil {
			continue
		}
		if err := renderPretty(w, item.View, opts); err != nil {
			return err
		}
	}
	if len(s.Warnings) == 0 {
		return nil
	}
	// The separating blank line belongs to the sections above it, so a page
	// that is nothing but warnings does not open on an empty line.
	if len(s.Items) > 0 {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return prettyWarnings(w, s.Warnings, st)
}

// prettyWarnings lists what the page could not produce, under the sections
// that did assemble.
//
// A dropped section leaves nothing behind: the heading is simply absent, so
// six sections where there should be seven look exactly like a page that
// only ever had six. The block is deliberately not given a heading rule of
// its own — a warning is not a seventh section, it is the page telling the
// reader that what is above is partial.
func prettyWarnings(w io.Writer, warnings []view.Error, st styles) error {
	for _, e := range warnings {
		line := st.warn.Render("!") + " " + st.muted.Render(e.Code) + "  " + e.Message
		if e.Hint != "" {
			line += st.muted.Render(" — " + e.Hint)
		}
		if _, err := fmt.Fprintln(w, wrap(line, st.width, "  ")); err != nil {
			return err
		}
	}
	return nil
}

// prettyMarkdown renders markdown text with glamour. Styles are fixed rather
// than auto-detected: auto-style queries the terminal, which is unsafe inside
// TUI paints and pipes. On any renderer error the raw body is still shown.
func prettyMarkdown(w io.Writer, body string, st styles) error {
	style := "dark"
	if !st.color {
		style = "notty"
	}
	width := st.width
	if width <= 0 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(style),
		glamour.WithWordWrap(width),
	)
	if err == nil {
		if out, rerr := r.Render(body); rerr == nil {
			_, werr := fmt.Fprintln(w, strings.Trim(out, "\n"))
			return werr
		}
	}
	_, err = fmt.Fprintln(w, strings.TrimRight(body, "\n"))
	return err
}

func prettyKeyValue(w io.Writer, kv view.KeyValue, st styles) error {
	width := 0
	for _, p := range kv.Pairs {
		width = max(width, lipgloss.Width(p.Key))
	}
	for _, p := range kv.Pairs {
		key := st.key.Render(pad(p.Key, width))
		val := p.Value
		if st.color && theme.ClassifyStatus(val) != theme.StatusNeutral {
			val = theme.StatusStyle(val).Render(val)
		}
		val = wrap(val, st.width, strings.Repeat(" ", width+2))
		if _, err := fmt.Fprintf(w, "%s  %s\n", key, val); err != nil {
			return err
		}
	}
	return nil
}

func prettyTable(w io.Writer, t view.Table, st styles, highlight int) error {
	headers := make([]string, len(t.Columns))
	rightAlign := map[int]bool{}
	statusCol := map[int]bool{}
	for i, c := range t.Columns {
		headers[i] = strings.ToUpper(c.Name)
		switch c.Kind {
		case view.KindNumber, view.KindBytes, view.KindPercent, view.KindDuration:
			rightAlign[i] = true
		case view.KindStatus:
			statusCol[i] = true
		}
	}
	// Exactly one cell per declared column, which is a redaction rule and not
	// a layout one.
	//
	// view.Redact masks by column name and says so in its own comment: "a cell
	// with no column cannot be named, so it cannot be masked". This renderer
	// drew those cells anyway, in a nameless extra column — so a table whose
	// rows outran their headers put the one value redaction exists to hide on
	// the screen, beside the dots covering its named neighbour. The markdown
	// renderer already drops them (markdown.go's writeMarkdownGrid), so the
	// two disagreed about the same data, which is the shape of a rule that
	// lives in one renderer instead of in the view.
	//
	// Ahead of the layout decision below, so the record layout inherits it: the
	// rule is about what may be drawn, not about how it is arranged.
	rows := make([][]string, len(t.Rows))
	for i, row := range t.Rows {
		cells := make([]string, len(t.Columns))
		copy(cells, row)
		rows[i] = cells
	}

	// A grid needs room to be a grid, and below that room it is the wrong
	// shape for the same data. Five columns in sixty cells gave each one ten,
	// so an audit finding read "advisories : GHSA- qw6h-vgh9- j6wx" down a
	// ten-cell gutter — every value present, none of them legible, and the
	// borders spending a sixth of the line saying where the gutters were.
	// Records use the whole width for one field at a time, which is what a
	// narrow terminal has to give.
	if !fitsAsGrid(t, headers, st) {
		return prettyRecords(w, t, headers, rows, st, recordStyle{
			highlight: highlight, status: statusCol,
		})
	}

	// Where the slack goes when there is room to spare. Computed once, from
	// the same headers and rows lipgloss is about to measure.
	grown := grownColumns(t, headers, st)

	// A hyphen is not a place to break a line, inside a table cell either.
	//
	// wrap() has shielded them since `rta explain` broke `--endpoint` in half,
	// but a lipgloss table does its own wrapping and never goes through it — so
	// a grid narrow enough to wrap a cell was still producing "GHSA-qw6h-vgh9-"
	// / "j6wx" and "https://osv.dev/vulnerability/" / "GHSA-29mw-wpgm-hmr9",
	// which are an advisory ID and a URL that cannot be copied off the screen.
	// The same substitution answers it: U+2011 is one cell wide, so every width
	// lipgloss measures is the width the real string has, and the restore
	// happens on the finished render.
	//
	// The status lookup below deliberately reads the *unshielded* rows: it
	// classifies a cell by its text, and a vocabulary that ever grows a
	// hyphenated word would silently stop matching.
	display := make([][]string, len(rows))
	for i, row := range rows {
		display[i] = make([]string, len(row))
		for j, cell := range row {
			display[i][j] = shieldHyphens(cell)
		}
	}
	shieldedHeaders := make([]string, len(headers))
	for i, h := range headers {
		shieldedHeaders[i] = shieldHyphens(h)
	}

	// Tables are rebuilt per attempt: lipgloss tables memoize layout, so a
	// width change on a rendered instance is unreliable.
	build := func(width int) string {
		tbl := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(st.border).
			Headers(shieldedHeaders...).
			Rows(display...).
			StyleFunc(func(row, col int) lipgloss.Style {
				s := lipgloss.NewStyle().Padding(0, 1)
				// A column told its width keeps it: lipgloss reads that as
				// fixed and leaves the rest to natural measurement.
				if w, ok := grown[col]; ok && width == 0 {
					s = s.Width(w)
				}
				// Before the header short-circuit, so a numeric column's
				// heading sits over its own digits. It used to return first,
				// which left "BYTES" flush left above a column of
				// right-aligned numbers — the one place a heading has a
				// column to line up with, and it did not.
				if rightAlign[col] {
					s = s.Align(lipgloss.Right)
				}
				if row == table.HeaderRow {
					return s.Inherit(st.header)
				}
				// Semantic hint, not decoration: color state where it matters.
				if st.color && statusCol[col] && row >= 0 && row < len(rows) && col < len(rows[row]) {
					s = s.Inherit(theme.StatusStyle(rows[row][col]))
				}
				// Row selection in interactive hosts: a quiet background band.
				if st.color && highlight > 0 && row == highlight-1 {
					s = s.Background(theme.Faint)
				}
				return s
			})
		if width > 0 {
			tbl = tbl.Width(width)
		}
		return tbl.Render()
	}
	rendered := build(0)
	// Constrain only when the natural width overflows: narrow tables keep
	// their tight fit, wide ones shrink columns instead of wrapping raggedly.
	// The shrink iterates because lipgloss tables can exceed the requested
	// width by a border cell or two at their minimum.
	for w := st.width; st.width > 0 && w >= 20 && lipgloss.Width(rendered) > st.width; w -= 2 {
		rendered = build(w)
	}
	if _, err := fmt.Fprintln(w, restoreHyphens(rendered)); err != nil {
		return err
	}
	var footer []string
	if t.Total > len(t.Rows) {
		footer = append(footer, fmt.Sprintf("%d of %d rows", len(t.Rows), t.Total))
	}
	if more := continues(t); more != "" {
		footer = append(footer, more)
	}
	if len(footer) > 0 {
		_, err := fmt.Fprintln(w, st.muted.Render(strings.Join(footer, " · ")))
		return err
	}
	return nil
}

// continues describes an answer that stops short of the data behind it.
//
// **The cursor was in the contract and rendered nowhere.** view.Table.Page
// crosses the wire and is carried by pkg/sdk/wire, and no surface read it — so
// a listing bounded at its limit came out looking exactly like a complete one,
// which is the same defect the csv branch records for Total. A source of a
// thousand objects answering with two hundred is not wrong; answering with two
// hundred and saying nothing is.
//
// The continuation value is shown because it is the argument somebody types
// next. Total is a count and needs no action; this is an instruction.
func continues(t view.Table) string {
	if t.Page == nil || t.Page.Next == "" {
		return ""
	}
	return "more rows after " + t.Page.Next
}

// grownColumns decides which columns absorb the space a table has been given
// and does not need, keyed by column index and holding the full width to
// render each at (padding included).
//
// The slack goes to the text columns and nowhere else. lipgloss expands a
// table by widening its *narrowest* column first, which for a list of tasks
// means a fifteen-cell ID beside a truncated title — the wrong answer given
// with great precision. A column's Kind already says which is which: numbers,
// durations, timestamps and statuses are as wide as they will ever be, and
// the unkinded ones are the prose. A table with no prose in it is left alone,
// because stretching numbers apart makes a row harder to follow, not easier.
func grownColumns(t view.Table, headers []string, st styles) map[int]int {
	if !st.fill || st.width <= 0 || len(t.Columns) == 0 {
		return nil
	}
	var text []int
	for i, c := range t.Columns {
		if c.Kind == view.KindText {
			text = append(text, i)
		}
	}
	if len(text) == 0 {
		return nil
	}

	// Natural width, measured the way it will be rendered: the widest cell
	// in the column, plus the cell padding, plus one border cell per column
	// and one to close the table.
	natural := make([]int, len(t.Columns))
	for i := range t.Columns {
		w := 0
		if i < len(headers) {
			w = lipgloss.Width(headers[i])
		}
		for _, row := range t.Rows {
			if i < len(row) {
				w = max(w, lipgloss.Width(row[i]))
			}
		}
		natural[i] = w + 2
	}
	slack := st.width - (sum(natural) + len(natural) + 1)
	if slack <= 0 {
		return nil // already at or over the width: the shrink path handles it
	}

	grown := make(map[int]int, len(text))
	share, extra := slack/len(text), slack%len(text)
	for n, i := range text {
		grown[i] = natural[i] + share
		if n < extra {
			grown[i]++
		}
	}
	return grown
}

func sum(ns []int) int {
	total := 0
	for _, n := range ns {
		total += n
	}
	return total
}

func prettyTree(w io.Writer, t view.Tree, st styles) error {
	var walk func(nodes []view.Node, prefix string) error
	walk = func(nodes []view.Node, prefix string) error {
		for i, n := range nodes {
			connector, childPrefix := "├── ", prefix+"│   "
			if i == len(nodes)-1 {
				connector, childPrefix = "└── ", prefix+"    "
			}
			line := prefix + st.muted.Render(connector) + n.Label
			if n.Detail != "" {
				line += " " + st.muted.Render(n.Detail)
			}
			if _, err := fmt.Fprintln(w, wrap(line, st.width, prefix+"    ")); err != nil {
				return err
			}
			if err := walk(n.Children, childPrefix); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range t.Roots {
		label := st.key.Render(root.Label)
		if root.Detail != "" {
			label += " " + st.muted.Render(root.Detail)
		}
		if _, err := fmt.Fprintln(w, wrap(label, st.width, "  ")); err != nil {
			return err
		}
		if err := walk(root.Children, ""); err != nil {
			return err
		}
	}
	return nil
}

// chartWidth/chartHeight bound drawn charts; content stays scannable.
const (
	chartWidth  = 64
	chartHeight = 8
	barWidth    = 30
	barMinWidth = 8
)

// plotWidth resolves the chart plot width for the available render width.
func plotWidth(st styles, natural, overhead int) int {
	if st.width <= 0 {
		return natural
	}
	return max(barMinWidth, min(natural, st.width-overhead))
}

func prettyChart(w io.Writer, c view.Chart, st styles) error {
	if len(c.Series) == 0 {
		_, err := fmt.Fprintln(w, st.muted.Render("(no data)"))
		return err
	}
	switch c.Kind {
	case view.ChartBar:
		return prettyBars(w, c, st)
	default:
		return prettyLines(w, c, st)
	}
}

// prettyLines draws line series with asciigraph; the legend carries names.
func prettyLines(w io.Writer, c view.Chart, st styles) error {
	data := make([][]float64, 0, len(c.Series))
	names := make([]string, 0, len(c.Series))
	for _, s := range c.Series {
		if len(s.Points) == 0 {
			continue
		}
		data = append(data, s.Points)
		names = append(names, s.Name)
	}
	if len(data) == 0 {
		_, err := fmt.Fprintln(w, st.muted.Render("(no data)"))
		return err
	}
	caption := strings.Join(names, " · ")
	if c.Unit != "" {
		caption += " (" + c.Unit + ")"
	}
	opts := []asciigraph.Option{
		asciigraph.Height(chartHeight),
		// ~9 cells of asciigraph axis labels on the left.
		asciigraph.Width(plotWidth(st, chartWidth, 9)),
		asciigraph.Caption(caption),
	}
	// A declared scale is a promise the chart is comparable to the next one
	// drawn of the same metric. The line path used to ignore it and autoscale,
	// so a run where every value sat between 40% and 42% drew the same
	// full-height sawtooth as one that swung from 0 to 100.
	if c.Max > 0 {
		opts = append(opts, asciigraph.LowerBound(0), asciigraph.UpperBound(c.Max))
	}
	plot := asciigraph.PlotMany(data, opts...)
	_, err := fmt.Fprintln(w, plot)
	return err
}

// prettyBars draws one horizontal bar per series (first point is the value).
func prettyBars(w io.Writer, c view.Chart, st styles) error {
	// A declared scale wins outright rather than being a floor the data can
	// raise: "100" on a percentage chart has to keep meaning full width, or a
	// core briefly reporting 101% quietly redraws every other bar shorter.
	// Values past the top are clamped below, which is what over-range should
	// look like. Without a declared scale the bars are relative to the
	// largest value present — the only honest autoscale for a bar chart.
	scale := c.Max
	nameWidth := 0
	for _, s := range c.Series {
		if c.Max <= 0 && len(s.Points) > 0 && s.Points[0] > scale {
			scale = s.Points[0]
		}
		nameWidth = max(nameWidth, lipgloss.Width(s.Name))
	}
	if scale <= 0 {
		scale = 1
	}
	// Label + two gaps + value ("100.0%") share the row with the bar.
	width := plotWidth(st, barWidth, nameWidth+lipgloss.Width(c.Unit)+10)
	for _, s := range c.Series {
		v := 0.0
		if len(s.Points) > 0 {
			v = s.Points[0]
		}
		filled := int(v / scale * float64(width))
		filled = min(max(filled, 0), width)
		bar := st.key.Render(strings.Repeat("█", filled)) +
			st.faint.Render(strings.Repeat("░", width-filled))
		label := pad(s.Name, nameWidth)
		value := fmt.Sprintf("%.1f%s", v, c.Unit)
		if _, err := fmt.Fprintf(w, "%s  %s  %s\n", st.muted.Render(label), bar, value); err != nil {
			return err
		}
	}
	return nil
}

// RenderError writes a coded error with its hint. Meant for stderr.
//
// --output has to keep applying when something goes wrong, which is exactly
// when a script needs a code it can branch on. This special-cased json alone,
// so `-o yaml` answered a failure with a styled "ERROR pg.conn.refused"
// block that no yaml parser accepts: the two machine-readable formats
// disagreed about whether a failure was machine-readable at all.
//
// csv is the deliberate exception, not an oversight left for later. An error
// is one coded fact and a hint, and a one-row code,message,hint table is a
// shape no capability in the catalogue produces, so a consumer piping csv
// into a column-indexed reader would get something that parses and means
// nothing. It falls through to the styled block, which at least says so in
// prose.
func RenderError(w io.Writer, e *view.Error, opts Options) error {
	if opts.Format != JSON {
		e = cleanPtr(e)
	}
	switch opts.Format {
	case Markdown:
		return markdownError(w, e)
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(view.Envelope{View: e})
	case YAML:
		m, err := view.ToMap(e)
		if err != nil {
			return err
		}
		out, err := yaml.Marshal(m)
		if err != nil {
			return err
		}
		_, err = w.Write(out)
		return err
	}
	st := newStyles(opts.NoColor)
	st.width = opts.Width
	w = profiled(w, opts)
	// The hanging indent is measured, not counted out by hand. Both badges
	// carry Padding(0, 1) when there is colour and nothing when there is not
	// (theme.ErrBadge, theme.HintBadge), so a hardcoded 6 was right in a pipe
	// and two cells short on a terminal — which is the half nobody diffs.
	badge := st.errBadge.Render("ERROR") + " "
	head := badge + st.muted.Render(e.Code) + " "
	if _, err := fmt.Fprintln(w, wrap(head+e.Message, st.width, indent(badge))); err != nil {
		return err
	}
	if e.Hint != "" {
		hint := st.hintBadge.Render("HINT") + " "
		if _, err := fmt.Fprintln(w, wrap(hint+e.Hint, st.width, indent(hint))); err != nil {
			return err
		}
	}
	return nil
}

// indent is a run of spaces as wide as s renders.
func indent(s string) string { return strings.Repeat(" ", lipgloss.Width(s)) }

// pad right-fills s to width display cells.
//
// Deliberately not fmt's "%-*s", which was what these columns used and is two
// wrong units at once: the width they were measured with is len(), which
// counts bytes, and fmt pads strings by *runes*. The three agree only for
// ASCII, so a key column stayed straight until somebody's data had an accent
// in it — and a bar chart's series lost the shared baseline that is the only
// thing a bar chart is for.
func pad(s string, width int) string {
	if n := lipgloss.Width(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// minWrap is the narrowest content column worth wrapping to; below it every
// break lands mid-word and the result is less readable than the overflow.
const minWrap = 16

// wrap reflows a run of text so no line exceeds width display cells, with
// every line after the first prefixed by cont. It is the single wrapping
// mechanism of this renderer: Text, KeyValue values, Tree rows and errors all
// go through it, so they break the same way and there is one place to change.
//
// width <= 0 means "natural width" and returns s untouched — that is what
// keeps piped and redirected output byte-identical to what it has always
// been, and diffable across machines.
func wrap(s string, width int, cont string) string {
	if width < minWrap {
		return s
	}
	// The floor keeps a wide key column (or a deep tree) from shrinking the
	// text to a three-cell gutter where every break lands mid-word.
	budget := max(minWrap, width-lipgloss.Width(cont))
	// Word boundaries first, then a hard break for the single token that is
	// longer than the budget on its own (a 64-char key, a long URL), so
	// nothing can escape the width.
	//
	// **Hyphens are not word boundaries here**, which is why this is two calls
	// and a substitution rather than one call to ansi.Wrap. x/ansi treats "-"
	// as a breakpoint always — right for prose, wrong for everything this
	// renderer actually shows. `rta explain s3.object.get` wrapped its own
	// command line as "[--" / "endpoint <string>]", which is a line nobody can
	// copy and a flag that reads as two things; `proj1-staging` and
	// `--allow-destructive` break the same way wherever they land near the
	// margin. Every hyphen in this tool is inside an identifier somebody may
	// be about to paste.
	lines := hardBreakOverlong(ansi.Wordwrap(shieldHyphens(s), budget, ""), budget)
	for i, line := range lines {
		lines[i] = restoreHyphens(line)
	}
	for i, line := range lines {
		// ansi.Wrap keeps the space it broke on; left in, it shows up as a
		// selection artifact and as noise in a diff.
		lines[i] = strings.TrimRight(line, " ")
		if i > 0 {
			lines[i] = cont + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// shieldHyphens hides "-" from x/ansi's always-on hyphen breakpoint, and
// restoreHyphens puts it back.
//
// U+2011 NON-BREAKING HYPHEN is the stand-in: one cell wide, so every width
// the wrapper computes is the width the real string has. A blanket
// substitution is safe because nothing this renderer emits carries a hyphen
// inside an escape sequence — SGR is digits, semicolons and a letter, and
// sanitize.go has already removed OSC (the one escape that could hold a URL)
// before anything reaches here.
const nonBreakingHyphen = "‑"

func shieldHyphens(s string) string { return strings.ReplaceAll(s, "-", nonBreakingHyphen) }
func restoreHyphens(s string) string {
	return strings.ReplaceAll(s, nonBreakingHyphen, "-")
}

// hardBreakOverlong splits only the lines a word wrap could not fit — a single
// token longer than the whole budget. Everything else is left exactly as the
// word wrap produced it.
func hardBreakOverlong(wrapped string, budget int) []string {
	lines := strings.Split(wrapped, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if ansi.StringWidth(line) <= budget {
			out = append(out, line)
			continue
		}
		out = append(out, strings.Split(ansi.Hardwrap(line, budget, false), "\n")...)
	}
	return out
}

// styles resolves the theme for one render call; color=false collapses
// everything to plain text (pipes, --no-color).
type styles struct {
	color     bool
	width     int  // 0 = natural
	fill      bool // width is a target, not a ceiling
	key       lipgloss.Style
	header    lipgloss.Style
	border    lipgloss.Style
	muted     lipgloss.Style
	faint     lipgloss.Style
	warn      lipgloss.Style
	errBadge  lipgloss.Style
	hintBadge lipgloss.Style
}

func newStyles(noColor bool) styles {
	if noColor {
		return styles{
			key: theme.Plain, header: theme.Plain, border: theme.Plain,
			muted: theme.Plain, faint: theme.Plain, warn: theme.Plain,
			errBadge: theme.Plain, hintBadge: theme.Plain,
		}
	}
	return styles{
		color:     true,
		key:       theme.Key,
		header:    theme.Header,
		border:    theme.Border,
		muted:     theme.Subtle,
		faint:     theme.Faded,
		warn:      theme.WarnText,
		errBadge:  theme.ErrBadge,
		hintBadge: theme.HintBadge,
	}
}
