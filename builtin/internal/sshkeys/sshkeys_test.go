package sshkeys

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// write drops one file into dir and gives back its path.
func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The five preambles OpenSSH writes or reads, plus the things that live
// beside them in the same directory and must not be mistaken for one.
//
// Matching the shape — BEGIN … PRIVATE KEY — rather than enumerating the
// words is what covers a format nobody here thought of, and it still cannot
// match a public key, a known_hosts, an ssh config or a certificate, because
// none of those say PRIVATE KEY.
func TestWhatCountsAsAPrivateKey(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"openssh", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blb\n", true},
		{"legacy-rsa", "-----BEGIN RSA PRIVATE KEY-----\nMIIE\n", true},
		{"legacy-ec", "-----BEGIN EC PRIVATE KEY-----\nMHcC\n", true},
		{"pkcs8", "-----BEGIN PRIVATE KEY-----\nMC4C\n", true},
		{"pkcs8-encrypted", "-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIH\n", true},
		{"leading-blank-line", "\n-----BEGIN OPENSSH PRIVATE KEY-----\n", false},
		{"crlf", "-----BEGIN OPENSSH PRIVATE KEY-----\r\nb3Blb\r\n", true},

		{"public-key", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 me@host\n", false},
		{"certificate", "-----BEGIN CERTIFICATE-----\nMIIB\n", false},
		{"public-pem", "-----BEGIN PUBLIC KEY-----\nMCow\n", false},
		{"ssh-config", "Host example\n  User me\n", false},
		{"known-hosts", "example.com ssh-ed25519 AAAA\n", false},
		{"empty", "", false},
		{"prose", "this is not an SSH key\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPrivateKey(write(t, dir, tc.name, tc.body)); got != tc.want {
				t.Errorf("IsPrivateKey(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// A leading blank line is deliberately not accepted, and it is worth being
// explicit about which side of the line that falls on: the preamble has to be
// the first thing in the file, because that is what every tool that reads
// these files requires. Accepting a shifted one would list a file ssh itself
// would reject.
func TestAKeyIsRecognisedOnlyOnItsFirstLine(t *testing.T) {
	dir := t.TempDir()
	shifted := write(t, dir, "shifted", "# a note\n-----BEGIN OPENSSH PRIVATE KEY-----\n")
	if IsPrivateKey(shifted) {
		t.Error("a preamble on the second line counted as a key")
	}
}

// The directory walk: what it keeps, what it leaves, and that it does not
// stop at the first thing it cannot read.
func TestPrivateKeysWalksADirectory(t *testing.T) {
	dir := t.TempDir()
	a := write(t, dir, "work_ed25519", "-----BEGIN OPENSSH PRIVATE KEY-----\n")
	b := write(t, dir, "id_rsa", "-----BEGIN RSA PRIVATE KEY-----\n")
	write(t, dir, "id_rsa.pub", "ssh-rsa AAAA me@host\n")
	write(t, dir, "config", "Host x\n")
	write(t, dir, "id_scratch", "notes to self\n")
	if err := os.MkdirAll(filepath.Join(dir, "id_directory"), 0o700); err != nil {
		t.Fatal(err)
	}

	got := PrivateKeys(dir)
	want := []string{b, a} // sorted: id_rsa before work_ed25519
	if len(got) != len(want) {
		t.Fatalf("PrivateKeys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("PrivateKeys[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// **A private key written into a .pub file is still a private key**, and it
// is the worst thing in this directory: .pub is the one name here that is
// world-readable by convention, so key material under it is exposed by the
// convention itself. An earlier version of PrivateKeys skipped .pub as a free
// optimisation and would have hidden exactly this — found by probing, not by
// reading.
func TestAPrivateKeyInAPubFileIsFound(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "id_ed25519.pub", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3Blb\n")
	got := PrivateKeys(dir)
	if len(got) != 1 || got[0] != path {
		t.Errorf("PrivateKeys = %v, want %q — key material under a .pub name is still key material", got, path)
	}
}

// A symlinked key is a real key: a dotfiles repository pointing ~/.ssh at its
// own copy is an ordinary setup, and the DirEntry's own type would call it a
// symlink and drop it.
func TestASymlinkedKeyIsAKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege here that the test does not have")
	}
	store := t.TempDir()
	real := write(t, store, "real_key", "-----BEGIN OPENSSH PRIVATE KEY-----\n")

	dir := t.TempDir()
	link := filepath.Join(dir, "id_ed25519")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got := PrivateKeys(dir)
	if len(got) != 1 || got[0] != link {
		t.Errorf("PrivateKeys = %v, want the symlink at %q", got, link)
	}
}

// A key this process cannot open is left out rather than listed: the row would
// promise something the next step cannot do.
func TestAnUnreadableKeyIsNotOffered(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("mode bits do not stop this reader")
	}
	dir := t.TempDir()
	path := write(t, dir, "id_locked", "-----BEGIN OPENSSH PRIVATE KEY-----\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	if got := PrivateKeys(dir); len(got) != 0 {
		t.Errorf("PrivateKeys = %v, want nothing", got)
	}
}

// Public keys are parsed rather than pattern-matched, because a public key is
// one unframed line and there is no shape to test.
func TestPublicKeysParsesRatherThanGuesses(t *testing.T) {
	dir := t.TempDir()
	good := write(t, dir, "id_ed25519.pub",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ7DPvBl4xkPVQzMU8fEAcqHLGL8pJnCK2bFrSBqOl3o me@host\n")
	write(t, dir, "authorized_keys.txt", "# a comment\n")
	write(t, dir, "id_ed25519", "-----BEGIN OPENSSH PRIVATE KEY-----\n")

	got := PublicKeys(dir)
	if len(got) != 1 || got[0] != good {
		t.Errorf("PublicKeys = %v, want just %q", got, good)
	}
}

// A directory that is not there is no keys, not a crash: every caller here is
// a listing or a completion, and neither has anywhere to put an error.
func TestAMissingDirectoryIsNoKeys(t *testing.T) {
	if got := PrivateKeys(filepath.Join(t.TempDir(), "nope")); got != nil {
		t.Errorf("PrivateKeys = %v, want nil", got)
	}
	if got := PublicKeys(""); got != nil {
		t.Errorf("PublicKeys = %v, want nil", got)
	}
}
