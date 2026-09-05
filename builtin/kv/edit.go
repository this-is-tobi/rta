package kv

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/this-is-tobi/rule-them-all/internal/stdio"
	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// shmDir is Linux's RAM-backed temporary filesystem. A secret written there
// is never handed to a block device, so no amount of undeleting or reading
// raw sectors afterwards recovers it — which is the only mitigation that
// actually works, since overwriting a file before removing it proves nothing
// on a copy-on-write or journalling filesystem. pass prefers it for the same
// reason. macOS has no equivalent; its per-user $TMPDIR is already mode 0700,
// which is the next best thing.
const shmDir = "/dev/shm"

// editorCommand resolves which editor to run, and with what.
func editorCommand() []string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		// $EDITOR routinely carries flags — "code --wait", "emacsclient -nw",
		// "subl -w" — so the value is split rather than treated as a program
		// name. Splitting rather than handing it to a shell is deliberate:
		// somebody's $EDITOR is not a script we should be expanding $, ` and
		// ; out of on their behalf.
		if fields := strings.Fields(os.Getenv(env)); len(fields) > 0 {
			return fields
		}
	}
	// vi, not nano: POSIX requires it, so it is the one editor that is
	// certainly installed, and refusing to run until somebody exports a
	// variable is a worse answer than an unfamiliar editor.
	return []string{"vi"}
}

// launchEditor runs the editor against the file and waits for it. Overridable
// in tests, which have neither a terminal to hand over nor an editor to hand
// it to.
var launchEditor = func(argv []string, path string) error {
	cmd := exec.Command(argv[0], append(argv[1:], path)...)
	// The editor gets the terminal, whole. Anything less and a full-screen
	// editor draws into a pipe.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdio.Real(), os.Stdout, os.Stderr
	return cmd.Run()
}

// editDir makes a directory only this user can enter, preferring one that
// never reaches a disk.
//
// A directory rather than a bare temporary file, because editors do not write
// only the file you named: vim leaves .swp and ~ files, emacs leaves #autosave#
// files, and each is a full copy of the plaintext sitting wherever the edited
// file was. Removing the directory removes all of them without having to know
// what any particular editor is called.
func editDir() (string, *view.Error) {
	if fi, err := os.Stat(shmDir); err == nil && fi.IsDir() {
		if dir, err := os.MkdirTemp(shmDir, "rta-kv-"); err == nil {
			return dir, nil
		}
		// Present but not writable — a container with a read-only /dev/shm,
		// say. The ordinary temporary directory is still mode 0700 per user.
	}
	dir, err := os.MkdirTemp("", "rta-kv-")
	if err != nil {
		return "", view.Errorf("kv.edit.notemp", "making a private directory to edit in: %v", err)
	}
	return dir, nil
}

// editFilename names the buffer after the key, sanitised.
//
// The name is cosmetic — it is what the editor puts in its status line, and
// the extension is how it picks a syntax mode, so a JSON credential opens
// with brace matching instead of as one long line. Being cosmetic is exactly
// why it is not the key itself: a key may contain a slash or a leading dot,
// and a filename assembled from user input is not something to hand to
// filepath.Join and hope.
func editFilename(key, kind string) string {
	var sb strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('-')
		}
	}
	name := strings.Trim(sb.String(), "-")
	if name == "" {
		name = "value"
	}
	switch kind {
	case "json":
		name += ".json"
	case "certificate", "private-key", "public-key":
		name += ".pem"
	}
	return name
}

// restoreTrailingNewline undoes the one edit nobody made.
//
// vim — and every editor that believes POSIX about what a text file is —
// writes a final newline whether or not the buffer ended in one. A bearer
// token that came back one byte longer than it went in produced 401s that
// read exactly like the token had been revoked, while the store, `kv show`
// and the byte count all agreed the value was fine.
//
// Only the newline the editor added comes off, and only when the stored value
// had none: a value that ends in a newline on purpose — a PEM bundle, an
// authorized_keys line — keeps it. That is deliberately narrower than a trim.
// `kv set --file` stores exactly what was on disk, and the difference is that
// there the newline belongs to the artifact, while here it belongs to the
// editor.
func restoreTrailingNewline(edited, original []byte) []byte {
	if bytes.HasSuffix(original, []byte("\n")) || !bytes.HasSuffix(edited, []byte("\n")) {
		return edited
	}
	return bytes.TrimSuffix(edited[:len(edited)-1], []byte("\r"))
}

