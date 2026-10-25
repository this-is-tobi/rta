package kv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/itemstore"
	"github.com/this-is-tobi/rta/builtin/internal/pathin"
	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The capability handlers: every verb kv offers, each a load–act–save over
// the store, plus the rendering helpers their views share.

// runList never touches a value: only key names, kinds, sizes, descriptions
// and timestamps leave this function, by construction — see the package doc.
func runList(_ context.Context, req plugin.Request) (view.View, error) {
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	kindFilter := strings.TrimSpace(req.String("kind"))
	// "db" and "db/" both name the folder: a key is never called "db/", so the
	// slash is only how the folder is spelled (checkKeyName).
	folder := strings.TrimSuffix(strings.TrimSpace(req.String("folder")), "/")
	// Matched against the description as well as the name, because the name
	// is the half you have forgotten: "which one was the deploy key for the
	// staging cluster" is answerable from what you wrote down at the time
	// and not from `prod-deploy-key`.
	match := strings.ToLower(strings.TrimSpace(req.String("match")))
	detail := req.Bool("detail")
	if req.Bool("removed") {
		return removedTable(req.Surface(), s), nil
	}

	names := make([]string, 0, len(s.Entries))
	for k, e := range s.Entries {
		if kindFilter != "" && e.Kind != kindFilter {
			continue
		}
		if folder != "" && !strings.HasPrefix(k, folder+"/") {
			continue
		}
		if match != "" && !strings.Contains(strings.ToLower(k), match) &&
			!strings.Contains(strings.ToLower(e.Description), match) {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names)

	// Which environments read each entry — config-side metadata joined in
	// for the person at the terminal, so a listing distinguishes the entries
	// profiles depend on from the leftovers. Withheld over MCP: an agent
	// already sees names and kinds, and "this entry authenticates prod" is
	// reconnaissance it has no call for, while the CLI and TUI user wrote
	// the mapping themselves. A config that will not load costs the column
	// and nothing else — the store's own listing must not break because the
	// config file is broken, and `rta doctor` owns reporting that.
	usedBy := map[string][]string{}
	if req.Surface() != plugin.SurfaceMCP {
		if cfg, err := config.Load(); err == nil {
			usedBy = cfg.KVUsers()
		}
	}
	withUsers, withHistory := false, false
	for _, k := range names {
		if len(usedBy[k]) > 0 {
			withUsers = true
		}
		if len(s.Entries[k].Previous) > 0 {
			withHistory = true
		}
	}

	cols := []view.Column{
		{Name: "Key"},
		{Name: "Kind"},
		{Name: "Size", Kind: view.KindBytes},
		{Name: "Description"},
		{Name: "Updated", Kind: view.KindDuration},
	}
	// How many earlier values each entry still keeps — the column that tells
	// a rotated key from one only ever set, without `kv show` on each. On the
	// same rule as Used By: only once some listed entry keeps one, because a
	// column of blanks names a concept the listing has no use for yet.
	if withHistory {
		cols = append(cols, view.Column{Name: "History"})
	}
	// Only when some listed entry has a user: a column of blanks would name
	// a concept this store does not otherwise have, on every listing, for
	// operators who never wrote a profile.
	if withUsers {
		cols = append(cols, view.Column{Name: "Used By"})
	}
	if detail {
		cols = append(cols, view.Column{Name: "Source"}, view.Column{Name: "Created", Kind: view.KindDuration})
	}
	t := view.Table{Columns: cols}
	for _, k := range names {
		e := s.Entries[k]
		row := []string{plugin.ListedName(k), e.Kind, format.Bytes(len(e.Value)), e.Description, itemstore.Age(e.Updated)}
		if withHistory {
			row = append(row, historyCell(len(e.Previous)))
		}
		if withUsers {
			row = append(row, strings.Join(usedBy[k], ", "))
		}
		if detail {
			row = append(row, e.origin(), itemstore.Age(e.Created))
		}
		t.Rows = append(t.Rows, row)
	}
	t.Total = len(t.Rows)
	// The table even when nothing is listed, and the sentence beside it for a
	// screen: see view.Table.Empty.
	if len(t.Rows) == 0 {
		t.Empty = emptyList(req.Surface(), len(s.Entries), folder, kindFilter, req.String("match"))
	}
	return t, nil
}

// historyCell is the count of earlier values, blank for none — a zero in
// every row would read as a fact about each entry rather than the absence
// of one.
func historyCell(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// runShow describes one entry without revealing it. Size is the closest it
// comes to the value, and a byte count tells you a token is a token without
// telling you which one.
func runShow(_ context.Context, req plugin.Request) (view.View, error) {
	key := req.String("key")
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	e, ok := s.Entries[key]
	if !ok {
		return nil, notFound(req.Surface(), s, key)
	}
	pairs := []view.Pair{
		{Key: "key", Value: key},
		{Key: "kind", Value: e.Kind},
		{Key: "size", Value: format.Bytes(len(e.Value))},
	}
	if e.Description != "" {
		pairs = append(pairs, view.Pair{Key: "description", Value: e.Description})
	}
	if o := e.origin(); o != "" {
		pairs = append(pairs, view.Pair{Key: "source", Value: o})
	}
	if n := len(e.Previous); n > 0 {
		pairs = append(pairs, view.Pair{Key: "history",
			Value: format.CountOf(n, "earlier value") + " — " + req.Surface().Call("kv.history", keyArg(key))})
	}
	// The same join kv.list makes, for one entry — and withheld over MCP for
	// the same reason, argued at runList.
	if req.Surface() != plugin.SurfaceMCP {
		if cfg, err := config.Load(); err == nil {
			if users := cfg.KVUsers()[key]; len(users) > 0 {
				pairs = append(pairs, view.Pair{Key: "used by", Value: strings.Join(users, ", ")})
			}
		}
	}
	pairs = append(pairs,
		view.Pair{Key: "updated", Value: itemstore.Age(e.Updated)},
		view.Pair{Key: "created", Value: itemstore.Age(e.Created)},
	)
	// Both ways out, with the one that shows nothing first: the page you are
	// on exists because you did not want the value on screen, and offering
	// only kv.get from it was an odd thing to end on. Each is the call as
	// the surface asking makes it — and an agent is offered no copy, since
	// kv.copy is HumanOnly and no tool it has: a row naming it sent the agent
	// to `rta kv copy`, a command line it cannot run for a clipboard that is
	// not its own.
	if sf := req.Surface(); sf != plugin.SurfaceMCP {
		pairs = append(pairs, view.Pair{Key: "copy", Value: sf.Call("kv.copy", keyArg(key))})
	}
	pairs = append(pairs, view.Pair{Key: "reveal", Value: req.Surface().Call("kv.get", keyArg(key))})
	return view.KeyValue{Pairs: pairs}, nil
}

func runGet(_ context.Context, req plugin.Request) (view.View, error) {
	key := req.String("key")
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	e, ok := s.Entries[key]
	if !ok {
		return nil, notFound(req.Surface(), s, key)
	}
	out := req.String("out")
	if out == "" {
		return view.Text{Body: string(e.Value)}, nil
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would write %s (%s) to %s", textclean.Record(key), format.Bytes(len(e.Value)), out)}, nil
	}
	// A secret leaving the store for the filesystem lands readable by its
	// owner and nobody else, whatever the umask says — and whatever mode the
	// file already had: os.WriteFile only applies its perm argument to a file
	// it creates, so overwriting an existing world-readable file left it
	// world-readable while this printed "mode 0600" beside it. Same
	// temp-file-plus-rename-plus-explicit-chmod discipline as the store
	// itself, which also makes the write atomic. Exactly the bytes that were
	// stored, too: a round trip, not an editorial pass deciding whether
	// something needed a trailing newline.
	if verr := writeOut(pathguard.ExpandTilde(out), e.Value); verr != nil {
		return nil, verr.WithInput("out")
	}
	return view.Text{Body: fmt.Sprintf("wrote %s to %s (%s, mode 0600)", textclean.Record(key), out, format.Bytes(len(e.Value)))}, nil
}

// writeOut writes a secret to a caller-chosen path at exactly mode 0600,
// regardless of what — if anything — was there before.
func writeOut(path string, data []byte) *view.Error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return view.Errorf("kv.out.unwritable", "creating %s: %v", dir, err)
	}
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return view.Errorf("kv.out.unwritable", "writing %s: %v", path, err)
	}
	return nil
}

