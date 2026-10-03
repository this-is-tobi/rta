package mcp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/paths"
)

// operatorPaths are the places on this machine rta keeps what is the
// operator's, with the words an agent is told them by: the data directory (the
// stores, the grants, the record), the config directory, and the file the
// store's identity is read from.
//
// **An error is written for the person at a terminal, and says where.** "reading
// /home/you/.local/share/rta/kv.age: permission denied" is the right sentence
// there, and over MCP it hands an agent the layout of the one place rta keeps
// what it must not be able to read or move: where the secrets store is, where
// the grants and the record are, and which file unlocks the store. The paths
// are in a good many errors, kv, note and net among them and some from the
// operating system itself, so they are put right once, here, where every error
// an agent is handed passes, and not in each place an error is made. The
// record keeps the error as it was: it is the operator's.
func operatorPaths() map[string]string {
	found := map[string]string{}
	add := func(path, label string) {
		if path != "" && path != "." && path != string(filepath.Separator) {
			found[filepath.Clean(path)] = label
		}
	}
	add(paths.Data(), "<data dir>")
	add(filepath.Dir(config.Path()), "<config dir>")
	add(os.Getenv("RTA_KV_IDENTITY"), "<identity file>")
	return found
}

// withoutOperatorPaths is s with those places named by what they are.
func withoutOperatorPaths(s string) string { return operatorNames()(s) }

// operatorNames is withoutOperatorPaths for many strings: the places are
// worked out once, for a result of thousands of cells.
func operatorNames() func(string) string {
	found := operatorPaths()
	order := make([]string, 0, len(found))
	for p := range found {
		order = append(order, p)
	}
	// The longest first: the data directory may sit inside the config
	// directory or the other way about, and the inner name has to win where
	// both would match.
	sort.Slice(order, func(i, j int) bool { return len(order[i]) > len(order[j]) })
	return func(s string) string {
		for _, p := range order {
			if strings.Contains(s, p) {
				s = replacePath(s, p, found[p])
			}
		}
		return s
	}
}

// replacePath replaces p in s where it is a path of its own: not the tail of a
// longer one (/srv/data inside /srv/data/x is a different place from /data) and
// not the head of a longer name (/data in /database). A data directory called
// /data is a place an operator may well choose, and a bare substring would
// rewrite every path that happens to contain it.
func replacePath(s, p, label string) string {
	var out strings.Builder
	for {
		i := strings.Index(s, p)
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		end := i + len(p)
		if startsAPath(s[:i]) && endsAPath(s[end:]) {
			out.WriteString(s[:i])
			out.WriteString(label)
		} else {
			out.WriteString(s[:end])
		}
		s = s[end:]
	}
}

// pathChar is what a name is made of, and what a path can continue with on its
// left: another directory above it.
func pathChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '-' || c == '.' || c == '/' || c == '\\' || c == '~' || c == '+' || c == '@'
}

// startsAPath is whether a path may begin right after before: nothing precedes
// it, or what does is not part of a longer path.
func startsAPath(before string) bool {
	return before == "" || !pathChar(before[len(before)-1])
}

// endsAPath is whether a path ends right before after: at the end, at a
// separator, or at punctuation that closes a sentence or a quote; not at more
// of a name.
func endsAPath(after string) bool {
	if after == "" {
		return true
	}
	switch c := after[0]; {
	case c == '/' || c == '\\':
		return true
	case c == '.':
		return len(after) == 1 || after[1] == ' ' || after[1] == '\n'
	case pathChar(c):
		return false
	}
	return true
}
