package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The theme editor: every palette field theme.Fields() names, an empty box
// for "no override" and the color it resolves to either way, written back
// the same load-modify-write way arrange.go's dashboard saves already do. And,
// last, in the operator's own block alone, the one entry of `theme:` that is not
// a colour: `status`, how much of the colour that grades is drawn.
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

// gradesFieldHelp is the line for the one box that is no colour and takes a word
// instead: it quiets the green, amber and red that grade, and leaves every word,
// number and border drawn. Beside themeFieldHelp and not in it, which holds the
// palette's slots and nothing else.
const gradesFieldHelp = "how much green, amber and red is drawn: all, alerts (warn and bad only) or off"

// fieldHelp is the line under a box.
func fieldHelp(key string) string {
	if key == theme.GradesKey {
		return gradesFieldHelp
	}
	return themeFieldHelp[key]
}

// themeTarget is a place the palette is written down: the top-level `theme:`
// block, or one profile's.
//
// The editor opens on a choice between them, because there are now several
// palettes and a person has to say whose they are editing — and defaults to the
// one in force, since that is the screen they are looking at and the one they
// most often want to change.
type themeTarget struct {
	// name is the profile whose block this is, "" for the top-level one.
	name string
	// stored is the block as the file holds it now: what the form is seeded
	// from, for the reason themeForm gives.
	stored map[string]string
	// floor is what the block is laid over, lowest first: nothing for the
	// top-level block, whose floor is the built-in palette, and for a profile
	// the operator's own block and the profile's colour as its primary.
	floor []theme.Layer
	// guarded says the block is held to the rule an environment's colours are
	// (theme.Resolve): a profile's always, the top-level block of a file
	// nobody named.
	guarded bool
	// source is how a problem names the block: `theme`, `profiles.prod.theme`.
	source string
	// standing is how many of the entries stored are applied: the count a
	// picker may say, and not the block as written (profile.ThemeStanding).
	standing int
}

// fields are the boxes this place offers, in the form's own order: every colour
// and then `status` for the operator's own block, and for a guarded one all but
// the status colours and the setting, which only the operator's own named config
// file can state (theme.Resolve). A box that saves a line the palette always
// leaves out is the wrong thing to offer.
func (t themeTarget) fields() []string {
	if !t.guarded {
		return append(slices.Clone(themeFieldOrder), theme.GradesKey)
	}
	return slices.DeleteFunc(slices.Clone(themeFieldOrder), theme.IsStatus)
}

// droppedStatuses are the status colours, and the setting about drawing them,
// stored in a block that has no box for them, which a save of the form leaves
// behind: sorted, for the notice.
func (t themeTarget) droppedStatuses() []string {
	if !t.guarded {
		return nil
	}
	var out []string
	for slot := range t.stored {
		if theme.IsStatus(slot) || slot == theme.GradesKey {
			out = append(out, slot)
		}
	}
	slices.Sort(out)
	return out
}

// layers is the palette this place makes of colours typed into it: the floor
// beneath, and the colours over it as the one block this place writes.
func (t themeTarget) layers(colors map[string]string) []theme.Layer {
	return append(slices.Clone(t.floor), theme.Layer{Source: t.source, Colors: colors, Guarded: t.guarded})
}

// palette is every slot's colour as this place would draw it with colours typed
// into it, and why each typed one that could not be used is left out — not a
// colour, no such slot, one too dim to be seen, or a primary that is a status.
// The picture the editor shows is this and not the live palette, which is
// another place's as often as not: the environment in force is a different
// screen from the one being edited.
//
// And under `status`, which is no colour, the way status would be drawn: the
// word a box with that name shows as what it is "currently".
func (t themeTarget) palette(typed map[string]string) (colours map[string]string, left map[string]theme.Problem) {
	overrides, problems := theme.Resolve(t.layers(typed)...)
	left = map[string]theme.Problem{}
	for _, p := range problems {
		if p.Source == t.source {
			left[p.Field] = p
		}
	}
	colours = theme.Effective(overrides)
	colours[theme.GradesKey] = theme.GradesOf(overrides).String()
	return colours, left
}