// envName turns a prefix and a key into an environment variable name:
// prefix "APP_", key "db-password" becomes APP_DB_PASSWORD. A leading digit
// gets a guard underscore, since a shell will not accept a name starting
// with one.
//
// Both halves go through the identical character whitelist. Key alone used
// to be filtered while prefix was written verbatim — found by review:
// a prefix containing a newline broke `kv env`'s output
// into extra lines, one of which could be a live command substitution,
// directly against the eval "$(rta kv env …)" usage this capability's own
// Description recommends. Filtering the whole name together, rather than
// filtering prefix and key separately and concatenating the results, is
// what keeps a boundary-straddling injection (a prefix ending mid-escape)
// from reopening the same hole a piece at a time.
func envName(prefix, key string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(prefix + key) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	name := sb.String()
	if name != "" && name[0] >= '0' && name[0] <= '9' {
		name = "_" + name
	}
	return name
}

// shellQuote wraps a value so a shell reads it back byte for byte. Single
// quotes protect everything except a single quote, which is spliced in.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func runEnv(_ context.Context, req plugin.Request) (view.View, error) {
	syntax := strings.ToLower(strings.TrimSpace(req.String("format")))
	if syntax == "" {
		syntax = "export"
	}
	if syntax != "export" && syntax != "dotenv" {
		return nil, view.Errorf("kv.env.badformat", "unknown format %q", syntax).
			WithHint("use export (for eval) or dotenv (for a .env file)").WithInput("format")
	}

	keys := req.StringSlice("key")
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	if len(keys) == 0 {
		for k := range s.Entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		// Nothing to export is an empty export, with the sentence beside it
		// for a person (view.Text.Empty). The line "# no keys stored" was
		// the export itself, so `rta kv env > .env` wrote it into the file
		// and -o json handed it to a script as the environment.
		if len(keys) == 0 {
			return view.Text{Empty: emptyList(req.Surface(), 0, "", "", "")}, nil
		}
	}

	prefix := req.String("prefix")
	var sb strings.Builder
	// Which key each variable came from. envName is not one-to-one — case
	// and punctuation fold away, so prod/db-password and prod-db-password
	// are both PROD_DB_PASSWORD — and both lines were printed: eval kept
	// whichever sorted last, a dotenv loader may keep either, and a secret
	// was loaded under another's name with nothing said. Refused rather than
	// warned about, since what is printed is meant to be evaluated or
	// written to a file, where a warning on another stream is not read.
	from := map[string]string{}
	for _, k := range keys {
		e, ok := s.Entries[k]
		if !ok {
			return nil, notFound(req.Surface(), s, k)
		}
		name := envName(prefix, k)
		if other, taken := from[name]; taken {
			if other == k {
				continue
			}
			sf := req.Surface()
			return nil, view.Errorf("kv.env.collision", "%s and %s both become %s", textclean.Record(other), textclean.Record(k), name).
				WithHint("rename one — `" + sf.Call("kv.rename", keyArg(k),
					plugin.Arg{Name: "new-name", Value: "<new-name>", Positional: true}) +
					"` — or export them in separate calls, each with its own " + sf.InputName("prefix"))
		}
		from[name] = k
		if syntax == "export" {
			sb.WriteString("export ")
		}
		sb.WriteString(name)
		sb.WriteString("=")
		sb.WriteString(shellQuote(string(e.Value)))
		sb.WriteString("\n")
	}
	return view.Text{Body: strings.TrimRight(sb.String(), "\n")}, nil
}

// valueToStore resolves the value and where it came from.
//
// `given` reports whether a value was supplied at all. Absent is not an error
// here any more, because it has a second legitimate meaning: an edit that
// changes only what an entry is *labelled*, leaving the secret alone. runSet
// decides which of the two it is, since only it knows whether the entry
// already exists and whether any metadata was named.
//
// filename is the file the value was kept in, and "" for anything else —
// piped among them, when --file named a stream rather than a file on disk.
// `--file /dev/stdin` is how a piped secret is stored (the CLI chapter says
// so), and it was recorded as a file called "stdin": kind "file" for a
// password, since detectKind labels an unrecognised value from disk that way,
// a source naming a file nobody has, and a filename kv edit then re-detected
// the edited value by. /dev/stdin, a /dev/fd/N from a process substitution and
// a named pipe are each where a value came through, not what it was kept in,
// so it is labelled by what it holds, as a typed value is.
func valueToStore(req plugin.Request) (value []byte, filename string, piped, given bool, err error) {
	if path := req.String("file"); path != "" {
		f, err := os.Open(pathguard.ExpandTilde(path))
		if err != nil {
			return nil, "", false, false, view.Errorf("kv.file.unreadable", "reading %s: %v", path, err).WithInput(pathin.Input("file", err))
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			return nil, "", false, false, view.Errorf("kv.file.unreadable", "reading %s: %v", path, err).WithInput(pathin.Input("file", err))
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, "", false, false, view.Errorf("kv.file.unreadable", "reading %s: %v", path, err).WithInput(pathin.Input("file", err))
		}
		// Exactly what was on disk. Trimming a trailing newline was a
		// convenience for text, and it cost every other file its last bytes —
		// a certificate's final DER byte is not whitespace to be tidied away.
		//
		// A descriptor's name says nothing about what came through it either
		// when the shell redirected a file in, `--file /dev/stdin <
		// token.txt`: the descriptor is a regular file, and its name is still
		// "stdin".
		if !info.Mode().IsRegular() || throughDescriptor(path) {
			return data, "", true, true, nil
		}
		return data, filepath.Base(path), false, true, nil
	}
	raw := req.String("value")
	if raw == "" {
		return nil, "", false, false, nil
	}
	return []byte(raw), "", false, true, nil
}

// throughDescriptor reports whether path names one of this process's open
// descriptors rather than a file: /dev/stdin, /dev/fd/N, /proc/self/fd/N.
func throughDescriptor(path string) bool {
	p := filepath.ToSlash(filepath.Clean(path))
	return p == "/dev/stdin" || strings.HasPrefix(p, "/dev/fd/") || strings.HasPrefix(p, "/proc/self/fd/")
}

