package kv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/itemstore"
	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
	// Matched against the description as well as the name, because the name
	// is the half you have forgotten: "which one was the deploy key for the
	// staging cluster" is answerable from what you wrote down at the time
	// and not from `prod-deploy-key`.
	match := strings.ToLower(strings.TrimSpace(req.String("match")))
	detail := req.Bool("detail")
	if req.Bool("removed") {
		return removedTable(s), nil
	}

	names := make([]string, 0, len(s.Entries))
	for k, e := range s.Entries {
		if kindFilter != "" && e.Kind != kindFilter {
			continue
		}
		if match != "" && !strings.Contains(strings.ToLower(k), match) &&
			!strings.Contains(strings.ToLower(e.Description), match) {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return view.Text{Body: emptyList(len(s.Entries), kindFilter, req.String("match"))}, nil
	}

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
	withUsers := false
	for _, k := range names {
		if len(usedBy[k]) > 0 {
			withUsers = true
			break
		}
	}

	cols := []view.Column{
		{Name: "Key"},
		{Name: "Kind"},
		{Name: "Size", Kind: view.KindBytes},
		{Name: "Description"},
		{Name: "Updated", Kind: view.KindDuration},
	}
	// Only when some listed entry has a user: a column of blanks would name
	// a concept this store does not otherwise have, on every listing, for
	// operators who never wrote a profile.
	if withUsers {
		cols = append(cols, view.Column{Name: "Used by"})
	}
	if detail {
		cols = append(cols, view.Column{Name: "Source"}, view.Column{Name: "Created", Kind: view.KindDuration})
	}
	t := view.Table{Columns: cols}
	for _, k := range names {
		e := s.Entries[k]
		row := []string{k, e.Kind, format.Bytes(uint64(len(e.Value))), e.Description, itemstore.Age(e.Updated)}
		if withUsers {
			row = append(row, strings.Join(usedBy[k], ", "))
		}
		if detail {
			row = append(row, e.origin(), itemstore.Age(e.Created))
		}
		t.Rows = append(t.Rows, row)
	}
	t.Total = len(t.Rows)
	return t, nil
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
		return nil, notFound(key)
	}
	pairs := []view.Pair{
		{Key: "key", Value: key},
		{Key: "kind", Value: e.Kind},
		{Key: "size", Value: format.Bytes(uint64(len(e.Value)))},
	}
	if e.Description != "" {
		pairs = append(pairs, view.Pair{Key: "description", Value: e.Description})
	}
	if o := e.origin(); o != "" {
		pairs = append(pairs, view.Pair{Key: "source", Value: o})
	}
	if n := len(e.Previous); n > 0 {
		pairs = append(pairs, view.Pair{Key: "history", Value: plural(n, "earlier value") + " — rta kv history " + key})
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
		// Both ways out, with the one that shows nothing first: the page
		// you are on exists because you did not want the value on screen,
		// and offering only `kv get` from it was an odd thing to end on.
		view.Pair{Key: "copy", Value: "rta kv copy " + key},
		view.Pair{Key: "reveal", Value: "rta kv get " + key},
	)
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
		return nil, notFound(key)
	}
	out := req.String("out")
	if out == "" {
		return view.Text{Body: string(e.Value)}, nil
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would write %q (%s) to %s", key, format.Bytes(uint64(len(e.Value))), out)}, nil
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
	if verr := writeOut(expandHome(out), e.Value); verr != nil {
		return nil, verr
	}
	return view.Text{Body: fmt.Sprintf("wrote %q to %s (%s, mode 0600)", key, out, format.Bytes(uint64(len(e.Value))))}, nil
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
			WithHint("use export (for eval) or dotenv (for a .env file)")
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
		if len(keys) == 0 {
			return view.Text{Body: "# no keys stored"}, nil
		}
	}

	prefix := req.String("prefix")
	var sb strings.Builder
	for _, k := range keys {
		e, ok := s.Entries[k]
		if !ok {
			return nil, notFound(k)
		}
		if syntax == "export" {
			sb.WriteString("export ")
		}
		sb.WriteString(envName(prefix, k))
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
func valueToStore(req plugin.Request) (value []byte, filename string, given bool, err error) {
	if path := req.String("file"); path != "" {
		data, err := os.ReadFile(expandHome(path))
		if err != nil {
			return nil, "", false, view.Errorf("kv.file.unreadable", "reading %s: %v", path, err)
		}
		// Exactly what was on disk. Trimming a trailing newline was a
		// convenience for text, and it cost every other file its last bytes —
		// a certificate's final DER byte is not whitespace to be tidied away.
		return data, filepath.Base(path), true, nil
	}
	raw := req.String("value")
	if raw == "" {
		return nil, "", false, nil
	}
	return []byte(raw), "", true, nil
}

// checkKeyName refuses the one key shape the folder convention cannot afford.
//
// A "/" in a key is a folder separator: `kv list --match prod/` browses one,
// and a grant scoped `prod/` covers the records under it and nothing else
// (internal/grant's CheckScope and coversFolder). That distinction only holds
// while `prod/` names a folder and never a record — a stored key ending in
// "/" would be both, and a grant naming it would be exact and prefix at once.
//
// Nothing else about a key is constrained. Keys are opaque strings and stay
// that way: a "." or ".." segment is legal here and is simply never swept into
// a folder grant, which is the grant matcher's job rather than this one's.
func checkKeyName(key string) *view.Error {
	if strings.HasSuffix(key, "/") {
		return view.Errorf("kv.set.foldername", "%q ends in a slash, so it names a folder rather than an entry", key).
			WithHint("drop the trailing slash — a folder is not stored, it is what the names " +
				"share, and `rta grant allow kv.get " + key + "` already covers everything under it")
	}
	return nil
}

// originOf records how this value reached the store.
//
// The surface is part of the answer and is the part nothing else preserves: a
// secret an agent wrote over MCP looks identical afterwards to one the
// operator typed, and "which of these did I not put here myself" is a
// reasonable question to be able to ask of your own store. --file is Local, so
// it cannot be the answer on the MCP surface and the two never contend.
func originOf(req plugin.Request, filename string) string {
	if filename != "" {
		// The basename only, matching Filename: which file it was is worth
		// recording and where it sat on disk is not, and the store should not
		// grow a copy of somebody's directory layout.
		return "file:" + filename
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
	key := strings.TrimSpace(req.String("key"))
	if key == "" {
		return map[string]any{}, nil
	}
	s, verr := load(req)
	if verr != nil {
		return map[string]any{}, nil
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
	key := strings.TrimSpace(req.String("key"))
	if key == "" {
		return nil, view.Errorf("kv.set.nokey", "key is empty")
	}
	if verr := checkKeyName(key); verr != nil {
		return nil, verr
	}
	if verr := refuseSilentIdentity(req); verr != nil {
		return nil, verr
	}
	value, filename, given, err := valueToStore(req)
	if err != nil {
		return nil, err
	}
	kind := strings.TrimSpace(req.String("kind"))
	if kind != "" && !contains(kinds, kind) {
		return nil, view.Errorf("kv.set.badkind", "unknown kind %q", kind).
			WithHint("use one of: " + strings.Join(kinds, ", "))
	}
	// A call that names neither a value nor anything to relabel is asking for
	// nothing at all, and saying so beats storing an empty secret.
	label := kind != "" || req.String("description") != ""
	if !given && !label {
		return nil, view.Errorf("kv.set.novalue", "no value given").
			WithHint("pass a value, or --file to read one from disk — or --description/--kind " +
				"to change what an existing entry is labelled without touching the secret")
	}
	if given && kind == "" {
		kind = detectKind(string(value), filename)
	}
	if req.DryRun && given {
		return view.Text{Body: fmt.Sprintf("would set %q (%s, %s)", key, kind, format.Bytes(uint64(len(value))))}, nil
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
	now := time.Now()
	previous, existed := s.Entries[key]

	var e entry
	if !given {
		if !existed {
			return nil, view.Errorf("kv.set.unknown", "%q is not in the store", key).
				WithHint("pass a value to create it — --description and --kind change what an " +
					"entry already holding a secret is labelled, and there is nothing to label yet")
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
			return view.Text{Body: fmt.Sprintf("would relabel %q (%s)", key, e.Kind)}, nil
		}
	} else {
		e = entry{
			Value: value, Kind: kind, Filename: filename, Origin: originOf(req, filename),
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
		msg = fmt.Sprintf("relabelled %q (%s) — the secret is unchanged", key, e.Kind)
	case existed:
		msg = fmt.Sprintf("updated %q (%s, %s)", key, kind, format.Bytes(uint64(len(value))))
	default:
		msg = fmt.Sprintf("set %q (%s, %s)", key, kind, format.Bytes(uint64(len(value))))
	}
	if specs := req.StringSlice("recipient"); len(specs) > 0 {
		msg += "\nstore re-encrypted — `rta kv recipients` lists who can read it"
	}
	return view.Text{Body: msg}, nil
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
	from := strings.TrimSpace(req.String("key"))
	to := strings.TrimSpace(req.String("new-name"))
	if from == "" || to == "" {
		return nil, view.Errorf("kv.rename.noname", "rename needs a key and a new name").
			WithHint("rta kv rename <key> <new-name>")
	}
	if from == to {
		return nil, view.Errorf("kv.rename.samename", "%q is already its name", from)
	}
	// The same guard set has: a rename is the other way to arrive at a name.
	if verr := checkKeyName(to); verr != nil {
		return nil, verr
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
		return nil, notFound(from)
	}
	// Refused, never confirmed. The overwrite would destroy the secret under
	// the target name with no history and no undo, which is exactly what
	// kv.rm is Destructive for — and a grant scoped to the key being renamed
	// says nothing at all about the one being clobbered.
	if _, taken := s.Entries[to]; taken {
		return nil, view.Errorf("kv.rename.taken", "%q already exists", to).
			WithHint("renaming onto it would destroy the secret it holds — remove that first: rta kv rm " + to)
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would rename %q to %q (%s, %s)",
			from, to, e.Kind, format.Bytes(uint64(len(e.Value))))}, nil
	}
	delete(s.Entries, from)
	s.Entries[to] = e
	if verr := save(req, s); verr != nil {
		return nil, verr
	}
	return view.Text{Body: fmt.Sprintf("renamed %q to %q — anything still asking for %q will not find it",
		from, to, from)}, nil
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
				return view.Text{Body: fmt.Sprintf("would purge the removed %q (%s)", key, r.Kind)}, nil
			}
			delete(s.Removed, key)
			if verr := save(req, s); verr != nil {
				return nil, verr
			}
			return view.Text{Body: fmt.Sprintf("purged %q — it was removed %s, and is gone now", key,
				itemstore.Age(r.RemovedAt))}, nil
		}
		return nil, notFound(key)
	}
	if req.DryRun {
		if purge {
			return view.Text{Body: fmt.Sprintf("would purge %q (%s) — no restore", key, e.Kind)}, nil
		}
		return view.Text{Body: fmt.Sprintf("would remove %q (%s) — restorable with `rta kv restore %s`", key, e.Kind, key)}, nil
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
		return view.Text{Body: fmt.Sprintf("purged %q — the value and its history are gone", key)}, nil
	}
	return view.Text{Body: fmt.Sprintf("removed %q — `rta kv restore %s` brings it back; `rta kv rm --purge %s` would not have",
		key, key, key)}, nil
}

