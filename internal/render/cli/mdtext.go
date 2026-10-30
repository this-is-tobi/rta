package cli

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// prettyMarkdown draws markdown text — a note's body, a plugin's page — for a
// terminal: headings in the heading style, lists hung under their bullets, a
// quoted block behind a bar, a fenced block verbatim and dimmed, and `code`,
// **bold**, *emphasis* and [links](url) inline. Anything else is the text it is.
//
// rta's own and not glamour's. Glamour drew these with a full CommonMark
// renderer and a syntax highlighter for every language chroma knows, and they
// were a twelfth of the binary — 5.15 MB of a stripped linux/amd64 build — for
// two kinds of text that use a handful of markdown's forms. What this leaves out
// is colour inside a fenced block and the forms a note does not use (tables,
// footnotes, HTML), which are shown as written, and markdown shown as written
// is still readable.
//
// Styled only with colour on: without it the markers stay, a heading keeps its
// #s and code its backticks, since the marker is then the only thing that says
// what the text is. Cleaned like everything this renderer draws (cleanRendered),
// after the styling, so what the body carries cannot pass as a style of its own.
func prettyMarkdown(w io.Writer, body string, st styles) error {
	width := st.width
	if width <= 0 {
		width = 80
	}
	var out []string
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, wrap(drawInline(strings.Join(para, " "), st), width, ""))
			para = nil
		}
	}
	fence := ""
	for _, line := range strings.Split(strings.ReplaceAll(strings.TrimRight(body, "\n"), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
				continue
			}
			out = append(out, "  "+st.muted.Render(line))
			continue
		}
		if m := mdFence.FindString(trimmed); m != "" {
			flush()
			fence = m
			continue
		}
		if trimmed == "" {
			flush()
			out = append(out, "")
			continue
		}
		if m := mdHeading.FindStringSubmatch(trimmed); m != nil {
			flush()
			out = append(out, headingLine(m[1], m[2], st))
			continue
		}
		if mdRule.MatchString(trimmed) {
			flush()
			out = append(out, st.muted.Render(strings.Repeat(st.glyphs.rule, min(width, 40))))
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, ">"); ok {
			flush()
			bar := st.muted.Render(st.glyphs.quote)
			// wrap keeps every line, the first among them, inside width less
			// the prefix it is given, which is the bar on all of them.
			out = append(out, bar+wrap(drawInline(strings.TrimSpace(rest), st), width, bar))
			continue
		}
		if m := mdItem.FindStringSubmatch(line); m != nil {
			flush()
			indent, marker := m[1], m[2]
			if !strings.ContainsAny(marker, ".)") {
				marker = st.glyphs.bullet
			}
			head := indent + marker + " "
			out = append(out, head+wrap(drawInline(m[3], st), width, strings.Repeat(" ", lipgloss.Width(head))))
			continue
		}
		para = append(para, trimmed)
	}
	flush()
	_, err := fmt.Fprintln(w, strings.Trim(cleanRendered(collapseBlank(out), st.color), "\n"))
	return err
}

var (
	mdFence   = regexp.MustCompile("^(```|~~~)")
	mdHeading = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*$`)
	mdRule    = regexp.MustCompile(`^([-*_])(\s*[-*_]){2,}$`)
	mdItem    = regexp.MustCompile(`^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$`)
	mdCode    = regexp.MustCompile("`[^`]+`")
	mdLink    = regexp.MustCompile(`!?\[([^\]]*)\]\(([^)\s]+)\)`)
	mdStrong  = regexp.MustCompile(`\*\*([^*\s](?:[^*]*[^*\s])?)\*\*`)
	mdEmph    = regexp.MustCompile(`\*([^*\s](?:[^*]*[^*\s])?)\*`)
)

// headingLine is a heading: its text in the heading style, or with its #s when
// there is no style to say it is one.
func headingLine(hashes, text string, st styles) string {
	if !st.color {
		return hashes + " " + drawInline(text, st)
	}
	return st.header.Render(drawInline(text, st))
}

// drawInline draws a line's inline forms. Code spans first and left as they
// are inside, since a `**` in a command is not emphasis; then links, as their
// text with the address beside it, which a terminal cannot follow otherwise and
// a reader is owed whatever the text says; then strong and emphasis.
func drawInline(s string, st styles) string {
	var b strings.Builder
	last := 0
	for _, at := range mdCode.FindAllStringIndex(s, -1) {
		b.WriteString(drawProse(s[last:at[0]], st))
		code := s[at[0]:at[1]]
		if st.color {
			code = st.key.Render(code[1 : len(code)-1])
		}
		b.WriteString(code)
		last = at[1]
	}
	b.WriteString(drawProse(s[last:], st))
	return b.String()
}

func drawProse(s string, st styles) string {
	// The address is printed, not hidden behind the text in an OSC 8 link as
	// glamour wrote it: a terminal shows a printed URL as one, and a reader
	// sees where the text goes whatever the text says.
	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		parts := mdLink.FindStringSubmatch(m)
		text, url := parts[1], parts[2]
		if text == "" || text == url {
			return st.key.Render(url)
		}
		return st.key.Render(text) + st.muted.Render(" ("+url+")")
	})
	strong, emph := lipgloss.NewStyle(), lipgloss.NewStyle()
	if st.color {
		strong, emph = strong.Bold(true), emph.Italic(true)
	}
	s = mdStrong.ReplaceAllStringFunc(s, func(m string) string { return strong.Render(m[2 : len(m)-2]) })
	return mdEmph.ReplaceAllStringFunc(s, func(m string) string { return emph.Render(m[1 : len(m)-1]) })
}

// collapseBlank joins the lines with runs of blank ones made one, which is what
// a paragraph break is.
func collapseBlank(lines []string) string {
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if l == "" && len(kept) > 0 && kept[len(kept)-1] == "" {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}