// checkKeyName refuses the one key shape the folder convention cannot afford.
//
// A "/" in a key is a folder separator: `kv list --match prod/` browses one,
// and a grant scoped `prod/` covers the records under it and nothing else
// (internal/grant's CheckScope and coversFolder). That distinction only holds
// while `prod/` names a folder and never a record — a stored key ending in
// "/" would be both, and a grant naming it would be exact and prefix at once.
//
// Nor white space around a name. Set and rename trimmed the name they were
// given, and the gate judges the name as the call spells it: a call on
// "prod/db " is its own record, which a grant on prod/db does not cover, and
// a person approving it read the record they were shown, not prod/db — yet
// prod/db is what was written. Refused rather than trimmed, so the key a
// grant was judged on is the key written, and a name that reads as another
// never reaches the store. White space as strings.TrimSpace reads it, the
// no-break space included.
//
// Nothing else about a key is constrained. Keys are opaque strings and stay
// that way: a "." or ".." segment is legal here and is simply never swept into
// a folder grant, which is the grant matcher's job rather than this one's.
func checkKeyName(key string) *view.Error {
	if key != strings.TrimSpace(key) {
		return view.Errorf("kv.set.padded", "%s has white space around it, which a key name cannot", textclean.Record(key)).
			WithHint("give the name without it — a key is written as the call names it, and one with " +
				"white space around it reads as the name without")
	}
	if strings.HasSuffix(key, "/") {
		return view.Errorf("kv.set.foldername", "%s ends in a slash, so it names a folder rather than an entry", textclean.Record(key)).
			WithHint("drop the trailing slash — a folder is not stored, it is what the names " +
				"share, and a kv.get grant scoped to " + textclean.Record(key) + " already covers everything under it")
	}
	return nil
}

// originOf records how this value reached the store.
//
// The surface is part of the answer and is the part nothing else preserves: a
// secret an agent wrote over MCP looks identical afterwards to one the
// operator typed, and "which of these did I not put here myself" is a
// reasonable question to be able to ask of your own store. --file is Local, so
// it cannot be the answer on the MCP surface and the two never contend — nor
// can piped, which is --file naming a stream (valueToStore).
func originOf(req plugin.Request, filename string, piped bool) string {
	if filename != "" {
		// The basename only, matching Filename: which file it was is worth
		// recording and where it sat on disk is not, and the store should not
		// grow a copy of somebody's directory layout.
		return "file:" + filename
	}
	if piped {
		return "piped"
	}
	if req.Surface() == plugin.SurfaceMCP {
		return "agent"
	}
	return "typed"
}

// prefillSet hands interactive surfaces what an entry is currently labelled,
// so the form somebody opens on an existing key shows what is there instead
// of four empty boxes that overwrite it.
//
// **The value is deliberately absent, and that is the whole design of this
// function.** Every other Prefill in the codebase returns the record's
// content; returning a secret here would put decrypted plaintext into a form
// box on a screen, which is the one thing the kv row actions are built to
// avoid — nothing on that screen shows a value, and `v` to reveal is a
// separate, deliberate act. An empty value box also means the ordinary
// submit path stays a relabel unless somebody types a new secret, which is
// exactly the behaviour runSet now implements.
//
// A store that will not open is answered with no defaults rather than an
// error. The form is blocked outright on a Prefill failure (startFormWith),
// and blocking it would be a regression on a locked store from what this did
// before there was any prefill at all — opening blank. The passphrase is
// asked for on submit either way.
func prefillSet(_ context.Context, req plugin.Request) (map[string]any, error) {
	key := req.String("key")
	if key == "" {
		return map[string]any{}, nil
	}
	s, verr := load(req)
	if verr != nil {
		return map[string]any{}, nil //nolint:nilerr // Prefill is a convenience: an unreadable store opens an empty form, and the run then refuses with the real message
	}
	e, ok := s.Entries[key]
	if !ok {
		// A key being invented has nothing to show, and seeding the previous
		// row's labels onto a new entry would be worse than blank.
		return map[string]any{}, nil
	}
	return map[string]any{"description": e.Description, "kind": e.Kind}, nil
}

func runSet(_ context.Context, req plugin.Request) (view.View, error) {
	key := req.String("key")
	if key == "" {
		return nil, view.Errorf("kv.set.nokey", "key is empty").WithInput("key")
	}
	if verr := checkKeyName(key); verr != nil {
		return nil, verr.WithInput("key")
	}
	if verr := refuseSilentIdentity(req); verr != nil {
		return nil, verr
	}
	value, filename, piped, given, err := valueToStore(req)
	if err != nil {
		return nil, err
	}
	if given && filename == "" && !piped {
		noteValueInArgv(req, key)
	}
	kind := strings.TrimSpace(req.String("kind"))
	if kind != "" && !slices.Contains(kinds, kind) {
		return nil, view.Errorf("kv.set.badkind", "unknown kind %q", kind).
			WithHint("use one of: " + strings.Join(kinds, ", ")).WithInput("kind")
	}
	// A call that names neither a value nor anything to relabel is asking for
	// nothing at all, and saying so beats storing an empty secret.
	label := kind != "" || req.String("description") != ""
	if !given && !label {
		// Unless a person is there to ask. A value typed after the key is in
		// the shell's history, and in ps for as long as the call runs — the
		// leak this store exists to close — and the ways around it, a file or
		// a pipe into --file /dev/stdin, need it somewhere else first. At a
		// terminal it is asked for with echo off, once the key and every input
		// have been checked and before the store is locked, so no other kv
		// call waits on the lock while a person types. Only there: an agent
		// has its argument, the TUI its masked box, and a script with no
		// terminal on its standard input the refusal below, which is what it
		// always had.
		//
		// The passphrase is settled first (lockBeforeValue): the secret is
		// typed once the store is known to open, never before.
		if verr := lockBeforeValue(req); verr != nil {
			return nil, verr
		}
		typed, verr := askValue(req, key)
		if verr != nil {
			return nil, verr
		}
		value, given = typed, typed != nil
	}
	if !given && !label {
		// file is Local: an agent's schema has no such argument, so an agent
		// is offered the value alone.
		sf := req.Surface()
		give := "give " + sf.ArgumentName("value")
		switch sf {
		case plugin.SurfaceMCP:
		case plugin.SurfaceCLI:
			// The pipe first: it is the way to give a value that is in nobody's
			// history, where an argument is in the shell's and in `ps`. The TUI
			// has no pipe to offer.
			give = "pipe it in with " + sf.InputName("file") + " /dev/stdin, " + give + ", or read one from disk with " +
				sf.InputName("file") + " <path>"
		default:
			give += ", or " + sf.InputName("file") + " to read one from disk"
		}
		return nil, view.Errorf("kv.set.novalue", "no value given").
			WithHint(give + " — or " + sf.InputName("description") + " or " + sf.InputName("kind") +
				" to change what an existing entry is labelled without touching the secret")
	}
	if given && kind == "" {
		kind = detectKind(string(value), filename)
	}
	if req.DryRun && given {
		return wouldSet(key, kind, value), nil
	}

	unlock, verr := lockStore()
	if verr != nil {
		return nil, verr
	}
	defer unlock()
	// Read before the load: whether this call is the one that makes the store.
	// The form's box and the --passphrase flag hand over one answer for a store
	// not made yet (newStorePassphraseField), and the receipt was the same line
	// as for a tenth entry, so a person who mistyped it learned there was a
	// store, and that nothing recovers its passphrase, from the first call that
	// would not open it.
	madeStore := !fileExists(storePath())
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	now := time.Now()
	previous, existed := s.Entries[key]
	if !given && !existed {
		// A description or a kind for a key the store does not hold is a new
		// entry labelled as it is made, and only the store could say the key
		// was new. Refused, its hint sent the person at the terminal to type
		// the value after the key, into the shell's history the prompt above
		// keeps it out of. So it is asked for here instead, under the lock as
		// the passphrase before it was — and a first store has not asked for its
		// passphrase yet, which is chosen before the value is typed as it is above.
		if !fileExists(storePath()) {
			if verr := lockBeforeValue(req); verr != nil {
				return nil, verr
			}
		}
		typed, verr := askValue(req, key)
		if verr != nil {
			return nil, verr
		}
		if typed != nil {
			value, given = typed, true
			if kind == "" {
				kind = detectKind(string(value), "")
			}
			if req.DryRun {
				return wouldSet(key, kind, value), nil
			}
		}
	}

	var e entry
	if !given {
		if !existed {
			sf := req.Surface()
			return nil, view.Errorf("kv.set.unknown", "%s is not in the store", textclean.Record(key)).
				WithHint("give " + sf.ArgumentName("value") + " to create it — " + sf.InputName("description") +
					" and " + sf.InputName("kind") + " change what an entry already holding a secret is " +
					"labelled, and there is nothing to label yet")
		}
		// The secret, where it came from, and both timestamps are untouched.
		// Updated especially: `kv list`'s Updated column is how you see that a
		// token has been sitting there for fourteen months, and a description
		// somebody corrected is not a rotation — the same reasoning kv.rename
		// already records for leaving that column alone when a name changes.
		e = previous
		if kind != "" {
			e.Kind = kind
		}
		if d := req.String("description"); d != "" {
			e.Description = d
		}
		if req.DryRun {
			return view.Text{Body: fmt.Sprintf("would relabel %s (%s)", textclean.Record(key), e.Kind)}, nil
		}
	} else {
		e = entry{
			Value: value, Kind: kind, Filename: filename, Origin: originOf(req, filename, piped),
			Description: req.String("description"), Created: now, Updated: now,
		}
		if existed {
			e.Created = previous.Created
			e.Previous = previous.retired(now)
			// An edit that says nothing about the description keeps the old one:
			// re-setting a rotated token should not silently erase what it is for.
			if e.Description == "" {
				e.Description = previous.Description
			}
		}
	}
	s.Entries[key] = e
	if verr := save(req, s); verr != nil {
		return nil, verr
	}

	var msg string
	switch {
	case !given:
		msg = fmt.Sprintf("relabelled %s (%s) — the secret is unchanged", textclean.Record(key), e.Kind)
	case existed:
		msg = fmt.Sprintf("updated %s (%s, %s)", textclean.Record(key), kind, format.Bytes(len(value)))
	default:
		msg = fmt.Sprintf("set %s (%s, %s)", textclean.Record(key), kind, format.Bytes(len(value)))
	}
	if madeStore {
		msg += "\nmade a new store — locked with the passphrase this call used, and nothing recovers it"
	}
	if specs := req.StringSlice("recipient"); len(specs) > 0 {
		msg += "\nstore re-encrypted — " + req.Surface().CapabilityName("kv.recipients") + " lists who can read it"
	}
	return view.Text{Body: msg}, nil
}