// runInit chooses how the store is locked, once.
//
// It refuses to touch a store that already exists. Re-keying an existing
// store is a different operation — it has to decrypt everything first, which
// means proving you can — and `kv rekey` is that operation. Silently
// re-initialising would produce a recipients file describing a store none of
// those recipients can open.
func runInit(_ context.Context, req plugin.Request) (view.View, error) {
	if fileExists(storePath()) {
		return nil, view.Errorf("kv.init.exists", "a store already exists at %s", storePath()).
			WithHint("to change the lock on it: rta kv rekey --generate (add a key) or --generate --only (switch to it)")
	}
	if specs, verr := loadRecipients(); verr == nil && len(specs) > 0 {
		return nil, view.Errorf("kv.init.exists", "this store is already set up for keys").
			WithHint("`rta kv recipients` lists them; delete " + recipientsPath() + " to start over")
	}

	generate := req.Bool("generate")
	identity := strings.TrimSpace(req.String("identity"))
	if !generate && identity == "" && os.Getenv(identityEnv) == "" {
		return nil, view.Errorf("kv.init.nokey", "name a key, or generate one").
			WithHint("rta kv init --generate   (or --identity ~/.ssh/id_ed25519)")
	}

	var generated string
	if generate {
		if identity != "" {
			return nil, view.Errorf("kv.init.bothkeys", "--generate makes a key; --identity names one").
				WithHint("pick one")
		}
		generated = defaultIdentity()
		if req.DryRun {
			return view.Text{Body: "would generate a key at " + generated + " and lock the store to it"}, nil
		}
		if _, verr := generateIdentity(generated); verr != nil {
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
	body := "the store is locked to keys — `rta kv recipients` lists them\n"
	switch {
	case generated != "":
		body += "\ngenerated a key at " + generated + " (mode 0600)\n" +
			"back it up: losing it loses every secret in the store, and nobody can help you\n" +
			"it is found automatically, so `rta kv set`/`get` need no flags"
	default:
		body += "\nusing " + identityPath(req) + "\n" +
			"set " + identityEnv + " to that path to skip --identity from now on"
	}
	return view.Text{Body: body}, nil
}

// runRekey changes the lock on a store that already exists.
//
// It is the operation `kv init` cannot be: init decides the lock when there is
// nothing to lose, and this one re-encrypts secrets that are already there.
// The two irreversible halves — reading it, and keeping a key you hold — are
// checked before anything is written, in that order.
func runRekey(_ context.Context, req plugin.Request) (view.View, error) {
	if !fileExists(storePath()) {
		return nil, view.Errorf("kv.rekey.nostore", "no store yet — nothing to re-key").
			WithHint("`rta kv init --generate` sets one up")
	}
	generate := req.Bool("generate")
	adding := req.StringSlice("recipient")
	only := req.Bool("only")
	if !generate && len(adding) == 0 {
		return nil, view.Errorf("kv.rekey.nokey", "name a key, or generate one").
			WithHint("rta kv rekey --generate (a key made for this store), " +
				// The private key file, not its .pub: naming a public key
				// alone proves nothing about holding it, and the lockout
				// guard below needs proof.
				"or --recipient ~/.ssh/id_ed25519 (one you already have)")
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
	if !only && s.Recipients != nil && !equal(stored, s.Recipients) {
		return nil, view.Errorf("kv.recipients.mismatch",
			"kv.recipients does not match who the store is actually encrypted to, "+
				"so it cannot be the set this re-key builds on").
			WithHint("something other than `kv rekey` edited it — compare `rta kv recipients` " +
				"against what you expect, then name the set you want outright: " +
				"`rta kv rekey --only --recipient <each key that should read it>`")
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
				WithHint("--recipient takes an age recipient, an SSH public key, or a path to either — " +
					"including the private key itself, whose public half is all that is read")
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
			WithHint("add --generate, or name the private half of a key you have: --identity ~/.ssh/id_ed25519")
	}
	if req.DryRun {
		return view.Text{Body: rekeyPreview(generate, only, want, stored)}, nil
	}

	var generated string
	if generate {
		generated = defaultIdentity()
		spec, verr := generateIdentity(generated)
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
	if verr := saveTo(s, recipients, want); verr != nil {
		return nil, verr
	}
	return view.Text{Body: rekeySummary(generated, want, stored)}, nil
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
	body := "would re-encrypt the store to " + plural(len(want)+boolToInt(generate), "key")
	if generate {
		body += "\ngenerating a key at " + defaultIdentity()
	}
	if only {
		if gone := dropped(want, stored); len(gone) > 0 && !generate {
			body += "\ndropping " + plural(len(gone), "reader") + " — they could no longer open it"
		} else if len(stored) > 0 && generate {
			body += "\ndropping " + plural(len(stored), "reader") + " — only the new key would open it"
		}
	}
	return body
}

func rekeySummary(generated string, want, stored []string) string {
	body := plural(len(want), "key") + " can open the store — `rta kv recipients` lists them"
	if generated != "" {
		body += "\n\ngenerated a key at " + generated + " (mode 0600)\n" +
			"back it up: losing it loses every secret it is the only key to\n" +
			"it is found automatically, so nothing needs a flag"
	}
	if gone := dropped(want, stored); len(gone) > 0 {
		body += "\n\ndropped " + plural(len(gone), "reader") + ": they cannot open the store from now on.\n" +
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
// construction — so this is the one kv capability that always works, and the
// one worth putting on a dashboard.
func runStatus(ctx context.Context, req plugin.Request) (view.View, error) {
	path := storePath()
	pairs := []view.Pair{{Key: "store", Value: path}}
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return view.KeyValue{Pairs: append(pairs, view.Pair{
			Key:   "state",
			Value: "no store yet — created by the first `rta kv set <key> <value>`",
		})}, nil
	case err != nil:
		return nil, view.Errorf("kv.store.unreadable", "reading %s: %v", path, err)
	}
	pairs = append(pairs,
		view.Pair{Key: "size", Value: format.Bytes(uint64(info.Size()))},
		view.Pair{Key: "changed", Value: itemstore.Age(info.ModTime())},
	)

	mode, specs, verr := currentMode()
	if verr != nil {
		return nil, verr
	}
	if mode == modeKeys {
		pairs = append(pairs, view.Pair{Key: "locked to",
			Value: plural(len(specs), "key") + " — see `rta kv recipients`"})
	} else {
		pairs = append(pairs, view.Pair{Key: "locked with", Value: "a passphrase"})
	}
	// Whether this shell can open it is the question behind the question: a
	// store you cannot unlock right now is the thing you want to find out
	// before you need the secret, not while you need it.
	pairs = append(pairs, view.Pair{Key: "unlocks here", Value: unlockAvailability(req, mode)})
	summary := view.KeyValue{Pairs: pairs}
	if !req.Bool("detail") {
		return summary, nil
	}
	return detailedStatus(ctx, req, summary, mode), nil
}

// detailedStatus is the full-page answer to "what is in this store and who
// can open it", composed from the capabilities that already answer each half
// — kv.recipients and kv.list — rather than reaching into the store again.
//
// kv.list is safe to embed precisely because of what it does not return: key
// names, kinds, sizes, descriptions and ages, never a value nor a preview of
// one. The inventory a person needs at a glance is exactly the part that is
// not the secret.
//
// The store still has to open for that section, which is the one thing the
// compact view promises never to need. So it is attempted with whatever key
// the caller already supplied and no more: an --identity, a passphrase in
// the environment, an unlocked key. Nothing here prompts, and a store that
// will not open without asking says so and stops, because a status page that
// blocks on a passphrase is not a status page.
func detailedStatus(ctx context.Context, req plugin.Request, summary view.KeyValue, mode keyMode) view.View {
	p := plugin.NewPage(ctx, req)
	p.PutAs("store", "store", summary)
	if mode == modeKeys {
		p.AddAs("recipients", "recipients", runRecipients, plugin.Read, nil)
	}
	v, err := p.Run(runList, plugin.Read, nil)
	if err != nil {
		ve := view.AsError(err, "kv.status.locked")
		p.PutAs("keys", "keys", view.Text{Body: "Locked — the inventory needs the store open, and nothing here " +
			"can open it without asking.\n\n" + ve.Message + "\n\nRun `rta kv list` once a key is at hand."})
		return p.View()
	}
	p.PutAs("keys", "keys", v)
	return p.View()
}

// unlockAvailability says whether a key is at hand, naming only where it came
// from — never any part of the key itself.
func unlockAvailability(req plugin.Request, mode keyMode) string {
	if mode == modeKeys {
		p := identityPath(req)
		switch {
		case p == "":
			return "no identity given (--identity, or set " + identityEnv + ")"
		case !lockedKey(p):
			return "yes — identity " + p
		case lookupPassphrase(req) != "" || keyPassphrases[p] != "":
			return "yes — identity " + p + ", unlocked"
		case canPrompt(req):
			// A locked key is not the same as no key, and the difference is a
			// question you can answer — which is worth distinguishing from the
			// case where nothing here can open the store at all.
			return "on request — identity " + p + " is passphrase-protected, you will be asked"
		}
		return "no — identity " + p + " is passphrase-protected (set " + passphraseEnv + ")"
	}
	if lookupPassphrase(req) != "" {
		return "yes — passphrase from the environment"
	}
	if canPrompt(req) {
		return "on request — you will be asked for the passphrase"
	}
	return "no passphrase available (set " + passphraseEnv + ")"
}

func runRecipients(_ context.Context, _ plugin.Request) (view.View, error) {
	specs, verr := loadRecipients()
	if verr != nil {
		return nil, verr
	}
	if len(specs) == 0 {
		return view.Text{Body: "The store is encrypted with a passphrase, not keys.\n\n" +
			// The private key path, not its .pub: --recipient reads either,
			// but only the private file also proves you hold it — which is
			// what the switch below needs, and a public key alone cannot show.
			"To switch to a key of your own:\n" +
			"  rta kv rekey --only --recipient ~/.ssh/id_ed25519\n" +
			"or to one made for the job, which needs no passphrase at all:\n" +
			"  rta kv rekey --only --generate"}, nil
	}
	t := view.Table{Columns: []view.Column{{Name: "Type"}, {Name: "Recipient"}, {Name: "Comment"}}}
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
func suggestKeys(_ context.Context, req plugin.Request) []string {
	mode, _, verr := currentMode()
	if verr != nil {
		return nil
	}
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

// emptyList says why the list is empty, which is a different sentence for an
// empty store and for a filter that matched nothing. Answering both with
// "no keys stored yet" sent people off to re-add a secret that was there all
// along, one `--kind json` away.
func emptyList(stored int, kind, match string) string {
	if stored == 0 {
		return "No keys stored yet — add one with: rta kv set <key> <value>"
	}
	var narrowed []string
	if kind != "" {
		narrowed = append(narrowed, "of kind "+kind)
	}
	if match != "" {
		narrowed = append(narrowed, fmt.Sprintf("matching %q", match))
	}
	return fmt.Sprintf("No key %s. The store holds %s — `rta kv list` shows every one.",
		strings.Join(narrowed, " "), plural(stored, "key"))
}

// plural counts a noun, matching builtin/audit's helper of the same name.
// "locked to 2 key(s)" is the shape of message that gets written once and
// read every day, and a store whose status line cannot count is not the
// thing to look careless about.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if len(noun) > 1 && strings.HasSuffix(noun, "y") &&
		!strings.ContainsRune("aeiou", rune(noun[len(noun)-2])) {
		return fmt.Sprintf("%d %sies", n, noun[:len(noun)-1])
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
