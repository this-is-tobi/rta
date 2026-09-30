package net

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/itemstore"
	"github.com/this-is-tobi/rta/builtin/internal/pathin"
	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Editing root-owned network configuration.
//
// /etc/hosts and /etc/resolv.conf decide where every process on the machine
// sends its traffic. Getting them wrong does not produce an error — it
// produces a machine that quietly talks to the wrong server, which is the
// worst kind of failure to debug. So the rules here are strict:
//
//   - Everything not being changed survives byte for byte. Comments,
//     ordering and spacing in a hosts file are somebody's notes to
//     themselves; a tool that reformats them on the way past is a tool
//     people stop using.
//   - Every write is preceded by a backup and performed atomically, so an
//     interrupted write cannot leave the machine with half a resolver.
//   - A file the system generates is not ours to edit. Writing to one is
//     worse than refusing: the change appears to work and then vanishes at
//     the next reboot, DHCP lease, or network change.

// backupDir keeps copies out of /etc — a directory full of hosts.bak.3 is
// its own kind of mess, and these belong with rta's own state.
func backupDir() string { return filepath.Join(itemstore.DataDir(), "backups") }

// maxHostsBytes and maxResolvBytes are more than either file ever holds,
// with room to spare: the largest hosts files in use are ad-blocking lists of
// a few megabytes, and a resolv.conf is under a kilobyte, since the resolver
// reads three nameservers and a search list of 256 characters. A file past
// the cap is not the file the input names, and reading it whole was the
// server's memory spent on the caller's say-so (pathin).
const (
	maxHostsBytes  = 32 << 20
	maxResolvBytes = 1 << 20
)

// opening is req as the file a net capability works on is opened under,
// which is always the one its file input names or, when it names none, the
// system's own (hostsPath, resolverPath).
//
// A file the caller named is a caller's path, and under a root it opens from
// the root like any other (pathin). The system's file is not: no caller chose
// it, nothing a caller may write lies on the way to it, and reading it is
// what the capability is for. Opened under the call's bounds it was judged as
// a path the call had reached and refused as outside the roots, so over MCP
// net.hosts.list and net.resolver.list could not read /etc/hosts or
// /etc/resolv.conf at all, nor net.info list the hosts file. It opens by
// name, as it did before calls carried bounds, and as the resolver line of
// net.info reads it (pathin.ReadFile).
func opening(req plugin.Request) plugin.Request {
	if strings.TrimSpace(req.String("file")) != "" {
		return req
	}
	return req.WithBounds(plugin.Bounds{})
}