// noteValueInArgv tells the person at a terminal, on stderr and in one line,
// that the value they gave after the key is in their shell's history and in
// the process list, and that the prompt is the way to give it instead.
//
// The receipt was the same line as for a value nobody could see, and this
// store's whole reason to exist is that a secret typed on a command line is
// kept in both of those places. It is said only where it can be acted on: the
// prompt is the CLI's, at a terminal (canPrompt), and a script with none, an
// agent and the TUI's masked box have their own ways to give a value and no
// shell history of the kind to warn about. A value that came from a file or a
// pipe never was in argv and is not mentioned. It is a note and not a refusal
// like `profile set`'s: by the time rta runs, the value is in the history
// already, and refusing it would protect nothing.
//
// The key is named as it is spelled on the line, and the value is never
// repeated.
func noteValueInArgv(req plugin.Request, key string) {
	if !canPrompt(req) {
		return
	}
	fmt.Fprintf(os.Stderr, "a value given on the command line is in your shell history and in the process list; "+
		"`%s` prompts for it instead, with echo off\n", req.Surface().Call("kv.set", keyArg(key)))
}

// wouldSet is a dry run's answer for a value it was given or was typed.
func wouldSet(key, kind string, value []byte) view.View {
	return view.Text{Body: fmt.Sprintf("would set %s (%s, %s)", textclean.Record(key), kind, format.Bytes(len(value)))}
}

// askValue is the value a person at the terminal types for key, or nil when
// nobody is there to ask (canPrompt) or they answered nothing: an empty
// answer, or none, is no value, and the caller refuses it as it would an
// empty one.
//
// The prompt reads one line, and a value pasted at it that spans lines is cut
// to its first. The rest used to go on to whatever reads the terminal next —
// the passphrase prompt that followed this one, which took a service account's
// JSON key's second line as the passphrase and, for a store not yet made,
// locked it with that, or the shell, which runs each line and keeps it in its
// history. The passphrase is asked for before the value now (lockBeforeValue),
// so only the shell is left to read on, and the rest is dropped all the same:
// the prompt reads on while the paste is still arriving and drops it
// (stdio.ReadSecretLine), and a line that came with more is refused whatever it
// holds: the first line of a kubeconfig is a value of its own to look at, and
// not the one pasted.
//
// A paste slower than the moment the prompt waits still has the rest go on,
// and nothing here can take those lines back. A first line that only opens a
// value is never one on its own, though, so it is refused rather than stored
// as though it were the whole, and the refusal says where the rest may have
// gone.
func askValue(req plugin.Request, key string) ([]byte, *view.Error) {
	if !canPrompt(req) {
		return nil, nil
	}
	typed, more, err := promptValue(key)
	if err != nil {
		// A terminal that could not be read: nothing was typed, which is the
		// empty answer's refusal rather than a read error of its own, as ^D
		// on an empty line already is (stdio.ReadSecretLine).
		typed = nil
	}
	file := req.Surface().InputName("file")
	switch {
	case more:
		return nil, view.Errorf("kv.set.multiline",
			"what was pasted for %s spans lines, and the prompt reads one", textclean.Record(key)).
			WithHint("nothing was stored, and the lines that arrived with the first were read and dropped " +
				"rather than left for your shell — any that arrived later, from a paste slower than the " +
				"prompt waits, went on to it; give a value that spans lines with " + file)
	case len(typed) == 0:
		return nil, nil
	case opensBlock(typed):
		return nil, view.Errorf("kv.set.multiline",
			"what was typed for %s opens a value that spans lines, and the prompt reads one", textclean.Record(key)).
			WithHint("nothing was stored, and any of the value's other lines the prompt did not read " +
				"went on to your shell, whose history may now hold them; give a value that spans " +
				"lines with " + file)
	}
	return typed, nil
}

// opensBlock reports whether line is the first line of a value that spans
// lines and nothing on its own: a PEM block's armour, a certificate's or a
// key's, or the lone brace or bracket a JSON document opens with.
func opensBlock(line []byte) bool {
	t := string(bytes.TrimSpace(line))
	return strings.HasPrefix(t, "-----BEGIN ") || t == "{" || t == "["
}

