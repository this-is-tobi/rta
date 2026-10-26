package tui

import (
	"cmp"
	"context"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The TUI is the terminal's whole event loop, so a key that makes one widget
// loop for good, or panic, takes the program with it and leaves nothing to
// report. Two were found by sending keys at random: alt+b at the start of an
// empty multi-line box, which never returned, and alt+d with the cursor before
// the last character of a single-line one, which ended the program with a stack
// trace. Both are in bubbles, neither is rta's, and a keyboard sends far more
// than anyone tries by hand.
//
// Two targets send it, one into every kind of form and one into the whole
// program, where the bytes the fuzzer chooses are keys and, for the whole
// program, a resize of the terminal now and then. The seeds are the two above
// and a few ordinary walks, which `go test` runs in a moment; the mutating run
// is the scheduled scan's (`make fuzz`).
//
// A form that closes (a submit that reaches its end) is opened again. Commands
// the model asks for are not run: they read files and wait on timers, and a
// hang in what Update does by itself is the question.

// FuzzFormKeys sends keys, from stormKeys, to a form of the kind the first byte
// picks. esc and ctrl+c are not among them: they leave the form, which is not
// what is being asked.
func FuzzFormKeys(f *testing.F) {
	shape := func(name string) byte {
		at := slices.IndexFunc(stormShapes, func(s stormShape) bool { return s.name == name })
		if at < 0 {
			f.Fatalf("no kind of box called %q in stormShapes", name)
		}
		return byte(at)
	}
	keys := keyPicks(f, stormKeys)
	f.Add(shape("text"), keys("alt+b"))
	f.Add(shape("text"), keys("a", "b", "space", "c", "alt+b", "alt+b", "alt+b", "alt+d", "ctrl+w"))
	f.Add(shape("string"), keys("a", "b", "c", "left", "alt+d"))
	f.Add(shape("string"), keys("a", "b", "c", "left", "left", "ctrl+delete"))
	f.Add(shape("options"), keys("x", "down", "down", "enter", "alt+d"))
	f.Add(shape("path"), keys("/", "x", "y", "alt+backspace", "left", "alt+delete"))
	f.Add(shape("int"), keys("1", "9", "left", "alt+d", "home", "alt+d"))
	f.Add(shape("secret"), keys("a", "b", "left", "alt+d", "ctrl+k"))

	f.Fuzz(func(t *testing.T, kind byte, picks []byte) {
		s := stormShapes[int(kind)%len(stormShapes)]
		c := plugin.Capability{
			ID: "demo.storm", Summary: "s", Safety: plugin.Write,
			Inputs: []plugin.Field{s.field, {Name: "after", Type: plugin.String}},
			Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ran"}, nil },
		}
		storm{
			label: s.name + " form",
			start: func() Model { return sizedForm(t, c, 100, 30) },
			msg:   func(pick byte) tea.Msg { return stormKeys[int(pick)%len(stormKeys)] },
			restart: func(m Model) (Model, bool) {
				if m.form == nil || m.mode != modeForm {
					return sizedForm(t, c, 100, 30), true
				}
				return m, false
			},
		}.run(t, picks)
	})
}

// FuzzAppKeys sends the whole program keys, from appKeys, and a resize of the
// terminal for the bytes past them, and renders after each: the dashboard, the
// catalogue and its filter, the panes, the theme, the dialogs.
func FuzzAppKeys(f *testing.F) {
	keys := keyPicks(f, appKeys)
	f.Add(keys("b", "/", "d", "e", "left", "alt+d", "enter", "esc"))
	f.Add(keys("b", "/", "x", "enter", "down", "enter", "tab", "alt+b", "esc", "esc"))
	f.Add(keys("p", "down", "down", "enter", "esc"))
	f.Add(keys("f", "down", "enter", "tab", "a", "left", "alt+d", "esc", "esc"))
	f.Add(keys("t", "down", "enter", "tab", "esc", "esc"))
	f.Add(keys("+", "down", "enter", "esc", "?", "esc"))
	f.Add(keys("/", "n", "e", "t", "alt+d", "enter", "esc"))
	f.Add([]byte{byte(len(appKeys)), byte(len(appKeys) + 1), byte(len(appKeys) + 2), byte(len(appKeys) + 3)})

	f.Fuzz(func(t *testing.T, picks []byte) {
		storm{
			label: "program",
			start: func() Model { m, _ := realModel(t, 100, 30); return m },
			msg:   appMsg,
			// Short, and drawn every few messages: the fuzzer takes an input that
			// runs for seconds to be a hung process and gives up on it, and a
			// render is most of what one message costs.
			most:      250,
			viewEvery: 4,
		}.run(t, picks)
	})
}

// keyPicks is how a seed names keys: the byte that picks each, by what the key
// prints as.
func keyPicks(f *testing.F, from []tea.KeyPressMsg) func(names ...string) []byte {
	return func(names ...string) []byte {
		out := make([]byte, len(names))
		for i, n := range names {
			at := slices.IndexFunc(from, func(k tea.KeyPressMsg) bool { return k.String() == n })
			if at < 0 {
				f.Fatalf("no key called %q", n)
			}
			out[i] = byte(at)
		}
		return out
	}
}

// stormShape is a kind of box a form can hold.
type stormShape struct {
	name  string
	field plugin.Field
}

var stormShapes = []stormShape{
	{"string", plugin.Field{Name: "f", Type: plugin.String}},
	{"text", plugin.Field{Name: "f", Type: plugin.Text}},
	{"int", plugin.Field{Name: "f", Type: plugin.Int}},
	{"float", plugin.Field{Name: "f", Type: plugin.Float}},
	{"bool", plugin.Field{Name: "f", Type: plugin.Bool}},
	{"duration", plugin.Field{Name: "f", Type: plugin.Duration}},
	{"path", plugin.Field{Name: "f", Type: plugin.Path}},
	{"secret", plugin.Field{Name: "f", Type: plugin.Secret}},
	{"stringSlice", plugin.Field{Name: "f", Type: plugin.StringSlice}},
	{"secretSlice", plugin.Field{Name: "f", Type: plugin.SecretSlice}},
	{"options", plugin.Field{Name: "f", Type: plugin.String, Options: []string{"one", "two", "three"}}},
	{"filled text", plugin.Field{Name: "f", Type: plugin.Text, Default: "a few words over one line"}},
}

// modified is code with each modifier a terminal reports, and none.
func modified(codes ...rune) []tea.KeyPressMsg {
	var keys []tea.KeyPressMsg
	for _, code := range codes {
		for _, mod := range []tea.KeyMod{0, tea.ModShift, tea.ModCtrl, tea.ModAlt, tea.ModCtrl | tea.ModAlt} {
			keys = append(keys, tea.KeyPressMsg{Code: code, Mod: mod})
		}
	}
	return keys
}

// control is every control and alt letter, c aside where it leaves.
func control(skip ...rune) []tea.KeyPressMsg {
	var keys []tea.KeyPressMsg
	for r := 'a'; r <= 'z'; r++ {
		if !slices.Contains(skip, r) {
			keys = append(keys, tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}, tea.KeyPressMsg{Code: r, Mod: tea.ModAlt})
		}
	}
	return keys
}