// readLines reads a configuration file as lines, keeping them exactly as
// written. req is the call asking, for pathin's line on what a path may name
// and where it opens from (opening), and max the file's cap.
func readLines(req plugin.Request, path string, max int) ([]string, *view.Error) {
	data, err := pathin.Read(opening(req), path, max)
	if err != nil {
		return nil, unreadable(path, err)
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// managedBy reports what generates this file and what to do about it, or
// zero values when nothing does.
//
// A symlink is the giveaway on modern Linux (systemd-resolved, resolvconf
// and NetworkManager all point /etc/resolv.conf somewhere under /run) and on
// macOS; generators that write a real file announce themselves in a header.
// The advice comes back with the diagnosis because the two differ: a symlink
// has a real file at the other end you could edit, a header does not. force
// is the override as the caller gives it (plugin.Surface.InputName), which
// the advice names.
//
// req is the call asking, for the file pathin opens to read the header, and
// the link it reads (opening). link is what the path the caller named held, when it was
// a symbolic link the surface resolved before the handler saw it
// (plugin.Request.Link) — path is then the file at its far end, which cannot
// say it was linked to — and "" to ask the filesystem about path itself,
// through pathin, which under a root never reads back a link put in the
// file's place since: what that one holds is a name the caller chose.
func managedBy(req plugin.Request, path, link, force string) (what, advice string) {
	req = opening(req)
	if link == "" {
		link, _ = pathin.Readlink(req, path)
	}
	if link != "" {
		return "a symlink to " + link,
			"edit " + link + " instead, or configure whatever writes it — " + force + " would " +
				"replace the symlink with a regular file, which usually breaks more than it fixes"
	}
	f, _, err := pathin.Open(req, path)
	if err != nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	lower := strings.ToLower(string(head[:n]))
	for _, m := range []struct{ marker, owner string }{
		{"systemd-resolved", "systemd-resolved"},
		{"networkmanager", "NetworkManager"},
		{"resolvconf", "resolvconf"},
		{"generated by", ""},
		{"do not edit", ""},
	} {
		if !strings.Contains(lower, m.marker) {
			continue
		}
		if m.owner == "" {
			return "another program", "configure that program instead, or give " + force + " if you are sure"
		}
		return m.owner, "configure " + m.owner + " instead, or give " + force + " if you are sure"
	}
	return "", ""
}

// backup copies path into rta's own state directory before it is changed,
// and returns where it went so the message can say. req is the call asking,
// for the file pathin opens (opening).
//
// Every backup gets its own file. Two edits in the same second are ordinary
// — `hosts add` then `hosts toggle` takes about that long — and a timestamp
// alone would have the second silently overwrite the first, leaving "saved
// to X" pointing at a copy of the very state it claimed to preserve.
//
// Owner-only, like everything else under the data directory, which
// paths.EnsureData creates when this is the first command to need it. It
// was made 0755 here, through MkdirAll's parent creation — the drift
// EnsureData exists to end — and a machine whose first rta command was a
// hosts edit then had `rta doctor` warning about a mode rta chose. A copy
// is 0600 whatever the original's mode: the hosts file is public, but the
// file input can name any file this process can read.
//
// max is the cap readLines held the same file to, and the copy is held to it
// too. The read comes first and the copy after, so the file can grow between
// them, and a copy with no bound was the one read of it left uncapped — on
// the CLI, where pathin opens whatever the path names, a copy of anything at
// all. A copy past it is refused as the read would be, and removed: half a
// file among the backups reads as a saved state that never existed.
func backup(req plugin.Request, path string, max int) (string, *view.Error) {
	src, _, err := pathin.Open(opening(req), path)
	if err != nil {
		return "", unreadable(path, err)
	}
	defer func() { _ = src.Close() }()
	dir := backupDir()
	if data, err := paths.EnsureData(); err != nil {
		return "", view.Errorf("net.sysfile.backup", "creating %s: %v", data, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", view.Errorf("net.sysfile.backup", "creating %s: %v", dir, err)
	}
	base := filepath.Join(dir, fmt.Sprintf("%s.%s", filepath.Base(path), time.Now().Format("20060102-150405")))
	for i := 0; ; i++ {
		dest := base
		if i > 0 {
			dest = fmt.Sprintf("%s-%d", base, i)
		}
		// O_EXCL: the check and the claim have to be one step, or two edits
		// racing land on the same name anyway.
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", view.Errorf("net.sysfile.backup", "writing %s: %v", dest, err)
		}
		n, werr := io.Copy(f, io.LimitReader(src, int64(max)+1))
		cerr := f.Close()
		if werr == nil && cerr == nil && n <= int64(max) {
			return dest, nil
		}
		_ = os.Remove(dest)
		if werr == nil && cerr == nil {
			return "", unreadable(path, &pathin.TooLargeError{Path: path, Max: max})
		}
		return "", view.Errorf("net.sysfile.backup", "writing %s: %v", dest, firstErr(werr, cerr))
	}
}

// unreadable is the refusal of a file readLines or backup could not read,
// saying which of pathin's refusals it met when it was one of them.
//
// The call's bounds' own refusal is the answer as it is — rta's own state
// under a root, by its name or by another name for one of its files — which
// says what the path is better than "reading it" could: wrapped as
// net.sysfile.unreadable, a file that may not be read was said to be one that
// could not.
func unreadable(path string, err error) *view.Error {
	var notAFile *pathin.NotAFileError
	var tooLarge *pathin.TooLargeError
	var refused *view.Error
	switch {
	case errors.As(err, &refused):
		return refused
	case errors.As(err, &notAFile):
		return view.Errorf("net.sysfile.notafile", "%v", err).
			WithHint("name the file itself — a hosts file or a resolv.conf is a regular file")
	case errors.As(err, &tooLarge):
		return view.Errorf("net.sysfile.toolarge", "%v, more than a hosts file or a resolv.conf holds", err).
			WithHint("name the hosts file or the resolv.conf itself")
	}
	return view.Errorf("net.sysfile.unreadable", "reading %s: %v", path, err)
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// writeLines replaces a configuration file atomically, keeping its mode.
// Atomically because a torn /etc/hosts is a machine that cannot resolve its
// own name; see internal/atomicfile for how. sf is the surface asking, and
// call the change the caller asked for, for the way a refusal to write says
// root is reached.
func writeLines(sf plugin.Surface, path string, lines []string, call rootCall) *view.Error {
	info, err := os.Stat(path)
	if err != nil {
		return view.Errorf("net.sysfile.unreadable", "reading %s: %v", path, err)
	}
	body := strings.Join(lines, "\n") + "\n"

	// Keep the original permissions rather than the 0600 a fresh file would
	// get: a 0600 /etc/hosts breaks name resolution for every non-root
	// process, which is a worse outage than the edit was a fix.
	if err := atomicfile.Write(path, []byte(body), info.Mode().Perm()); err != nil {
		return permissionError(sf, path, err, call)
	}
	return nil
}

// permissionError turns "permission denied" into the sentence that actually
// helps, since needing root here is the expected case and not a mistake.
//
// errors.Is rather than os.IsPermission: the write goes through atomicfile,
// which wraps, and os.IsPermission only unwraps the handful of error types
// the os package defines itself. It looked equivalent and would have quietly
// dropped the one hint the caller needs, on the one path they always take.
//
// Only a command line is run again with sudo. A TUI and an MCP server run as
// whoever started them, and "the same command" told an agent to rerun one it
// never typed: from either, the change is made at a terminal, so the command
// that makes it is named as a terminal spells it — to an agent through
// plugin.AskOperator, the one phrase that hands an agent a command, since
// the operator is who runs it.
func permissionError(sf plugin.Surface, path string, err error, call rootCall) *view.Error {
	if errors.Is(err, fs.ErrPermission) {
		hint := "run the same command with sudo — editing " + path + " needs root"
		switch sf {
		case plugin.SurfaceMCP:
			hint = "editing " + path + " needs root, which this server does not run as — " +
				plugin.AskOperator(strings.TrimPrefix(call.commandLine(), "rta ")) + " with sudo"
		case plugin.SurfaceTUI:
			hint = "editing " + path + " needs root, which the TUI does not run as — run `sudo " +
				call.commandLine() + "` at a terminal"
		}
		return view.Errorf("net.sysfile.permission", "cannot write %s: permission denied", path).WithHint(hint)
	}
	return view.Errorf("net.sysfile.write", "writing %s: %v", path, err)
}

// rootCall is the call that makes a change to a system file: the capability,
// and the arguments that say what the change is, for permissionError to name
// the command that makes it as root.
type rootCall struct {
	id   string
	args []plugin.Arg
}

// commandLine is the call as a terminal types it, whatever surface asked,
// since a terminal is where root is had.
func (c rootCall) commandLine() string { return plugin.SurfaceCLI.Call(c.id, c.args...) }

// callOf is the rootCall of capability id as req made it: each value of the
// positional inputs named, in their order, a StringSlice one word by word,
// then any switch turned on and the file given, if one was — a Local input,
// so never over MCP, where the change is to the system's own file.
func callOf(req plugin.Request, id string, positional []string, switches ...string) rootCall {
	c := rootCall{id: id}
	for _, name := range positional {
		for _, v := range req.StringSlice(name) {
			c.args = append(c.args, plugin.Arg{Name: name, Value: v, Positional: true})
		}
	}
	for _, name := range switches {
		if req.Bool(name) {
			c.args = append(c.args, plugin.Arg{Name: name, Value: true})
		}
	}
	if file := req.String("file"); file != "" {
		c.args = append(c.args, plugin.Arg{Name: "file", Value: file})
	}
	return c
}

// guardManaged refuses to edit a file something else generates, unless the
// caller says they know. Silently editing a generated file is worse than
// refusing: the change works, then disappears at the next reboot or lease
// renewal, and nothing points at why.
func guardManaged(req plugin.Request, path string, force bool) *view.Error {
	what, advice := managedBy(req, path, "", req.Surface().InputName("force"))
	if what == "" || force {
		return nil
	}
	return view.Errorf("net.sysfile.managed", "%s is %s, so changes here get overwritten", path, what).
		WithHint(advice)
}