// runRename moves an entry to another name inside the store.
//
// Nothing about the secret changes, and that is the point: the value is never
// decrypted into anything but this process's memory, so unlike the get-set-rm
// dance it replaces, no part of it reaches a shell's history or another
// command's argv.
//
// The timestamps travel unchanged, including Updated. A name is not a
// rotation, and `kv list`'s Updated column is the one place you can see that
// a token has been sitting there for fourteen months — a rename that reset it
// would quietly answer "yesterday" to the only question that column exists
// to answer.
func runRename(_ context.Context, req plugin.Request) (view.View, error) {
	from, to := req.String("key"), req.String("new-name")
	if from == "" || to == "" {
		return nil, view.Errorf("kv.rename.noname", "rename needs a key and a new name").
			WithHint("give " + req.Surface().ArgumentName("key") + " and " + req.Surface().ArgumentName("new-name"))
	}
	if from == to {
		return nil, view.Errorf("kv.rename.samename", "%s is already its name", textclean.Record(from)).WithInput("new-name")
	}
	// The same guard set has: a rename is the other way to arrive at a name.
	if verr := checkKeyName(to); verr != nil {
		return nil, verr.WithInput("new-name")
	}
	if verr := refuseSilentIdentity(req); verr != nil {
		return nil, verr
	}
	unlock, verr := lockStore()
	if verr != nil {
		return nil, verr
	}
	defer unlock()
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	e, ok := s.Entries[from]
	if !ok {
		return nil, notFound(req.Surface(), s, from)
	}
	// Refused, never confirmed. The overwrite would destroy the secret under
	// the target name with no history and no undo, which is exactly what
	// kv.rm is Destructive for — and a grant scoped to the key being renamed
	// says nothing at all about the one being clobbered.
	if _, taken := s.Entries[to]; taken {
		return nil, view.Errorf("kv.rename.taken", "%s already exists", textclean.Record(to)).
			WithHint("renaming onto it would destroy the secret it holds — remove that first: `" +
				req.Surface().Call("kv.rm", keyArg(to)) + "`").WithInput("new-name")
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would rename %s to %s (%s, %s)",
			textclean.Record(from), textclean.Record(to), e.Kind, format.Bytes(len(e.Value)))}, nil
	}
	delete(s.Entries, from)
	s.Entries[to] = e
	if verr := save(req, s); verr != nil {
		return nil, verr
	}
	return view.Text{Body: fmt.Sprintf("renamed %s to %s — anything still asking for %s will not find it",
		textclean.Record(from), textclean.Record(to), textclean.Record(from))}, nil
}

func runRemove(_ context.Context, req plugin.Request) (view.View, error) {
	if verr := refuseSilentIdentity(req); verr != nil {
		return nil, verr
	}
	unlock, verr := lockStore()
	if verr != nil {
		return nil, verr
	}
	defer unlock()
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	key := req.String("key")
	purge := req.Bool("purge")
	e, ok := s.Entries[key]
	if !ok {
		// --purge also finishes off something removed earlier, which is the
		// one case a key can be absent from the listing and still be here.
		if r, removed := s.Removed[key]; removed && purge {
			if req.DryRun {
				return view.Text{Body: fmt.Sprintf("would purge the removed %s (%s)", textclean.Record(key), r.Kind)}, nil
			}
			delete(s.Removed, key)
			if verr := save(req, s); verr != nil {
				return nil, verr
			}
			return view.Text{Body: fmt.Sprintf("purged %s — it was removed %s, and is gone now", textclean.Record(key),
				itemstore.Age(r.RemovedAt))}, nil
		}
		return nil, notFound(req.Surface(), s, key)
	}
	if req.DryRun {
		if purge {
			return view.Text{Body: fmt.Sprintf("would purge %s (%s) — no restore", textclean.Record(key), e.Kind)}, nil
		}
		return view.Text{Body: fmt.Sprintf("would remove %s (%s) — restorable with `%s`", textclean.Record(key), e.Kind,
			req.Surface().Call("kv.restore", keyArg(key)))}, nil
	}
	delete(s.Entries, key)
	if purge {
		delete(s.Removed, key)
	} else {
		if s.Removed == nil {
			s.Removed = map[string]removedEntry{}
		}
		// A second removal of a reused name replaces the first: one slot per
		// name is the whole promise, and the earlier one has had its chance.
		s.Removed[key] = removedEntry{entry: e, RemovedAt: time.Now()}
	}
	if verr := save(req, s); verr != nil {
		return nil, verr
	}
	if purge {
		return view.Text{Body: fmt.Sprintf("purged %s — the value and its history are gone", textclean.Record(key))}, nil
	}
	sf := req.Surface()
	return view.Text{Body: fmt.Sprintf("removed %s — `%s` brings it back; `%s` would not have", textclean.Record(key),
		sf.Call("kv.restore", keyArg(key)), sf.Call("kv.rm", keyArg(key), plugin.Arg{Name: "purge", Value: true}))}, nil
}

// runInit chooses how the store is locked, once.
//
// It refuses to touch a store that already exists. Re-keying an existing
// store is a different operation — it has to decrypt everything first, which
// means proving you can — and kv.rekey is that operation. Silently
// re-initialising would produce a recipients file describing a store none of
// those recipients can open.
func runInit(_ context.Context, req plugin.Request) (view.View, error) {
	sf := req.Surface()
	if fileExists(storePath()) {
		return nil, view.Errorf("kv.init.exists", "a store already exists at %s", storePath()).
			WithHint("to change the lock on it: " + sf.CapabilityWith("kv.rekey", "generate") +
				" adds a key, and with " + sf.InputName("only") + " as well switches the lock to it")
	}
	if specs, verr := loadRecipients(); verr == nil && len(specs) > 0 {
		return nil, view.Errorf("kv.init.exists", "this store is already set up for keys").
			WithHint(sf.CapabilityName("kv.recipients") + " lists them; delete " + recipientsPath() + " to start over")
	}

	generate := req.Bool("generate")
	identity := strings.TrimSpace(req.String("identity"))
	if !generate && identity == "" && os.Getenv(identityEnv) == "" {
		return nil, view.Errorf("kv.init.nokey", "name a key, or generate one").
			WithHint(sf.InputName("generate") + " makes a key for this store, or " + identityName(sf) +
				" names one already held, e.g. ~/.ssh/id_ed25519 — or skip init: " + NoStoreNext(sf, "<key>"))
	}

	var generated string
	if generate {
		if identity != "" {
			return nil, view.Errorf("kv.init.bothkeys", "%s makes a key; %s names one",
				sf.InputName("generate"), identityName(sf)).
				WithHint("pick one")
		}
		generated = defaultIdentity()
		if req.DryRun {
			return view.Text{Body: "would generate a key at " + generated + " and lock the store to it"}, nil
		}
		if _, verr := generateIdentity(sf, generated); verr != nil {
			return nil, verr
		}
	}
	if req.DryRun {
		return view.Text{Body: "would lock the store to " + identityPath(req)}, nil
	}

	// Writing the empty store is what commits the choice: save() resolves the
	// recipients and records them only once the ciphertext is safely on disk.
	if verr := save(req, store{Entries: map[string]entry{}}); verr != nil {
		return nil, verr
	}
	body := "the store is locked to keys — " + sf.CapabilityName("kv.recipients") + " lists them\n"
	switch {
	case generated != "":
		body += "\ngenerated a key at " + generated + " (mode 0600)\n" +
			"back it up: losing it loses every secret in the store, and nobody can help you\n" +
			"it is found automatically, so " + sf.CapabilityName("kv.set") + " and " +
			sf.CapabilityName("kv.get") + " need no key named"
	default:
		body += "\nusing " + identityPath(req)
		if sf != plugin.SurfaceMCP {
			body += "\nset " + identityEnv + " to that path to skip " + sf.InputName("identity") + " from now on"
		}
	}
	return view.Text{Body: body}, nil
}