// themeTargetFor is the target for a profile, or for the top-level block with
// "" — false for a profile that is not there, or that this configuration is not
// entitled to name (profile.ThemeFloor says why that matters).
func themeTargetFor(cfg config.Config, name string) (themeTarget, bool) {
	if name == "" {
		return themeTarget{stored: cfg.Theme, guarded: !cfg.Trusted(), source: "theme"}, true
	}
	p, ok := cfg.Profiles[name]
	if !ok || !p.Trusted() {
		return themeTarget{}, false
	}
	return themeTarget{
		name: name, stored: p.Theme, floor: profile.ThemeFloor(cfg, name),
		guarded: true, source: "profiles." + name + ".theme",
		standing: len(profile.ThemeStanding(cfg, name)),
	}, true
}

// themeTargets is every place the palette can be written, the top-level block
// first and then each profile that exists, by name.
func themeTargets(cfg config.Config) []themeTarget {
	out := make([]themeTarget, 0, 1+len(cfg.Profiles))
	for _, name := range append([]string{""}, cfg.ProfileNames()...) {
		if target, ok := themeTargetFor(cfg, name); ok {
			out = append(out, target)
		}
	}
	return out
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
//
// The same holds for a profile's block, with one more reason: the colours
// under it are the operator's own and the profile's, and a box seeded from
// them would write the operator's palette into the profile, where it would
// stop following the operator's edits to their own.
type themeForm struct {
	form   *huh.Form
	target themeTarget
	// bindings holds what each box says.
	bindings map[string]*string
	// The three things tab's rule needs, for the same reasons capForm keeps
	// them: the widget the cursor is in, what that box can be completed to,
	// and which boxes have already had their list said out loud. A palette
	// does not change while the form editing it is open, so unlike a capForm's
	// these are values rather than functions.
	inputs map[string]*huh.Input
	offers map[string][]string
	listed map[string]bool
	// browsed is the candidate the arrows last placed in each box (offerbrowse.go).
	browsed map[string]browsed
	// validators is what each box holds a typed value to, kept so a test can ask
	// a box what it would say.
	validators map[string]func(string) error
}

// touched says whether a box holds something other than what the block had on
// disk when the editor opened, which is what esc and ctrl+c would lose: a box
// typed in and emptied again is the box it was, as on a capability form.
func (tf *themeForm) touched() bool {
	for key, v := range tf.bindings {
		if *v != tf.target.stored[key] {
			return true
		}
	}
	return false
}

func newThemeForm(target themeTarget) *themeForm {
	tf := &themeForm{
		target:   target,
		bindings: map[string]*string{},
		inputs:   map[string]*huh.Input{},
		offers:   map[string][]string{},
		listed:   map[string]bool{},
		browsed:  map[string]browsed{},

		validators: map[string]func(string) error{},
	}
	// What each box resolves to in this place, from what is on disk: the
	// "currently" beside it and what tab offers. Not the live palette, which is
	// the environment in force's, and is not what this block lies over unless
	// that is the place being edited.
	live, _ := target.palette(target.stored)
	offered := target.fields()
	fields := make([]huh.Field, 0, len(offered))
	for _, key := range offered {
		v := target.stored[key] // "" when this field has no override on disk
		tf.bindings[key] = &v
		tf.offers[key] = offersFor(key, live)
		in := huh.NewInput().
			Title(key).
			Description(fmt.Sprintf("%s (currently %s) — tab completes, ↑↓ browse",
				fieldHelp(key), live[key])).
			Suggestions(tf.offers[key]).
			Value(&v).
			Validate(tf.validator(key))
		tf.validators[key] = tf.validator(key)
		tf.inputs[key] = in
		fields = append(fields, in)
	}
	// huh's help line is off here as on every form: the footer speaks for
	// the focused field (fieldHints), in the app's one vocabulary.
	tf.form = huh.NewForm(huh.NewGroup(fields...)).WithKeyMap(formKeyMap()).
		WithTheme(formTheme()).WithShowHelp(false).WithShowErrors(false)
	return tf
}

// tabInTheme is tab on the theme editor, and it is the same rule the
// capability form follows (tabOn) rather than a second one that resembles it.
//
// Every box here starts empty — an empty box means "no override" — which is
// precisely the state bubbles will not match a suggestion against, so this is
// the screen where listing on the first press earns its keep: "tab completes"
// is written under all ten fields, and until the list was said out loud the
// only way to find out what it offered was to guess a prefix.
func (m Model) tabInTheme(msg tea.Msg) (tea.Model, tea.Cmd) {
	tf := m.themeForm
	name, ok := tf.focusedInput()
	if !ok {
		return m.updateThemeForm(msg)
	}
	typed := strings.TrimSpace(*tf.bindings[name])
	switch tabOn(typed, tf.offers[name], tf.listed[name]) {
	case tabAccepts:
		return m.updateThemeForm(msg)
	case tabLists:
		tf.listed[name] = true
		m.flash = listing(tf.offers[name])
		return m, nil
	}
	return m, huh.NextField
}

// focusedInput names the box the cursor is in. Every field of this form is
// one, so the only false is a form that has not started.
func (tf *themeForm) focusedInput() (string, bool) {
	if tf == nil || tf.form == nil {
		return "", false
	}
	focused := tf.form.GetFocusedField()
	for name, in := range tf.inputs {
		if focused == huh.Field(in) {
			return name, true
		}
	}
	return "", false
}

// offersFor is what a box can be completed to: the three ways to draw status for
// the box that is about that, and the palette for every colour.
func offersFor(key string, live map[string]string) []string {
	if key == theme.GradesKey {
		return theme.GradeModes()
	}
	return paletteFor(key, live)
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

// gradesOrEmpty accepts one of the ways to draw status or nothing at all, which
// is hexOrEmpty's rule for the one box that is about a choice and not a colour.
func gradesOrEmpty(s string) error {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	if _, ok := theme.ParseGrades(s); !ok {
		return fmt.Errorf("must be %s", strings.Join(theme.GradeModes(), ", "))
	}
	return nil
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

// validator is what box key holds a typed value to: a colour, or nothing, and a
// colour the palette would leave out is refused at the box with the reason the
// palette gives (theme.Resolve), instead of saved as a line the preview had
// already drawn a cross for. Judged with everything else typed in the form, since
// whether a colour can stand is a question of the palette it lies in.
//
// The reason is told in the footer, which holds it to one line: it is the first
// words that matter, the fact and not the instruction, and the instruction is
// what the preview's cross and `rta doctor` say.
func (tf *themeForm) validator(key string) func(string) error {
	return func(s string) error {
		if key == theme.GradesKey {
			return gradesOrEmpty(s)
		}
		if err := hexOrEmpty(s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		typed := tf.overrides()
		typed[key] = s
		if _, left := tf.target.palette(typed); left[key].Reason != "" {
			return fmt.Errorf("would be left out: it %s", left[key].Reason)
		}
		return nil
	}
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
// **The merged palette this place would produce, not the typed colours alone.**
// A box left empty shows what the place beneath it gives that field — the
// built-in for the top-level block, and for a profile the operator's own colour
// or the profile's colour as its primary — which is what it will be once saved,
// and a colour that cannot be used is a cross instead of a swatch, whether it is
// not a colour at all or the palette would leave it out (a status stated as
// another: theme.Resolve). The swatch is the one the screen will draw.
//
// One compact strip rather than a column beside the form. A huh field is
// title, description and input across two to four lines depending on how its
// description wraps, and ten of them make a form far taller than ten fixed
// preview lines — lipgloss.JoinHorizontal pads the shorter side, which
// looked fine measured in isolation and unreadable once the two were
// actually side by side: the swatch for "faint" landed next to the
// description text of some field three rows away. A strip above the form has
// no height to disagree about.
func (tf *themeForm) preview(width int) string {
	palette, left := tf.target.palette(tf.overrides())
	parts := make([]string, 0, len(themeFieldOrder))
	for _, key := range themeFieldOrder {
		var swatch string
		if _, out := left[key]; out {
			swatch = theme.WarnText.Render("××××")
		} else {
			// Blank is what the place beneath gives, and a colour that was
			// typed and used is itself: either way, what the palette says.
			swatch = lipgloss.NewStyle().Foreground(lipgloss.Color(palette[key])).Render("████")
		}
		parts = append(parts, swatch+" "+key)
	}
	return packParts(parts, "  ", width)
}

// packParts lays parts out in lines of at most width cells, a part never split
// across two: a swatch parted from its name reads as the swatch of whatever
// name came before it. A width of zero or less is one line, and a part wider
// than a line gets a line to itself.
func packParts(parts []string, sep string, width int) string {
	var lines []string
	line, lineW := "", 0
	for _, p := range parts {
		pw := lipgloss.Width(p)
		switch {
		case line == "":
			line, lineW = p, pw
		case width > 0 && lineW+len(sep)+pw > width:
			lines = append(lines, line)
			line, lineW = p, pw
		default:
			line += sep + p
			lineW += len(sep) + pw
		}
	}
	return strings.Join(append(lines, line), "\n")
}

// themePicker is the choice of whose palette is edited, which the editor opens
// on whenever there is more than one to edit.
type themePicker struct {
	form *huh.Form
	// value is the profile chosen, "" for the top-level block.
	value string
	// targets are the places offered, as they were when the picker opened.
	targets []themeTarget
}

// newThemePicker offers every target, with the environment in force chosen:
// the screen somebody is looking at is the one they most often mean, and
// "global" when nothing is on, which is the only palette there is then.
func newThemePicker(targets []themeTarget, active string) *themePicker {
	tp := &themePicker{targets: targets}
	opts := make([]huh.Option[string], 0, len(targets))
	for _, target := range targets {
		label := "global — the top-level theme:, under every environment"
		if target.name != "" {
			label = target.name
			if target.name == active {
				label += " — switched on"
			}
			if n := target.standing; n > 0 {
				label += " — " + format.CountOf(n, "colour") + " of its own"
			}
		}
		opts = append(opts, huh.NewOption(label, target.name))
		if target.name == active {
			tp.value = active
		}
	}
	tp.form = huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("whose palette is this?").
			Options(opts...).
			Value(&tp.value),
	)).WithTheme(formTheme()).WithShowHelp(false).WithShowErrors(false)
	return tp
}

// startThemeForm opens the theme editor: on a choice of whose palette when
// there is more than one, and on the top-level block's colours when that is all
// there is. A picker with one entry is a question with one answer, which
// profilePicker already declines to ask, and every machine with no profile —
// which is most of them — opens the editor it always had.
//
// Seeded from what is actually written down — not from what is running, for the
// reason themeForm's own doc gives.
func (m Model) startThemeForm() (tea.Model, tea.Cmd) {
	cfg, err := config.LoadFile()
	if err != nil {
		m.refuse("theme not opened: " + err.Error())
		return m, nil
	}
	targets := themeTargets(cfg)
	if len(targets) < 2 {
		return m.openThemeEditor("")
	}
	m.themePick = newThemePicker(targets, m.active)
	m.mode = modeTheme
	m.fitThemeForm()
	return m, m.themePick.form.Init()
}

// openThemeEditor is the editor on one place, read again from the file: a
// profile can have gone, or its block changed, since the picker offered it.
func (m Model) openThemeEditor(name string) (tea.Model, tea.Cmd) {
	m.themePick = nil
	cfg, err := config.LoadFile()
	if err != nil {
		m.refuse("theme not opened: " + err.Error())
		return m.leaveThemeEditor()
	}
	target, ok := themeTargetFor(cfg, name)
	if !ok {
		m.refuse("theme not opened: no profile " + name + " any more")
		return m.leaveThemeEditor()
	}
	m.themeForm = newThemeForm(target)
	m.mode = modeTheme
	m.fitThemeForm()
	return m, m.themeForm.form.Init()
}

// fitThemeForm sizes the embedded huh form to the panel frame. Mirrors
// fitForm exactly — the preview is a strip above the form now, not a column
// beside it, so it costs the form no width of its own, only the two lines
// themeView adds above it.
func (m *Model) fitThemeForm() {
	if m.themePick != nil {
		if m.width > 0 {
			m.themePick.form = m.themePick.form.WithWidth(formWidth(m.width))
		}
		if m.height > 0 {
			m.themePick.form = m.themePick.form.WithHeight(max(m.height-5, 6))
		}
	}
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

// saveTheme writes the submitted overrides into the place that was picked — the
// top-level block, or that profile's — and applies them to this process
// immediately — an operator editing colors wants to see the result, not
// restart rta to find out whether they got it right.
//
// Only the block that was edited is written. The load-modify-write is under the
// lock like every writer's, the profile's other keys are carried as they were,
// and an untouched form writes the same block it read, which the file's own
// writer sees is no change and leaves the text alone. A profile that has gone
// since the picker offered it is not recreated by a save: it is refused.
func (m Model) saveTheme() (tea.Model, tea.Cmd) {
	target := m.themeForm.target
	overrides := m.themeForm.overrides()
	m.themeForm = nil
	m.mode = modeDashboard

	gone := false
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		if target.name == "" {
			cfg.Theme = overrides
			return cfg, true
		}
		p, ok := cfg.Profiles[target.name]
		if !ok {
			gone = true
			return cfg, false
		}
		p.Theme = overrides
		cfg.Profiles[target.name] = p
		return cfg, true
	}); err != nil {
		m.refuse("theme not saved: " + err.Error())
		return m.leaveThemeEditor()
	}
	if gone {
		m.refuse("theme not saved: no profile " + target.name + " any more")
		return m.leaveThemeEditor()
	}

	// The palette of the environment in force, not the block just saved on its
	// own: the colours a person sees after a save are the ones the file now
	// says for where they are, and the next tick would otherwise paint them
	// over a screen that had briefly shown something else. What is reported is
	// the block that was saved: another profile's mistake is not this save's.
	var problems []theme.Problem
	if cfg, err := config.LoadFile(); err == nil {
		for _, p := range m.paint(cfg) {
			if p.Source == target.source {
				problems = append(problems, p)
			}
		}
		if target.name != "" {
			// Not the one in force: nothing was painted for it, and what it
			// would do is judged against its own place all the same.
			problems = append(problems, otherPlace(cfg, target, m.active)...)
		}
	}
	place := ""
	if target.name != "" {
		place = " for " + target.name
	}
	switch {
	// What is said after a save that left something out is that it did, and the
	// command that prints which and why in full. It was the reason itself, and
	// the footer holds a notice to one line: at sixty columns it ended before the
	// words that said what to change. Short enough to end on the command at the
	// narrowest terminal, which is the one part that must not be cut.
	case len(problems) > 0:
		m.flash = "saved, but " + format.CountOf(len(problems), "field") +
			" not applied — see `" + plugin.SurfaceTUI.Call("audit.doctor") + "`"
	case len(overrides) == 0 && target.name == "":
		m.flash = "reset to the built-in theme"
	case len(overrides) == 0:
		m.flash = "removed " + target.name + "'s theme — it draws in your own colours again"
	default:
		m.flash = fmt.Sprintf("saved %d theme %s%s", len(overrides), format.PluralOf(len(overrides), "override"), place)
		if target.name != "" && target.name != m.active {
			m.flash += " — drawn while it is switched on"
		}
	}
	// A save writes the boxes the editor offered, so a status colour a hand put in
	// a block that cannot state one is not carried over: it painted nothing, and
	// what the palette said of it is gone with it. Said, since a line that
	// vanishes unannounced is a line somebody looks for. Not after a notice that
	// ends on a command, which must be the last words on the line.
	if dropped := target.droppedStatuses(); len(dropped) > 0 && len(problems) == 0 {
		why := "the status colours are only set in your own config file"
		if slices.Contains(dropped, theme.GradesKey) {
			why = "the status colours, and how they are drawn, are only set in your own config file"
		}
		m.flash += " — dropped " + strings.Join(dropped, ", ") + ": " + why
	}
	m.tickGen++
	return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.connFor)
}

// otherPlace is the problems of a block saved for a profile that is not the one
// in force, which paint did not look at: the profile is judged as if it were.
func otherPlace(cfg config.Config, target themeTarget, active string) []theme.Problem {
	if target.name == active {
		return nil
	}
	var out []theme.Problem
	for _, p := range profile.ThemeProblems(cfg, target.name) {
		if p.Source == target.source {
			out = append(out, p)
		}
	}
	return out
}

// updateThemePick drives the picker's embedded huh form and dispatches on its
// state, the drive-then-dispatch shape every form-driven mode shares.
func (m Model) updateThemePick(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.themePick.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		m.themePick.form = f
	}
	return m.afterThemePickUpdate(cmd)
}

