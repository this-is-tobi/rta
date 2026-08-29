package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
)

// The theme editor: every palette field theme.Fields() names, an empty box
// for "no override" and the color it resolves to either way, written back
// the same load-modify-write way arrange.go's dashboard saves already do.
//
// Purpose-built rather than routed through capForm: a color is a hex string
// with one validator, not a plugin.Field carrying Positional/Local/Config
// semantics that mean nothing here, and stretching that type to fit would
// have cost more than the form this took on its own.

// themeFieldOrder is the form's own order — grouped by what a field is for,
// not theme.Fields()'s alphabetical one (accent, bad, faint, …), which reads
// as a word list rather than a palette. Checked against theme.Fields() by
// TestThemeFieldOrderNamesExactlyWhatThemeFieldsDoes, so a field theme.go
// adds or removes cannot silently go missing from — or linger dead in — this
// form.
var themeFieldOrder = []string{
	"primary", "accent", "label", "good", "warn", "bad", "muted", "faint", "inverse", "ink",
}

// themeFieldHelp is one line per field, in the voice of theme.go's own
// palette comments — not exported from theme, because it is presentation for
// this one screen rather than a fact about the color itself.
var themeFieldHelp = map[string]string{
	"primary": "identity: keys, selection, panel titles for content",
	"accent":  "sparing highlights — filter matches",
	"label":   "pane names, in the panel border",
	"good":    "status: healthy, active, ok",
	"warn":    "status: needs attention",
	"bad":     "status: failed, destructive",
	"muted":   "secondary text",
	"faint":   "structure: borders, chart fills",
	"inverse": "text on a Bad background (the ERROR badge)",
	"ink":     "text on a Warn background (the HINT badge)",
}

// themeForm collects one hex string per palette field.
//
// Seeded from the config file's own theme: section, not from theme.Current().
// Current always answers every field — the built-in when nothing overrides
// it — so seeding from it would have started every box non-empty, and
// submitting an untouched form would have written all ten built-ins into the
// file as if the operator had chosen them. Pinned that way, a future rta
// that changes a built-in shade would silently stop reaching anyone who had
// ever opened this screen and pressed enter. An empty box means "no
// override" and stays empty until the operator types something; what it
// currently resolves to is shown beside it, not written into it.
type themeForm struct {
	form     *huh.Form
	bindings map[string]*string
}

func newThemeForm(existing map[string]string) *themeForm {
	tf := &themeForm{bindings: map[string]*string{}}
	live := theme.Current()
	var fields []huh.Field
	for _, key := range themeFieldOrder {
		v := existing[key] // "" when this field has no override on disk
		tf.bindings[key] = &v
		fields = append(fields, huh.NewInput().
			Title(key).
			Description(fmt.Sprintf("%s (currently %s) — tab completes",
				themeFieldHelp[key], live[key])).
			Suggestions(paletteFor(key, live)).
			Value(&v).
			Validate(hexOrEmpty))
	}
	tf.form = huh.NewForm(huh.NewGroup(fields...)).WithKeyMap(formKeyMap())
	return tf
}

// paletteFor is what this field can be completed to: its own current value
// first, then the rest of the live palette.
//
// The form already prints "(currently #7d56f4)" under every box and then makes
// somebody retype it, which is the shape of a screen that knows the answer and
// will not say it — pressing tab now fills it in. The other nine follow
// because the usual edit is not inventing a colour, it is making two things
// match: accent taking primary's shade, warn taking error's.
//
// Static, unlike a capForm's: a palette does not change while the form that
// edits it is open.
func paletteFor(key string, live map[string]string) []string {
	out := make([]string, 0, len(themeFieldOrder))
	if v := live[key]; v != "" {
		out = append(out, v)
	}
	for _, other := range themeFieldOrder {
		if other == key || live[other] == "" || live[other] == live[key] {
			continue
		}
		out = append(out, live[other])
	}
	return out
}

// hexOrEmpty accepts theme.HexColor's shape or nothing at all — clearing a
// field back to blank is how an operator returns one color to its built-in
// without needing to remember what that built-in was.
func hexOrEmpty(s string) error {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	if !theme.HexColor.MatchString(s) {
		return fmt.Errorf("must be #rrggbb")
	}
	return nil
}

// overrides collects the non-empty fields into what config.Theme should
// hold.
func (tf *themeForm) overrides() map[string]string {
	out := map[string]string{}
	for key, v := range tf.bindings {
		if s := strings.TrimSpace(*v); s != "" {
			out[key] = s
		}
	}
	return out
}

// preview renders one swatch per field from what is currently typed, never
// from the live palette: nothing here calls theme.Apply until submit, so a
// stray keystroke mid-edit cannot recolor the rest of the running app out
// from under whatever else is on screen behind this form.
//
// One compact strip rather than a column beside the form. A huh field is
// title, description and input across two to four lines depending on how its
// description wraps, and ten of them make a form far taller than ten fixed
// preview lines — lipgloss.JoinHorizontal pads the shorter side, which
// looked fine measured in isolation and unreadable once the two were
// actually side by side: the swatch for "faint" landed next to the
// description text of some field three rows away. A strip above the form has
// no height to disagree about.
func (tf *themeForm) preview() string {
	live := theme.Current()
	parts := make([]string, 0, len(themeFieldOrder))
	for _, key := range themeFieldOrder {
		v := strings.TrimSpace(*tf.bindings[key])
		var swatch string
		switch {
		case v == "":
			// Built-in: shown as the live color, since that is what this
			// field will actually resolve to if the form is submitted as is.
			swatch = lipgloss.NewStyle().Foreground(lipgloss.Color(live[key])).Render("████")
		case theme.HexColor.MatchString(v):
			swatch = lipgloss.NewStyle().Foreground(lipgloss.Color(v)).Render("████")
		default:
			swatch = theme.WarnText.Render("××××")
		}
		parts = append(parts, swatch+" "+key)
	}
	return strings.Join(parts, "  ")
}