// runRekey changes the lock on a store that already exists.
//
// It is the operation kv.init cannot be: init decides the lock when there is
// nothing to lose, and this one re-encrypts secrets that are already there.
// The two irreversible halves — reading it, and keeping a key you hold — are
// checked before anything is written, in that order.
func runRekey(_ context.Context, req plugin.Request) (view.View, error) {
	sf := req.Surface()
	if !fileExists(storePath()) {
		// Both locks are offered, the key first: whoever reached for a re-key was
		// choosing how the store is locked, and `kv set` alone would answer a
		// `--generate` with the one lock they were not asking for.
		return nil, view.Errorf("kv.rekey.nostore", "no store yet — nothing to re-key").
			WithHint(sf.CapabilityWith("kv.init", "generate") + " makes one locked to a key — or " +
				NoStoreNext(sf, "<key>"))
	}
	generate := req.Bool("generate")
	adding := req.StringSlice("recipient")
	only := req.Bool("only")
	if !generate && len(adding) == 0 {
		// The private key file, not its .pub: naming a public key alone
		// proves nothing about holding it, and the lockout guard below needs
		// proof. recipient is Local, so over MCP a key already held is the
		// operator's to add.
		held := "or " + sf.InputName("recipient") + " one you already have, e.g. ~/.ssh/id_ed25519"
		if sf == plugin.SurfaceMCP {
			held = "and one already held is the operator's to add, from their terminal"
		}
		return nil, view.Errorf("kv.rekey.nokey", "name a key, or generate one").
			WithHint(sf.InputName("generate") + " adds a key made for this store, " + held)
	}

	unlock, verr := lockStore()
	if verr != nil {
		return nil, verr
	}
	defer unlock()
	// Read it first, with whatever opens it today. Generating a key changes
	// what `--identity`-less commands resolve to, so the order matters: a
	// store you cannot open is a store you cannot re-key, and finding that
	// out after writing a new key beside the config would be a mess to undo.
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	_, stored, verr := currentMode()
	if verr != nil {
		return nil, verr
	}

	// **The base set has to agree with the ciphertext before it may be a
	// base.** Without --only, this re-key starts from kv.recipients and adds
	// to it — and kv.recipients is plaintext with no cryptographic tie to the
	// store, writable by anyone who can write the data directory without ever
	// holding a key — a writer that cannot read. writeKeys refuses
	// exactly that divergence on an ordinary write; re-key computes its own
	// recipient set and so never reached that guard, which made it the way
	// past it: append one line to kv.recipients, wait for the operator to run
	// any `kv rekey`, and every secret is re-encrypted to the new reader with
	// nothing on screen to say so.
	//
	// --only is deliberately exempt, and it is the documented recovery: it
	// discards the stored set entirely and uses only what was named on the
	// command line, so nothing untrusted reaches the new recipients. That is
	// what the mismatch hint tells people to run, here and in writeKeys, and
	// refusing it would leave a tampered file unfixable.
	if !only && s.Recipients != nil && !slices.Equal(stored, s.Recipients) {
		return nil, view.Errorf("kv.recipients.mismatch",
			"kv.recipients does not match who the store is actually encrypted to, "+
				"so it cannot be the set this re-key builds on").
			WithHint("something other than kv.rekey edited it — compare " + sf.CapabilityName("kv.recipients") +
				" against what you expect, then name the set you want outright — " +
				rekeyOnly(sf, "each key that should read it"))
	}
	var want []string
	if !only {
		want = append(want, stored...)
	}
	held := heldHere(req)
	for _, spec := range adding {
		_, canonical, err := parseRecipient(spec)
		if err != nil {
			return nil, view.Errorf("kv.recipient.invalid", "%v", err).
				WithHint(sf.InputName("recipient") + " takes an age recipient, an SSH public key, or a path to either — " +
					"including the private key itself, whose public half is all that is read").WithInput("recipient")
		}
		want = mergeSpec(want, canonical)
		// Naming a private key you have is proof you have it, which is what
		// the guard below is really asking about.
		if privateKeyFile(spec) {
			held = append(held, canonical)
		}
	}
	// --identity never changes the set either. It says which private key is here —
	// which is what opens the store now, and the only way to *prove* a key is
	// yours, since a public key on its own shows nothing of the sort. Letting
	// it also mean "and keep this reader" would make `--only --generate`
	// unable to switch anything, because opening the store would preserve the
	// very key the switch is leaving behind.
	//
	// A generated key is held by definition, so this can only fail when the
	// set was named entirely from keys nothing here can open.
	if !generate && !canRead(want, held) {
		return nil, view.Errorf("kv.rekey.lockout", "nothing you hold could open the store afterwards").
			WithHint("give " + sf.InputName("generate") + ", or name the private half of a key you have in " +
				identityName(sf) + ", e.g. ~/.ssh/id_ed25519")
	}
	if req.DryRun {
		return view.Text{Body: rekeyPreview(generate, only, want, stored)}, nil
	}
	// A store with no recipients is the passphrase's: whatever it is re-keyed
	// to, the passphrase stops being one of the ways in.
	passphraseLock := len(stored) == 0

	var generated string
	if generate {
		generated = defaultIdentity()
		spec, verr := generateIdentity(sf, generated)
		if verr != nil {
			return nil, verr
		}
		want = mergeSpec(want, spec)
	}

	recipients, verr := recipientsFor(want)
	if verr != nil {
		return nil, verr
	}
	// Rekey is the one operation allowed to change who the store is
	// encrypted to — set the embedded record to match what is actually
	// being committed here, the same as an ordinary write does for itself.
	s.Recipients = want
	if verr := saveTo(sf, s, recipients, want); verr != nil {
		return nil, verr
	}
	// What opens the store just changed; a session remembering the old
	// answer would fail on its next use, and that is not a wrong-passphrase
	// message an operator should have to decode after a rekey they asked for.
	forgetSession()
	return view.Text{Body: rekeySummary(sf, generated, want, stored, passphraseLock)}, nil
}

// dropped returns the recipients in stored that the new set leaves out.
func dropped(want, stored []string) []string {
	var out []string
	for _, spec := range stored {
		if !canRead([]string{spec}, want) {
			out = append(out, spec)
		}
	}
	return out
}

func rekeyPreview(generate, only bool, want, stored []string) string {
	body := "would re-encrypt the store to " + format.CountOf(len(want)+boolToInt(generate), "key")
	if generate {
		body += "\ngenerating a key at " + defaultIdentity()
	}
	if len(stored) == 0 {
		body += "\nthe passphrase would no longer open the store"
		if generate {
			body += " — the key would, and anything running as you can read the key file"
		}
	}
	if only {
		if gone := dropped(want, stored); len(gone) > 0 && !generate {
			body += "\ndropping " + format.CountOf(len(gone), "reader") + " — they could no longer open it"
		} else if len(stored) > 0 && generate {
			body += "\ndropping " + format.CountOf(len(stored), "reader") + " — only the new key would open it"
		}
	}
	return body
}

