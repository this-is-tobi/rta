package paths

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// noHome is a machine with no home in its environment and none in its account
// database, whose temporary directory is a fresh one, and whose notice the
// test can read.
func noHome(t *testing.T) *bytes.Buffer {
	t.Helper()
	clear(t)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	savedHome, savedTo := passwdHome, noticeTo
	passwdHome = func() string { return "" }
	strandedNotice = sync.Once{}
	var notice bytes.Buffer
	noticeTo = &notice
	t.Cleanup(func() {
		passwdHome, noticeTo = savedHome, savedTo
		strandedNotice = sync.Once{}
	})
	return &notice
}

// "." put the grant file, its seal key and the stores in whatever directory
// rta ran in — inside a project an agent was working on.
func TestNoHomeNeverMeansTheCurrentDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows resolves a home from USERPROFILE and the account database")
	}
	noHome(t)
	got := Data()
	if got == "." || !filepath.IsAbs(got) {
		t.Fatalf("Data() = %q with no home, want an absolute directory of its own", got)
	}
	if !strings.HasPrefix(filepath.Base(got), "rta-") {
		t.Errorf("Data() = %q, want a directory named for this account under the temporary one", got)
	}
}

// A HOME that is not set is not a machine with no home: the account database
// says where the account lives.
func TestAnUnsetHomeIsAskedOfTheAccountDatabase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows resolves a home from USERPROFILE")
	}
	clear(t)
	t.Setenv("HOME", "")
	saved := passwdHome
	passwdHome = func() string { return "/home/svc" }
	t.Cleanup(func() { passwdHome = saved })

	want := filepath.Join("/home/svc", ".local", "share", "rta")
	if got := Data(); got != want {
		t.Errorf("Data() = %q, want %q", got, want)
	}
}

// Said once and on the way in, ending in what to do: a record that vanishes at
// the next reboot is a surprise for whoever was not told.
func TestTheStrandedDirectoryIsSaidOnceAndSaysWhatToDo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows resolves a home from USERPROFILE and the account database")
	}
	notice := noHome(t)
	first := Data()
	Data()
	if n := strings.Count(notice.String(), "no home directory"); n != 1 {
		t.Fatalf("the notice was said %d times, want once: %q", n, notice.String())
	}
	for _, want := range []string{first, "HOME", "RTA_DATA_DIR"} {
		if !strings.Contains(notice.String(), want) {
			t.Errorf("the notice %q does not mention %q", notice.String(), want)
		}
	}
}

// The name is the uid and nothing else, in a directory every account can
// write to, so whoever creates it first decides who owns it.
func TestAStrandedDirectoryThatIsNotPrivateIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	noHome(t)
	dir := Data()
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureData(); err == nil {
		t.Errorf("EnsureData used %s, which lets every account in", dir)
	}
}

func TestAStrandedDirectoryThatIsALinkIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges there")
	}
	noHome(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, Data()); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureData(); err == nil {
		t.Error("EnsureData followed a link planted where the stranded directory goes")
	}
}

func TestAStrandedDirectoryIsCreatedPrivateAndReused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	noHome(t)
	dir, err := EnsureData()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("%s: %v, mode %v, want 0700", dir, err, info)
	}
	if again, err := EnsureData(); err != nil || again != dir {
		t.Errorf("a second EnsureData = %q, %v, want the same directory", again, err)
	}
}