// runEdit opens a value in an editor and stores whatever comes back.
func runEdit(_ context.Context, req plugin.Request) (view.View, error) {
	// An editor is a person at a keyboard. Checked before anything else, so
	// a surface that could never finish the operation does not decrypt the
	// store on the way to finding that out.
	if !canPrompt(req) {
		return nil, view.Errorf("kv.edit.noterminal", "an editor needs a terminal, and there is none here").
			WithHint("at a shell: rta kv edit " + req.String("key") +
				" — anywhere else, send the new value with kv.set")
	}
	if verr := refuseSilentIdentity(req); verr != nil {
		return nil, verr
	}
	key := req.String("key")
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	e, ok := s.Entries[key]
	if !ok {
		return nil, notFound(key)
	}
	// A DER certificate, a PKCS#12 bundle or a JKS keystore opened in a text
	// editor comes back re-encoded, line-ending-normalised and one newline
	// longer — a value that is not the secret any more and cannot be told
	// from it by size. Refused rather than mangled, with the round trip that
	// does preserve bytes named in the hint.
	if !utf8.Valid(e.Value) {
		return nil, view.Errorf("kv.edit.binary", "%q is not text (%s), and an editor would not give it back unchanged", key, e.Kind).
			WithHint("rta kv get " + key + " --out <file>, edit that, then rta kv set " + key + " --file <file>")
	}

	argv := editorCommand()
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would open %q (%s, %s) in %s",
			key, e.Kind, format.Bytes(uint64(len(e.Value))), argv[0])}, nil
	}

	dir, verr := editDir()
	if verr != nil {
		return nil, verr
	}
	// Whatever happens next — the editor crashing, the store refusing to
	// save, a panic on the way out — the plaintext and every backup file
	// beside it go with the directory.
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, editFilename(key, e.Kind))
	if err := os.WriteFile(path, e.Value, 0o600); err != nil {
		return nil, view.Errorf("kv.edit.notemp", "writing the value to edit: %v", err)
	}
	if err := launchEditor(argv, path); err != nil {
		return nil, view.Errorf("kv.edit.failed", "%s: %v", argv[0], err).
			WithHint("nothing was changed — set $EDITOR to something on this machine")
	}
	edited, err := os.ReadFile(path)
	if err != nil {
		// Some editors write by rename and take the original with them if
		// they are killed. Saying so beats "no such file or directory".
		return nil, view.Errorf("kv.edit.gone", "the editor left no file behind: %v", err).
			WithHint("nothing was changed")
	}
	edited = restoreTrailingNewline(edited, e.Value)

	if bytes.Equal(edited, e.Value) {
		// Not a no-op for tidiness: writing here would re-encrypt the whole
		// store and move the entry's Updated stamp, so opening a value to
		// look at it and quitting would age-reset the one column that says
		// how long a token has been sitting there.
		return view.Text{Body: fmt.Sprintf("%q is unchanged", key)}, nil
	}
	// An editor exits 0 having written nothing far more often than anybody
	// means to store an empty secret — `:q!` after a `ggdG`, a crash between
	// truncate and write. kv.set will not accept an empty value either.
	if len(bytes.TrimSpace(edited)) == 0 {
		return nil, view.Errorf("kv.edit.empty", "the editor returned an empty value").
			WithHint("nothing was changed — to delete the entry: rta kv rm " + key)
	}

	// The kind is re-detected, because that is what the edit may have
	// changed: pasting a certificate over a string is a different sort of
	// entry, and `kv list` filtering on a stale label is how you fail to
	// find it. The description, source filename and creation time are the
	// entry's, not the value's, and stay.
	//
	// Re-detection is skipped for a kind that was pinned by hand — see below
	// for how that is told apart. Unconditional re-detection silently threw
	// away `kv set --kind` on the next edit, which is the operator's stated
	// answer being replaced by a guess.
	// Re-read before writing, under the same store lock every other write
	// takes, and apply only this entry.
	//
	// Every other write in this plugin holds that lock for its whole
	// load..save window; this one holds an external editor open for as long
	// as somebody is looking at it, and serializing every other write behind
	// that would be a worse regression than the race being closed here — so
	// only the final re-read-and-save is locked, not the wait for the
	// editor. Before this, the re-read narrowed the window without closing
	// it: a `kv set` issued from another terminal could still land between
	// this Load and the Save below, silently gone the moment this exits with
	// a success message on both. The lock removes that window rather than
	// narrowing it further. Re-reading itself is still needed independent of
	// the lock, to catch a change that already landed *before* the editor
	// exited — that one the lock cannot see, since it was not held yet. It
	// also keeps a concurrent `kv rekey` honest: saving the stale snapshot
	// put its old embedded recipients back, and the next load failed the
	// recipients comparison in crypt.go with "it may have been edited by
	// hand" — an alarming accusation about a legitimate rekey.
	unlock, verr := lockStore()
	if verr != nil {
		return nil, verr
	}
	defer unlock()
	fresh, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	current, ok := fresh.Entries[key]
	switch {
	case !ok:
		return nil, view.Errorf("kv.edit.vanished",
			"%q was removed while the editor was open", key).
			WithHint("nothing was changed — store the edited value with: rta kv set " + key)
	case !bytes.Equal(current.Value, e.Value):
		// Refuse rather than pick a winner: one of the two values is about to
		// be lost either way, and only the person knows which.
		return nil, view.Errorf("kv.edit.conflict",
			"%q changed while the editor was open", key).
			WithHint("nothing was changed — re-run `rta kv edit " + key +
				"` to start from the current value")
	}
	current.Previous = current.retired(time.Now())
	current.Value = edited
	// A pinned kind is one that disagrees with what detection would have said
	// about the value being replaced: nothing but `--kind` could have put it
	// there. Inferring it this way rather than recording a "pinned" flag keeps
	// the stored shape unchanged, and the one state it cannot distinguish — a
	// hand-set kind that happens to equal the detected one — is a state where
	// re-detecting produces the same answer anyway.
	if current.Kind == detectKind(string(e.Value), current.Filename) {
		current.Kind = detectKind(string(edited), current.Filename)
	}
	current.Updated = time.Now()
	fresh.Entries[key] = current
	if verr := save(req, fresh); verr != nil {
		return nil, verr
	}
	e = current
	return view.Text{Body: fmt.Sprintf("updated %q (%s, %s)",
		key, e.Kind, format.Bytes(uint64(len(edited))))}, nil
}