func glyphs(text string) []tea.KeyPressMsg {
	var keys []tea.KeyPressMsg
	for _, r := range text {
		keys = append(keys, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return keys
}

var (
	// moves are the keys that move a cursor, take a step through a form, or
	// edit by position.
	moves = []rune{
		tea.KeyEnter, tea.KeyTab, tea.KeyBackspace, tea.KeyDelete, tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight,
		tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown,
	}

	// stormKeys is what a keyboard sends into a form: the odd glyph, the
	// movement keys alone and with each modifier, and every control and alt
	// letter but c.
	stormKeys = slices.Concat(glyphs("abcxyzAZ019 /.-_~:,*\\é世😀"), modified(moves...), control('c'))

	// appKeys is what it sends into the program, where the letters and the
	// punctuation are commands, and esc and ctrl+c leave a screen or the program.
	appKeys = slices.Concat(
		glyphs("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/?+:-[], "),
		modified(append([]rune{tea.KeyEscape}, moves...)...), control(),
	)

	// sizes are terminals from a sliver to a wide one.
	sizes = func() []tea.WindowSizeMsg {
		var out []tea.WindowSizeMsg
		for _, w := range []int{1, 8, 20, 44, 80, 140, 220} {
			for _, h := range []int{1, 4, 12, 30, 60} {
				out = append(out, tea.WindowSizeMsg{Width: w, Height: h})
			}
		}
		return out
	}()
)

// appMsg is the message a byte stands for: a key, and past the keys a resize.
func appMsg(pick byte) tea.Msg {
	if i := int(pick); i < len(appKeys) {
		return appKeys[i]
	}
	return sizes[(int(pick)-len(appKeys))%len(sizes)]
}

// storm sends a model the messages picks stand for, and fails with the last of
// them when an Update panics or does not return.
type storm struct {
	label string
	start func() Model
	msg   func(pick byte) tea.Msg
	// restart returns a model to carry on with, and true, when the one it was
	// given has left the screen being asked about.
	restart func(Model) (Model, bool)
	// most is how many messages are sent, 3000 where it is left out.
	most int
	// viewEvery renders after that many messages, since a panic in drawing is as
	// final; the end of a run is drawn whatever it is.
	viewEvery int
}

func (s storm) run(t *testing.T, picks []byte) {
	t.Helper()
	if most := cmp.Or(s.most, 3000); len(picks) > most {
		picks = picks[:most]
	}
	m := s.start()

	var (
		mu    sync.Mutex
		trail []string
		count atomic.Int64
		done  = make(chan string, 1)
	)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Sprintf("%v\n%s", r, debug.Stack())
				return
			}
			done <- ""
		}()
		for i, pick := range picks {
			msg := s.msg(pick)
			mu.Lock()
			trail = append(trail, describe(msg))
			if len(trail) > 40 {
				trail = trail[1:]
			}
			mu.Unlock()
			next, _ := m.Update(msg)
			m = next.(Model)
			if s.restart != nil {
				m, _ = s.restart(m)
			}
			if s.viewEvery > 0 && i%s.viewEvery == 0 {
				_ = m.View()
			}
			count.Add(1)
		}
		_ = m.View()
	}()

	lastSeen, lastMove := int64(-1), time.Now()
	for {
		select {
		case r := <-done:
			if r != "" {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("a message panicked the %s after %d: %s\nthe last messages: %s",
					s.label, count.Load(), r, strings.Join(trail, " "))
			}
			return
		case <-time.After(250 * time.Millisecond):
			if n := count.Load(); n != lastSeen {
				lastSeen, lastMove = n, time.Now()
				continue
			}
			if time.Since(lastMove) > 5*time.Second {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("the %s stopped answering after %d messages\nthe last messages: %s",
					s.label, count.Load(), strings.Join(trail, " "))
			}
		}
	}
}

func describe(msg tea.Msg) string {
	switch v := msg.(type) {
	case tea.KeyPressMsg:
		return v.String()
	case tea.WindowSizeMsg:
		return fmt.Sprintf("resize(%dx%d)", v.Width, v.Height)
	}
	return fmt.Sprintf("%T", msg)
}