func (m Model) afterThemePickUpdate(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	switch m.themePick.form.State {
	case huh.StateCompleted:
		return m.openThemeEditor(m.themePick.value)
	case huh.StateAborted:
		return m.leaveThemeEditor()
	}
	return m, cmd
}

// leaveThemeEditor is every way off the theme screen but a save that worked,
// which starts the refresh again itself.
//
// Through backToDashboard and not by setting the mode: the dashboard's refresh
// chain re-arms only while the dashboard is up (the tickMsg case), so the tick
// that fell due while somebody looked at the palette was dropped, and a way back
// that only set the mode left every tile to go stale for good, the switch
// unread with them, until some other screen's way back happened to start it.
func (m Model) leaveThemeEditor() (tea.Model, tea.Cmd) {
	m.themePick, m.themeForm = nil, nil
	return m.backToDashboard()
}

// fastSubmitThemePick accepts the highlighted palette, so the shortcut that
// means "accept what is there" on every form-shaped screen means it here too.
func (m Model) fastSubmitThemePick() (tea.Model, tea.Cmd) {
	m.themePick.form = advanceFormBySyntheticEnter(m.themePick.form)
	return m.afterThemePickUpdate(nil)
}

// updateThemeForm drives the embedded huh form and dispatches on completion,
// mirroring updateForm's shape for the one mode it does not handle.
func (m Model) updateThemeForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.themeForm == nil {
		return m.leaveThemeEditor()
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
		return m.leaveThemeEditor()
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

// themeBoxRows is what the focused field needs on screen to be usable: its
// title, its description and the box itself.
const themeBoxRows = 5

// themeView frames the editor beside its live preview, or the choice of whose
// palette it is on the way there.
func (m Model) themeView() string {
	if m.themePick != nil {
		footer := m.footerFor(modeTheme)
		head := panelHead{Title: "theme", Note: "one palette per environment, over your own"}
		return panel(head, "\n"+m.themePick.form.View(), m.width, m.height-lipgloss.Height(footer), true) + "\n" + footer
	}
	if m.themeForm == nil {
		return ""
	}
	footer := m.footerFor(modeTheme)
	// Wrapped, not truncated: at a narrow width the strip runs onto more
	// lines rather than losing the last few fields off the edge, which is the
	// one place in this screen an operator cannot recover a dropped fact by
	// scrolling. By swatch and name together (packParts), since a plain soft
	// wrap broke between the two.
	strip := m.themeForm.preview(max(m.width-4, 0))
	body := strip + "\n\n" + m.themeForm.form.View()
	// The strip is the preview of what is being typed, and the box is the
	// thing typed into: on a window too short for both, the strip goes, so the
	// focused box is never the part cut off.
	if inner := m.height - lipgloss.Height(footer) - 2; m.height > 0 &&
		inner-lipgloss.Height(strip)-2 < themeBoxRows {
		body = m.themeForm.form.View()
	}
	head := panelHead{Title: "theme", Note: "#rrggbb, or blank for the built-in"}
	if target := m.themeForm.target; target.name != "" {
		head = panelHead{Title: "theme · " + target.name,
			Note: "#rrggbb, or blank for the colours beneath it — yours, then its colour"}
	}
	return panel(head, "\n"+body, m.width, m.height-lipgloss.Height(footer), true) + "\n" + footer
}
