package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// A server that starts over a config that does not parse has no snapshot of
// the operator's connections, and a call that names no profile used to run on
// the base connection for as long as the file stayed broken: R5 had nothing to
// hold. The server stays up and serves what a profile has no say over; a call
// to a plugin one governs is refused, and the first read of the file that
// succeeds lifts it with no restart.
func TestACallIsRefusedWhileTheConfigHasNeverRead(t *testing.T) {
	f := newProfileFixture(t, twoProfiles, func(o *Options) {
		o.Profiles = config.Config{}
		o.ProfilesErr = errors.New("parsing config.yaml: unclosed")
	})
	if err := writeFile(f.dir+"/config.yaml", "profiles: [unclosed\n"); err != nil {
		t.Fatal(err)
	}

	res := f.call(t, map[string]any{"sql": "select 1"})
	assertCode(t, res, "core.profile.unreadable")
	if *f.sawHost != "" {
		t.Errorf("the handler ran against %q while nothing was known of the connections", *f.sawHost)
	}
	if text := contentText(t, res); strings.Contains(text, "config.yaml") || strings.Contains(text, f.dir) {
		t.Errorf("the refusal names the operator's file: %s", text)
	}
	if res := f.callOther(t); res.IsError {
		t.Errorf("a capability no profile can govern was refused too: %s", contentText(t, res))
	}

	// The operator fixes the file. Nothing restarts the server.
	if err := writeFile(f.dir+"/config.yaml", twoProfiles); err != nil {
		t.Fatal(err)
	}
	assertCode(t, f.call(t, map[string]any{"sql": "select 1"}), "core.profile.required")
	if res := f.call(t, map[string]any{"sql": "select 1", "profile": "staging"}); strings.Contains(contentText(t, res), "core.profile.unreadable") {
		t.Errorf("still refused as unreadable after the file read again")
	}
}

// A file that read at startup and fails later is answered from what was read:
// that is the case Reload's own comment describes, and it must not turn into a
// refusal because the new field exists.
func TestAFileThatReadAtStartupAndFailsLaterIsAnsweredFromTheSnapshot(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	if err := writeFile(f.dir+"/config.yaml", "profiles: [unclosed\n"); err != nil {
		t.Fatal(err)
	}
	assertCode(t, f.call(t, map[string]any{"sql": "select 1"}), "core.profile.required")
}
