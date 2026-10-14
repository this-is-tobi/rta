package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

// slowClipboard stands in a clipboard program that takes seconds to answer: a
// compositor connection that is slow to come up, a wrapper that waits on a
// lock. internal/clipboard bounds each program at five seconds, and that bound
// is how long the screen used to stop answering keys while it waited.
func slowClipboard(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nsleep 4\ncat > /dev/null\n"
	for _, name := range []string{"pbcopy", "xclip", "xsel", "wl-copy", "clip.exe", "termux-clipboard-set"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// settleCopy runs what a key press returned until the copy reports back, and
// hands that report to the model the way the program's loop would. It gives up
// on a command that does not answer within two seconds, since a batch carries
// commands (a tick, a terminal write) that never produce a message here.
func settleCopy(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	found := make(chan copiedMsg, 1)
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			switch msg := c().(type) {
			case copiedMsg:
				found <- msg
			case tea.BatchMsg:
				for _, sub := range msg {
					run(sub)
				}
			}
		}()
	}
	run(cmd)
	select {
	case msg := <-found:
		next, _ := m.Update(msg)
		return next.(Model)
	case <-time.After(2 * time.Second):
		t.Fatal("no copy reported back")
		return m
	}
}

// pressAndCopy is press for a key that starts a copy: the model after the copy
// has reported back.
func pressAndCopy(t *testing.T, m Model, key string) Model {
	t.Helper()
	next, cmd := m.Update(keyMsg(key))
	return settleCopy(t, next.(Model), cmd)
}

func passwordOnScreen(t *testing.T) Model {
	t.Helper()
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	next, _ := sized.(Model).Update(resultMsg{
		cap: genPasswordCap(), view: view.Table{
			Columns: []view.Column{{Name: "Password"}, {Name: "Entropy (bits)"}},
			Rows:    [][]string{{"qT8!vN2vX9!fL3jRb@Yz", "94.2"}},
		},
	})
	return next.(Model)
}

// A copy waits on a program outside this one, and the program's loop is the
// only thing that draws the screen and reads the keys. The copy ran inside the
// key's own handler, so a slow clipboard froze everything — the spinner, the
// refresh of every tile, the next key — until the program answered or the five
// seconds ran out. The key now returns at once and the copy reports back as a
// message.
func TestACopyDoesNotHoldTheScreenWhileTheClipboardProgramRuns(t *testing.T) {
	slowClipboard(t)
	for name, press := range map[string]func() (Model, tea.Msg){
		"c copies the value": func() (Model, tea.Msg) { return passwordOnScreen(t), tea.KeyPressMsg{Code: 'c', Text: "c"} },
		"y copies the view":  func() (Model, tea.Msg) { return resultOnScreen(t), tea.KeyPressMsg{Code: 'y', Text: "y"} },
	} {
		t.Run(name, func(t *testing.T) {
			m, key := press()
			start := time.Now()
			_, cmd := m.Update(key)
			if took := time.Since(start); took > time.Second {
				t.Errorf("the key held the screen for %s while the clipboard program ran", took)
			}
			if cmd == nil {
				t.Error("nothing was left to do the copy")
			}
		})
	}
}

// What the copy says when it is done reaches the screen, and is the same
// sentence it said when it ran in place.
func TestAFinishedCopyReportsOnTheScreen(t *testing.T) {
	fakeClipboard(t)
	m := passwordOnScreen(t)
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if got := next.(Model).flash; got != "" {
		t.Errorf("flash = %q before the copy has reported", got)
	}
	if got := settleCopy(t, next.(Model), cmd).flash; got != "copied value" {
		t.Errorf("flash = %q, want the copy confirmed", got)
	}

	t.Setenv("PATH", t.TempDir())
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	done := settleCopy(t, next.(Model), cmd)
	if done.flashBad == "" || done.mode != modeResult {
		t.Errorf("flash = %q (bad %q), mode = %v, want the failure said and the result still on screen", done.flash, done.flashBad, done.mode)
	}
}
