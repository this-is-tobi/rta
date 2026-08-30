// Package sshkeys answers "which files in this directory are SSH keys" by
// reading them rather than by reading their names.
//
// Four places asked that question and all four answered `id_*`: keys.list,
// keys.backup's completion, and kv's two identity pickers. It is the
// convention `ssh-keygen` follows when you let it choose, and it is not a
// rule — `ssh-keygen -f ~/.ssh/work_ed25519` is an ordinary thing to type, a
// dotfiles repository symlinks keys under whatever names it likes, and
// `IdentityFile` in an ssh config takes any path at all. So a key named
// anything else was invisible: not listed by the capability whose job is
// listing keys, and not offered by the completion on the capability whose job
// is backing one up. **A key you cannot see is a key you cannot back up**,
// which is the whole point of the plugin the blind spot lived in.
//
// The name test also admitted what it should have excluded. `id_rsa.old`,
// a stray `id_` scratch file, an `id_` directory — all counted, and turned up
// in the listing as `unknown`, which reads like a broken key rather than like
// a file that was never one.
//
// Shared rather than copied a fifth time, and the bar for that is the repo's
// own: builtin/keys says of an earlier duplication "two built-ins, ten lines,
// no third caller yet to justify the seam". There are four callers now, and
// the shared thing is no longer ten lines of path arithmetic — it is the
// judgement of what counts as a key, which has to be the same judgement
// everywhere or the pickers offer a different set than the listing shows.
package sshkeys

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
)

// headerBytes is how much of a file is read to decide. The longest PEM
// preamble this recognises is `-----BEGIN ENCRYPTED PRIVATE KEY-----` at 37
// bytes; the rest is slack so a leading blank line or a CRLF does not push
// the header out of the window. Deliberately not the whole file: this runs
// over every entry in ~/.ssh, on a keystroke in the completion case, and a
// private key's bytes are not something to pull into memory to answer a
// question about its first line.
const headerBytes = 128

// PrivateKeys lists the SSH private keys in dir, sorted, by absolute path.
//
// An unreadable file is not one — a key this process cannot open is a key it
// cannot back up either, so leaving it out of the list is the same answer the
// next step would give, arrived at without a row that promises something that
// will fail.
func PrivateKeys(dir string) []string {
	// No .pub exemption, and probing found the reason. Skipping them reads
	// like a free optimisation — a public key cannot carry a private
	// preamble — right up until somebody writes a private key into a .pub
	// file, which is a mistake people make and which leaves key material in
	// the one file in this directory that is world-readable by convention.
	// That is the worst thing this listing could find, and the shortcut was
	// the one rule that would hide it.
	return collect(dir, func(path, _ string) bool { return IsPrivateKey(path) })
}

// PublicKeys lists the SSH public keys in dir, sorted, by absolute path.
func PublicKeys(dir string) []string {
	return collect(dir, func(path, _ string) bool { return isPublicKey(path) })
}

// IsPrivateKey reports whether path holds an SSH private key, on its PEM
// preamble.
//
// Every format OpenSSH writes or reads is PEM-framed and says so on its first
// line: `-----BEGIN OPENSSH PRIVATE KEY-----` since 2018, the older
// `-----BEGIN RSA/EC/DSA PRIVATE KEY-----`, and PKCS#8's `-----BEGIN PRIVATE
// KEY-----` and `-----BEGIN ENCRYPTED PRIVATE KEY-----`. Matching the shape
// rather than enumerating the words covers the ones nobody thought of and
// still cannot match a public key, a known_hosts, an ssh config or a
// certificate — none of which say PRIVATE KEY.
//
// Not a full parse, deliberately. Parsing would mean holding key material in
// memory to answer a question about identity, and it would drop an encrypted
// key rta cannot open — which is exactly a key somebody wants listed.
func IsPrivateKey(path string) bool {
	line, ok := firstLine(path)
	if !ok {
		return false
	}
	return strings.HasPrefix(line, "-----BEGIN ") && strings.HasSuffix(line, "PRIVATE KEY-----")
}

// isPublicKey parses rather than pattern-matches: a public key is one line of
// plain text with no frame around it, so there is no cheap shape to test, and
// the parse costs nothing on a file this size.
func isPublicKey(path string) bool {
	line, ok := firstLine(path)
	if !ok {
		return false
	}
	_, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	return err == nil
}

// firstLine reads at most headerBytes and returns the first line, trimmed.
func firstLine(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close() //nolint:errcheck // read-only
	buf := make([]byte, headerBytes)
	n, err := io.ReadFull(f, buf)
	if n == 0 && err != nil {
		return "", false
	}
	text := string(buf[:n])
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text), true
}

// collect walks one directory, keeping the regular files want accepts.
//
// Symlinks are followed rather than skipped, because a dotfiles repository
// that symlinks ~/.ssh/id_ed25519 at its own copy is an ordinary setup and
// the key it points at is a real key. os.Stat follows; the DirEntry's own
// type does not.
func collect(dir string, want func(path, name string) bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if want(path, name) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}
