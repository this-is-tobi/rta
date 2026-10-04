package paths

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
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
	noHome(t)
	dir := whereStranded()
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureData(); err == nil {
		t.Errorf("EnsureData used %s, which lets every account in", dir)
	}
}

// whereStranded is the name the stranded directory has, which Data makes if it
// is not there, so a test that has to put something at the name asks for it
// without Data.
func whereStranded() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("rta-%d", os.Getuid()))
}

// The grant gate reads grants.json and the key that seals it from wherever
// Data says, and never goes through EnsureData: a directory another account
// made at the stranded name, holding a grant file and a key that agree, was
// read as this account's own grants. The mode is what stands in for the
// owner, which a test cannot be another account to set.
func TestAReaderIsNeverSentIntoAStrandedDirectoryThatIsNotPrivate(t *testing.T) {
	noHome(t)
	dir := whereStranded()
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	forged := filepath.Join(dir, "grants.json")
	if err := os.WriteFile(forged, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Data()
	if got == dir || strings.HasPrefix(got, dir+string(filepath.Separator)) {
		t.Fatalf("Data() = %q, a directory that lets every account in, so whoever made it wrote what is read there", got)
	}
	_, err := os.ReadFile(filepath.Join(got, "grants.json"))
	if err == nil {
		t.Fatalf("a read under Data() = %q found a file", got)
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a read under Data() = %q is %v: a reader takes that for no grants having been issued, "+
			"and not for state it cannot trust", got, err)
	}
}

func TestAReaderIsNeverSentThroughALinkPlantedAtTheStrandedName(t *testing.T) {
	noHome(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, whereStranded()); err != nil {
		t.Fatal(err)
	}
	if got := Data(); got == whereStranded() || strings.HasPrefix(got, elsewhere) {
		t.Errorf("Data() = %q followed a link planted where the stranded directory goes", got)
	}
}

// The directory a reader is sent to is one this account made, so there is no
// moment between looking at it and reading from it for another account to put
// its own there.
func TestDataMakesTheStrandedDirectoryPrivateBeforeAnyoneReadsFromIt(t *testing.T) {
	noHome(t)
	dir := Data()
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("%s: %v, %v, want a directory of mode 0700", dir, info, err)
	}
	if again := Data(); again != dir {
		t.Errorf("a second Data() = %q, want %q", again, dir)
	}
}

func TestAStrandedDirectoryThatIsALinkIsRefused(t *testing.T) {
	noHome(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, whereStranded()); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureData(); err == nil {
		t.Error("EnsureData followed a link planted where the stranded directory goes")
	}
}

func TestAStrandedDirectoryIsCreatedPrivateAndReused(t *testing.T) {
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
