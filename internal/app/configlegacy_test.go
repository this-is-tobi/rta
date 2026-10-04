package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// macOS machine returns a home whose old config directory is the one earlier
// builds used, and the directory rta reads now. RTA_CONFIG is left unset
// because it is what takes the old directory out of the question.
func macOSMachine(t *testing.T) (home, old, own string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("RTA_DATA_DIR", filepath.Join(home, "data"))
	t.Setenv("RTA_KV_IDENTITY", "")
	t.Setenv("RTA_KV_PASSPHRASE", "")
	old = filepath.Join(home, "Library", "Application Support", "rta")
	was := legacyConfigDir
	t.Cleanup(func() { legacyConfigDir = was })
	legacyConfigDir = func() string {
		if _, err := os.Stat(old); err != nil {
			return ""
		}
		return old
	}
	return home, old, filepath.Join(home, ".config", "rta")
}

func put(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// **A config that stops applying after an upgrade must not be silent.** The
// directory moved, nothing reads the old one, and every command still
// succeeds — so the file's owner finds out from behaviour that quietly differs
// from what they wrote. The row names what is there, where rta looks now, and
// the command that moves it; it is a diagnostic, and nothing is moved for them.
func TestDoctorNamesAConfigLeftAtTheOldMacOSLocation(t *testing.T) {
	_, old, own := macOSMachine(t)
	put(t, old, "config.yaml", "kv.identity")

	row := report(t)["old config"]
	if row[0] != "warn" {
		t.Fatalf("old config status = %q, want warn: %q", row[0], row[1])
	}
	for _, want := range []string{old, own, "config.yaml, kv.identity", "mv -n", "mkdir -p -m 700"} {
		if !strings.Contains(row[1], want) {
			t.Errorf("the row does not say %q: %s", want, row[1])
		}
	}
	// The command is pasteable: the old path has a space, so it is quoted whole.
	if !strings.Contains(row[1], "'"+old+"'/*") {
		t.Errorf("the old directory is not quoted as one word: %s", row[1])
	}
}

// Nothing to say when nothing is left, and no row either for a directory that
// holds nothing rta keeps in it.
func TestDoctorSaysNothingOfAnOldDirectoryThatHoldsNoneOfRtasFiles(t *testing.T) {
	_, old, _ := macOSMachine(t)
	if _, ok := report(t)["old config"]; ok {
		t.Error("a row for an old directory that is not there")
	}
	put(t, old, "something-else.txt")
	if _, ok := report(t)["old config"]; ok {
		t.Error("a row for an old directory that holds nothing of rta's")
	}
}

// Moving would replace the new directory's file with an older one, so the row
// says it is in both and leaves the choice to the person who knows which is
// right, rather than handing over a command that decides for them.
func TestDoctorDoesNotOfferToOverwriteAFileTheNewDirectoryHolds(t *testing.T) {
	_, old, own := macOSMachine(t)
	put(t, old, "config.yaml", "policy.yaml")
	put(t, own, "config.yaml")

	row := report(t)["old config"]
	if row[0] != "warn" || !strings.Contains(row[1], "config.yaml is in both") {
		t.Fatalf("the row does not name the clash: %v", row)
	}
	if strings.Contains(row[1], "mv ") {
		t.Errorf("the row hands over a move across a clash: %s", row[1])
	}
}