// startThemeForm opens the editor, seeded from what is actually written down
// — not from what is running, for the reason themeForm's own doc gives.
func (m Model) startThemeForm() (tea.Model, tea.Cmd) {
	cfg, err := config.LoadFile()
	if err != nil {
		m.flash = "theme not opened: " + err.Error()
		return m, nil
	}
	m.themeForm = newThemeForm(cfg.Theme)
	m.mode = modeTheme
	m.fitThemeForm()
	return m, m.themeForm.form.Init()
}

// fitThemeForm sizes the embedded huh form to the panel frame. Mirrors
// fitForm exactly — the preview is a strip above the form now, not a column
// beside it, so it costs the form no width of its own, only the two lines
// themeView adds above it.
func (m *Model) fitThemeForm() {
	if m.themeForm == nil {
		return
	}
	if m.width > 0 {
		m.themeForm.form = m.themeForm.form.WithWidth(formWidth(m.width))
	}
	if m.height > 0 {
		m.themeForm.form = m.themeForm.form.WithHeight(max(m.height-7, 6))
	}
}

// saveTheme writes the submitted overrides and applies them to this process
// immediately — an operator editing colors wants to see the result, not
// restart rta to find out whether they got it right.
func (m Model) saveTheme() (tea.Model, tea.Cmd) {
	overrides := m.themeForm.overrides()
	m.themeForm = nil
	m.mode = modeDashboard

	cfg, err := config.LoadFile()
	if err != nil {
		m.flash = "theme not saved: " + err.Error()
		return m, nil
	}
	cfg.Theme = overrides
	if err := config.Write(cfg); err != nil {
		m.flash = "theme not saved: " + err.Error()
		return m, nil
	}

	problems := theme.Apply(cfg.Theme)
	switch {
	case len(problems) == 1:
		m.flash = "saved, but " + problems[0].String()
	case len(problems) > 1:
		m.flash = fmt.Sprintf("saved, but %d fields could not be applied — see `rta doctor`", len(problems))
	case len(overrides) == 0:
		m.flash = "reset to the built-in theme"
	default:
		m.flash = fmt.Sprintf("saved %d theme %s", len(overrides), pluralNoun(len(overrides), "override"))
	}
	m.tickGen++
	return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.profileFor)
}

// updateThemeForm drives the embedded huh form and dispatches on completion,
// mirroring updateForm's shape for the one mode it does not handle.
func (m Model) updateThemeForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.themeForm == nil {
		m.mode = modeDashboard
		return m, nil
	}
	model, cmd := m.themeForm.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		m.themeForm.form = f
	}
	return m.afterThemeFormUpdate(cmd)
}

// afterThemeFormUpdate dispatches on the form's state after it was driven
// forward by one message — a real keypress from updateThemeForm, or
// several synthetic ones from fastSubmitThemeForm.
func (m Model) afterThemeFormUpdate(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	switch m.themeForm.form.State {
	case huh.StateCompleted:
		return m.saveTheme()
	case huh.StateAborted:
		m.themeForm = nil
		m.mode = modeDashboard
		return m, nil
	}
	return m, cmd
}

// fastSubmitThemeForm accepts whatever is currently typed across every
// remaining field, exactly as pressing enter on each in turn would — an
// empty, untouched box is a valid "no override" answer for every field
// here (hexOrEmpty), so racing through the whole form with nothing typed
// is itself a legitimate, if unusual, way to reach "reset to the built-in
// theme".
func (m Model) fastSubmitThemeForm() (tea.Model, tea.Cmd) {
	if m.themeForm == nil {
		return m, nil
	}
	m.themeForm.form = advanceFormBySyntheticEnter(m.themeForm.form)
	return m.afterThemeFormUpdate(nil)
}

// themeView frames the editor beside its live preview.
func (m Model) themeView() string {
	if m.themeForm == nil {
		return ""
	}
	footer := m.footerFor(modeTheme)
	strip := m.themeForm.preview()
	if m.width > 4 {
		// Soft-wrapped, not truncated: at a narrow width the strip runs onto
		// a second line rather than losing the last few fields off the edge,
		// which is the one place in this screen an operator cannot recover a
		// dropped fact by scrolling.
		strip = lipgloss.NewStyle().Width(m.width - 4).Render(strip)
	}
	body := strip + "\n\n" + m.themeForm.form.View()
	head := panelHead{Title: "theme", Note: "#rrggbb, or blank for the built-in"}
	return panel(head, "\n"+body, m.width, m.height-lipgloss.Height(footer), true) + "\n" + footer
}
