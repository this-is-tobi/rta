package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// askedOf puts a person at the terminal for the length of a test, who answers
// every question with answer, and returns what they were asked.
func askedOf(t *testing.T, answer string, err error) *[]string {
	t.Helper()
	savedTerminal, savedAsk := confirmTerminal, askLine
	t.Cleanup(func() { confirmTerminal, askLine = savedTerminal, savedAsk })
	confirmTerminal = func() bool { return true }
	var asked []string
	askLine = func(_ context.Context, prompt string) (string, error) {
		asked = append(asked, prompt)
		return answer, err
	}
	return &asked
}

// confirmRegistry has the destructive capabilities the question is about: one
// that previews what it would do, one whose target is not there, one from a
// plugin outside this binary, and a counter of what actually ran.
func confirmRegistry(t *testing.T, ran *[]string) *registry.Registry {
	t.Helper()
	reg := registry.New()
	track := func(what string, req plugin.Request) { *ran = append(*ran, what) }
	if err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo plugin",
		Capabilities: []plugin.Capability{
			{
				ID: "demo.item.rm", Summary: "remove an item", Safety: plugin.Destructive,
				Inputs: []plugin.Field{{Name: "id", Type: plugin.String, Positional: true, Required: true}},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					if req.DryRun {
						track("preview", req)
						return view.Text{Body: "would remove item " + req.String("id") + ": past"}, nil
					}
					track("remove", req)
					return view.Text{Body: "removed " + req.String("id")}, nil
				},
			},
			{
				ID: "demo.item.purge", Summary: "purge an item", Safety: plugin.Destructive,
				Inputs: []plugin.Field{{Name: "id", Type: plugin.String, Positional: true, Required: true}},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					if req.DryRun {
						return nil, view.Errorf("demo.notfound", "no item %s", req.String("id"))
					}
					track("purge", req)
					return view.Text{Body: "purged"}, nil
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	external := plugin.Plugin{
		Name: "far", Summary: "outside this binary",
		Capabilities: []plugin.Capability{{
			ID: "far.thing.rm", Summary: "remove a thing", Safety: plugin.Destructive,
			Inputs: []plugin.Field{
				{Name: "name", Type: plugin.String, Positional: true, Required: true},
				{Name: "token", Type: plugin.Secret},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				if req.DryRun {
					track("far preview", req)
					return view.Text{Body: "would remove, says the plugin"}, nil
				}
				track("far remove", req)
				return view.Text{Body: "removed it"}, nil
			},
		}},
	}
	if err := reg.RegisterFrom(external, registry.Origin{Path: "/usr/local/bin/rta-plugin-far", Digest: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// A person at a terminal is shown what would be removed and asked, instead of
// being refused and sent to type the command again with a flag.
func TestADestructiveCommandAsksAPersonAtATerminal(t *testing.T) {
	var ran []string
	reg := confirmRegistry(t, &ran)
	asked := askedOf(t, "y\n", nil)

	out, errOut, err := run(t, reg, "demo", "item", "rm", "6", "--no-color")
	if err != nil {
		t.Fatalf("a yes was refused: %v", err)
	}
	if !strings.Contains(out, "removed 6") {
		t.Errorf("stdout = %q, want the result", out)
	}
	if !strings.Contains(errOut, "would remove item 6: past") {
		t.Errorf("stderr = %q, want the preview the question is about", errOut)
	}
	if strings.Contains(out, "would remove") {
		t.Errorf("the preview went to stdout, where a pipe would carry it: %q", out)
	}
	if len(*asked) != 1 || !strings.Contains((*asked)[0], "[y/N]") {
		t.Errorf("asked %q, want one question ending [y/N]", *asked)
	}
	if strings.Join(ran, ",") != "preview,remove" {
		t.Errorf("ran %v, want the preview and then the removal", ran)
	}
}

// Anything but a yes leaves it alone, says so, and exits as a script that did
// not say --yes does.
func TestAnythingButYesLeavesTheTargetAlone(t *testing.T) {
	for name, c := range map[string]struct {
		answer string
		err    error
	}{
		"no": {"n\n", nil}, "enter": {"\n", nil}, "words": {"maybe\n", nil}, "end of input": {"", io.EOF},
	} {
		t.Run(name, func(t *testing.T) {
			var ran []string
			reg := confirmRegistry(t, &ran)
			askedOf(t, c.answer, c.err)
			out, errOut, err := run(t, reg, "demo", "item", "rm", "6", "--no-color")
			var ve *view.Error
			if !errors.As(err, &ve) || ve.Code != CodeConfirmRequired || ExitCode(err) != 3 {
				t.Fatalf("err = %v (exit %d), want %s and exit 3", err, ExitCode(err), CodeConfirmRequired)
			}
			if strings.Contains(strings.Join(ran, ","), "remove,") || ran[len(ran)-1] != "preview" || out != "" {
				t.Errorf("ran %v, stdout %q: something was removed", ran, out)
			}
			if !strings.Contains(errOut, "Not confirmed") {
				t.Errorf("stderr = %q, want the line saying nothing was changed", errOut)
			}
			// ^D is no newline: the question's line is closed before the next.
			if opened := strings.Contains(errOut, "\n\nNot confirmed"); opened != (c.err != nil) {
				t.Errorf("stderr = %q, the line the question was on was closed: %v, want %v", errOut, opened, c.err != nil)
			}
		})
	}
}

// A target that is not there beats a confirmation of it: the capability's own
// refusal, no question, and the exit of any failed command.
func TestATargetThatIsNotThereIsReportedWithoutAsking(t *testing.T) {
	var ran []string
	reg := confirmRegistry(t, &ran)
	asked := askedOf(t, "y\n", nil)
	_, errOut, err := run(t, reg, "demo", "item", "purge", "99", "--no-color")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "demo.notfound" || ExitCode(err) == 3 {
		t.Fatalf("err = %v (exit %d), want demo.notfound", err, ExitCode(err))
	}
	if len(*asked) != 0 {
		t.Errorf("a target that does not exist was asked about: %q", *asked)
	}
	if !strings.Contains(errOut, "no item 99") {
		t.Errorf("stderr = %q, want the capability's own refusal", errOut)
	}
	if len(ran) != 0 {
		t.Errorf("ran %v after a refusal", ran)
	}
}

// Nothing is asked where nobody is typing, and --yes and --dry-run never ask.
func TestNothingIsAskedWhereNobodyIsTyping(t *testing.T) {
	var ran []string
	reg := confirmRegistry(t, &ran)
	asked := askedOf(t, "y\n", nil)

	confirmTerminal = func() bool { return false }
	_, _, err := run(t, reg, "demo", "item", "rm", "6")
	if ExitCode(err) != 3 {
		t.Errorf("no terminal: exit %d, want 3", ExitCode(err))
	}
	if len(ran) != 0 {
		t.Errorf("no terminal: ran %v, want nothing, not even the preview", ran)
	}

	confirmTerminal = func() bool { return true }
	if _, _, err := run(t, reg, "demo", "item", "rm", "6", "--yes"); err != nil {
		t.Errorf("--yes: %v", err)
	}
	if _, _, err := run(t, reg, "demo", "item", "rm", "6", "--dry-run"); err != nil {
		t.Errorf("--dry-run: %v", err)
	}
	if len(*asked) != 0 {
		t.Errorf("asked %q although nobody was typing, or --yes said it was meant", *asked)
	}
}

// A plugin's preview is its own claim. At a terminal it is shown what the call
// will run with, a credential masked, and the plugin's dry run is not run until
// the person has said yes — and not at all when they have not.
func TestAnExternalPluginIsConfirmedOnItsInputsAlone(t *testing.T) {
	var ran []string
	reg := confirmRegistry(t, &ran)
	askedOf(t, "n\n", nil)
	_, errOut, err := run(t, reg, "far", "thing", "rm", "box", "--token", "hunter2", "--no-color")
	if ExitCode(err) != 3 {
		t.Fatalf("exit %d, want 3", ExitCode(err))
	}
	for _, want := range []string{"outside this binary", "name", "box"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr = %q, want %q", errOut, want)
		}
	}
	if strings.Contains(errOut, "hunter2") || strings.Contains(errOut, "says the plugin") {
		t.Errorf("stderr carries a credential or the plugin's own preview: %q", errOut)
	}
	if len(ran) != 0 {
		t.Errorf("a plugin's dry run ran before anyone said yes: %v", ran)
	}

	askedOf(t, "y\n", nil)
	if _, _, err := run(t, reg, "far", "thing", "rm", "box", "--token", "hunter2"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ran, ",") != "far remove" {
		t.Errorf("ran %v, want the removal alone", ran)
	}
}

// ^C is the natural way to say no, and a read on a terminal does not return for
// a signal: the question stops waiting when the command's context ends, and the
// answer is the one anything that is not a yes gets.
func TestAQuestionStopsWaitingWhenTheCommandIsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	blocked := make(chan struct{})
	defer close(blocked)
	done := make(chan error, 1)
	go func() {
		_, err := readLineOrDone(ctx, func(byte) (string, error) { <-blocked; return "", nil })
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want the context's", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the question went on waiting for a read after the command was interrupted")
	}

	line, err := readLineOrDone(context.Background(), func(byte) (string, error) { return "y\n", nil })
	if line != "y\n" || err != nil {
		t.Errorf("an answer was %q %v", line, err)
	}

	var ran []string
	reg := confirmRegistry(t, &ran)
	askedOf(t, "", context.Canceled)
	_, errOut, err := run(t, reg, "demo", "item", "rm", "6", "--no-color")
	if ExitCode(err) != 3 || !strings.Contains(errOut, "Not confirmed") || strings.Join(ran, ",") != "preview" {
		t.Errorf("an interrupted question: exit %d, stderr %q, ran %v", ExitCode(err), errOut, ran)
	}
}