func rekeySummary(sf plugin.Surface, generated string, want, stored []string, passphraseLock bool) string {
	body := format.CountOf(len(want), "key") + " can open the store — " + sf.CapabilityName("kv.recipients") + " lists them"
	if passphraseLock {
		body += "\nthe passphrase no longer opens the store"
	}
	if generated != "" {
		body += "\n\ngenerated a key at " + generated + " (mode 0600)\n" +
			"back it up: losing it loses every secret it is the only key to\n" +
			"it is found automatically, so nothing needs to name it — and anything running as you can read the key file and open the store"
	}
	if gone := dropped(want, stored); len(gone) > 0 {
		body += "\n\ndropped " + format.CountOf(len(gone), "reader") + ": they cannot open the store from now on.\n" +
			"copies made before now are unaffected — re-keying changes the lock, not the backups."
	}
	return body
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// runStatus reports on the store without opening it. Every fact here comes
// from the file's metadata or from the recipients list, which is public by
// construction — so this is the one kv capability that always works, whatever
// the store holds and whatever is at hand to open it. What is inside is
// kv.overview's to count, and it carries these lines.
func runStatus(_ context.Context, req plugin.Request) (view.View, error) {
	facts, verr := readStoreFacts(req)
	if verr != nil {
		return nil, verr
	}
	pairs := slices.Clone(facts.where)
	if facts.mode != "" {
		pairs = append(pairs, facts.lock(unlockAvailability(req, facts.mode))...)
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// storeFacts is everything that can be said of the store without opening it:
// where it is, and how it is locked. Whether this environment opens it is a
// line of its own, which a page words for what it has found out (lock).
type storeFacts struct {
	where  []view.Pair
	locked view.Pair
	// mode is how the store is locked, "" while there is no store, which where
	// says itself.
	mode keyMode
	// size and changed are the file's, zero while there is none, and readers is
	// how many keys a store locked to keys is locked to: the numbers behind
	// lines where and locked say as words.
	size    int64
	changed time.Time
	readers int
}

// lock is the lines about the lock — how it is, and what is said of opening it —
// and none while there is no store. The one place these lines are spelled, for
// the capabilities that carry them.
func (f storeFacts) lock(unlocks string) []view.Pair {
	if f.mode == "" {
		return nil
	}
	return []view.Pair{f.locked, {Key: "unlocks here", Value: unlocks}}
}

// whereOn is the lines about the file for a page, or for a tile (the TUI's
// compact answer): one line, how big the file is and when it changed, since the
// path fits no tile and the line is the one a glance reads it for. A store that
// is not there says so, and what makes one, in a few words.
func (f storeFacts) whereOn(tile bool) []view.Pair {
	if !tile {
		return f.where
	}
	if f.mode == "" {
		return []view.Pair{{Key: "state", Value: plugin.SurfaceTUI.CapabilityName("kv.set") + " creates one"}}
	}
	return []view.Pair{{Key: "store", Value: format.Bytes(f.size) + ", changed " + itemstore.Age(f.changed)}}
}

func readStoreFacts(req plugin.Request) (storeFacts, *view.Error) {
	path := storePath()
	where := []view.Pair{{Key: "store", Value: path}}
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return storeFacts{where: append(where, view.Pair{
			Key:   "state",
			Value: "no store yet — " + NoStoreNext(req.Surface(), "<key>"),
		})}, nil
	case err != nil:
		return storeFacts{}, view.Errorf("kv.store.unreadable", "reading %s: %v", path, err)
	}
	where = append(where,
		view.Pair{Key: "size", Value: format.Bytes(info.Size())},
		view.Pair{Key: "changed", Value: itemstore.Age(info.ModTime())},
	)

	mode, specs, verr := currentMode()
	if verr != nil {
		return storeFacts{}, verr
	}
	locked := view.Pair{Key: "locked with", Value: "a passphrase"}
	if mode == modeKeys {
		locked = view.Pair{Key: "locked to",
			Value: format.CountOf(len(specs), "key") + " — see " + req.Surface().CapabilityName("kv.recipients")}
	}
	facts := storeFacts{where: where, locked: locked, mode: mode, size: info.Size(), changed: info.ModTime()}
	if mode == modeKeys {
		facts.readers = len(specs)
	}
	return facts, nil
}

// unlockAvailability says whether a key is at hand, naming only where it came
// from — never any part of the key itself. Whether this shell can open the
// store is the question behind the question: a store you cannot unlock right now
// is the thing you want to find out before you need the secret, not while you
// need it.
//
// A key at hand is not a key that fits, which only opening the store can say.
func unlockAvailability(req plugin.Request, mode keyMode) string {
	return unlockLine(req, mode, "you will be asked")
}

// unlockLine is unlockAvailability for a page that says who asks for a key
// that is not at hand, in the words asks gives: a page that never asks cannot
// tell its reader that they will be.
func unlockLine(req plugin.Request, mode keyMode, asks string) string {
	if mode == modeKeys {
		p := identityPath(req)
		switch {
		case p == "" && req.Surface() == plugin.SurfaceMCP:
			return "no identity given (the operator sets " + identityEnv + ")"
		case p == "":
			return "no identity given (" + req.Surface().InputName("identity") + ", or set " + identityEnv + ")"
		case !identityReadable(p):
			// Before lockedKey, because lockedKey answers false for a file it
			// could not open and this line is the one that turns that into a
			// promise about the store.
			return "no — identity " + p + " cannot be read"
		case !lockedKey(p):
			return "yes — identity " + p
		case lookupPassphrase(req) != "" || keyPassphrases[p] != "":
			return "yes — identity " + p + ", unlocked"
		case canPrompt(req):
			// A locked key is not the same as no key, and the difference is a
			// question you can answer — which is worth distinguishing from the
			// case where nothing here can open the store at all.
			return "on request — identity " + p + " is passphrase-protected, " + asks
		}
		return "no — identity " + p + " is passphrase-protected (" + unlockWays(req) + ")"
	}
	if lookupPassphrase(req) != "" {
		return "yes — passphrase " + passphraseSource(req)
	}
	if canPrompt(req) {
		return "on request — " + asks
	}
	return "no passphrase available (" + unlockWays(req) + ")"
}

// unlockWays is how the store is opened from where the reader stands: in the TUI
// the person is at the keyboard and U asks for the passphrase; anywhere else it
// is the environment's to set.
func unlockWays(req plugin.Request) string {
	if req.Surface() == plugin.SurfaceTUI {
		return "press U to unlock it for this session, or set " + passphraseEnv + " before starting rta"
	}
	return "set " + passphraseEnv
}

func runRecipients(_ context.Context, req plugin.Request) (view.View, error) {
	// No key can read a store that does not exist or is locked with a
	// passphrase, so both answer with the recipients table and no rows, and
	// what a person is told in its place beside it (view.Table.Empty). Each
	// answered with its sentence as a Text view, which every format carried:
	// `jq '.rows[]'` met a view with no rows on exactly the stores with none.
	t := view.Table{Columns: []view.Column{{Name: "Type"}, {Name: "Recipient"}, {Name: "Comment"}}}
	// Before the recipients list, because an empty one means two different
	// things and only one of them was being said. With no store on disk this
	// announced "The store is encrypted with a passphrase, not keys." — a
	// flat claim about a file that does not exist — and then offered two
	// `kv rekey` lines, both of which rekey refuses with kv.rekey.nostore.
	// Three commands deep before anything said the store had never been
	// created, and the first two read like a working system.
	//
	// `kv status` and `kv rekey` both already knew this state by name; the
	// phrase is theirs. Every command offered here is one that works from
	// here, which is what the two it replaces were not — each spelled as the
	// surface asking makes the call.
	// A key somebody already holds is named by identity or recipient, both
	// Local: over MCP they are the operator's to give, so the agent reads
	// whose key it is and the command that person runs (operatorsCall).
	sf := req.Surface()
	const ownKey = "~/.ssh/id_ed25519"
	held, whose := "a key you already hold", "a key of your own"
	if sf == plugin.SurfaceMCP {
		held, whose = "a key the operator already holds", "a key the operator holds"
	}
	if !fileExists(storePath()) {
		t.Empty = "No store yet — nothing to read, and nothing locking it.\n\n" +
			"What it is locked with is decided when it is created:\n" + callList(
			[2]string{sf.Call("kv.init", plugin.Arg{Name: "generate", Value: true}), "a key made for this store"},
			[2]string{operatorsCall(sf, "kv.init", plugin.Arg{Name: "identity", Value: ownKey}), held},
			[2]string{setCall(sf), "a passphrase you choose, if you never run init"})
		return t, nil
	}
	specs, verr := loadRecipients()
	if verr != nil {
		return nil, verr
	}
	if len(specs) == 0 {
		t.Empty = "The store is encrypted with a passphrase, not keys.\n\n" +
			// The private key path, not its .pub: --recipient reads either,
			// but only the private file also proves you hold it — which is
			// what the switch below needs, and a public key alone cannot show.
			"To switch to " + whose + ":\n" +
			"  " + operatorsCall(sf, "kv.rekey", plugin.Arg{Name: "only", Value: true},
			plugin.Arg{Name: "recipient", Value: ownKey}) + "\n" +
			"or to one made for the job, which needs no passphrase at all — so the passphrase stops opening the store,\n" +
			"and anything running as you can read the key file and open it:\n" +
			"  " + sf.Call("kv.rekey", plugin.Arg{Name: "only", Value: true}, plugin.Arg{Name: "generate", Value: true})
		return t, nil
	}
	for _, spec := range specs {
		fields := strings.Fields(spec)
		switch {
		case strings.HasPrefix(spec, "age1"):
			t.Rows = append(t.Rows, []string{"age", spec, ""})
		case len(fields) >= 2:
			// An authorized-keys line: type, key, and an optional comment
			// that is usually the only human-readable part.
			t.Rows = append(t.Rows, []string{fields[0], truncate(fields[1], 24), strings.Join(fields[2:], " ")})
		default:
			t.Rows = append(t.Rows, []string{"?", truncate(spec, 24), ""})
		}
	}
	t.Total = len(t.Rows)
	return t, nil
}

// suggestKeys completes a key name from the store — but only when the store
// can be opened without asking anybody anything.
//
// This is the one completion that could otherwise do real harm: resolving a
// passphrase may prompt, and a prompt fired by the tab key would hang the
// shell mid-command-line on a question nobody expects. So availability is
// checked first, and a store that would need a prompt simply offers nothing.
//
// The surface is the backstop underneath that check: a completion request
// cannot prompt whatever it calls (plugin.SurfaceCompletion), so the worst a
// mistake here can cost is a missing suggestion.
//
// "Without asking anybody" includes a person who already answered. A
// completion request is stripped of every secret input, so the passphrase the
// TUI's form typed into the unlock (session.go) never reaches it, and after
// pressing U the key boxes offered nothing until the person set the variable
// the unlock exists to avoid. The session is looked at, not used (peekSession):
// the deadline is the person's, and a box being tabbed through must not be
// what keeps a store open. Only a completion looks — a request on any other
// surface names its passphrase itself or is refused, and an agent's must
// never be answered from what a person typed into the TUI.
func suggestKeys(_ context.Context, req plugin.Request) []string {
	mode, _, verr := currentMode()
	if verr != nil {
		return nil
	}
	req = withHeldPassphrase(req)
	switch mode {
	case modeKeys:
		if identityPath(req) == "" {
			return nil
		}
	default:
		if lookupPassphrase(req) == "" {
			return nil
		}
	}
	s, verr := load(req)
	if verr != nil {
		return nil
	}
	keys := make([]string, 0, len(s.Entries))
	for k, e := range s.Entries {
		// The description is exactly what a key name six months old lacks.
		note := e.Description
		if note == "" {
			note = e.Kind
		}
		keys = append(keys, k+"\t"+note)
	}
	sort.Strings(keys)
	return keys
}

// withHeldPassphrase is a completion request carrying the passphrase the person
// unlocked the store with, when it has none of its own and one is held. For a
// store locked to a passphrase-protected key the held one is the key's, which
// is what the key's unlock reads from the same input.
func withHeldPassphrase(req plugin.Request) plugin.Request {
	if req.Surface() != plugin.SurfaceCompletion || lookupPassphrase(req) != "" {
		return req
	}
	if held, ok := peekSession(); ok {
		return req.With(map[string]any{"passphrase": held})
	}
	return req
}

// setCall is the call that makes the first store a passphrase one. A person at a
// terminal is asked for the value, and a value typed after the key is in shell
// history, so the call they are shown does not carry one; an agent has no prompt
// and gives its value as an argument.
func setCall(sf plugin.Surface) string {
	args := []plugin.Arg{keyArg("<key>")}
	if sf == plugin.SurfaceMCP {
		args = append(args, plugin.Arg{Name: "value", Value: "<value>", Positional: true})
	}
	return sf.Call("kv.set", args...)
}

// suggestFolders completes a folder from the keys the store holds, under
// suggestKeys' rule: only when the store opens without asking anybody anything.
func suggestFolders(ctx context.Context, req plugin.Request) []string {
	seen := map[string]bool{}
	var folders []string
	for _, entry := range suggestKeys(ctx, req) {
		key, _, _ := strings.Cut(entry, "\t")
		for _, folder := range foldersOf(key) {
			if !seen[folder] {
				seen[folder] = true
				folders = append(folders, folder)
			}
		}
	}
	return folders
}

// foldersOf is the folders a key is in, outermost first: a/b/c is in a/ and
// a/b/. The convention kv.tree draws and a grant's scope follows — a folder is
// what names share, and is never an entry itself (checkKeyName).
func foldersOf(key string) []string {
	parts := strings.Split(key, "/")
	out := make([]string, 0, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		out = append(out, strings.Join(parts[:i], "/")+"/")
	}
	return out
}

// callList lays calls out one per line, each with what it is for aligned
// beside it.
func callList(calls ...[2]string) string {
	width := 0
	for _, c := range calls {
		width = max(width, len(c[0]))
	}
	lines := make([]string, len(calls))
	for i, c := range calls {
		lines[i] = fmt.Sprintf("  %-*s   %s", width, c[0], c[1])
	}
	return strings.Join(lines, "\n")
}

// emptyList says why the list is empty, which is a different sentence for an
// empty store and for a filter that matched nothing. Answering both with
// "no keys stored yet" sent people off to re-add a secret that was there all
// along, one kind of json away. sf is the surface asking, for the names it
// gives the capabilities that answer next.
func emptyList(sf plugin.Surface, stored int, folder, kind, match string) string {
	if stored == 0 {
		if !fileExists(storePath()) {
			return "No keys stored yet — " + NoStoreNext(sf, "<key>")
		}
		return "No keys stored yet — " + sf.CapabilityName("kv.set") + " adds one"
	}
	var narrowed []string
	if folder != "" {
		narrowed = append(narrowed, "in "+folder+"/")
	}
	if kind != "" {
		narrowed = append(narrowed, "of kind "+kind)
	}
	if match != "" {
		narrowed = append(narrowed, fmt.Sprintf("matching %q", match))
	}
	return fmt.Sprintf("No key %s. The store holds %s — %s shows every one.",
		strings.Join(narrowed, " "), format.CountOf(stored, "key"), sf.CapabilityName("kv.list"))
}
