package paths

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/this-is-tobi/rta/pkg/view"
)

// CodeUnwritable is the code of the error a directory rta cannot write is
// reported with, wherever it was found out.
const CodeUnwritable = "core.dir.unwritable"

// Unwritable is the error for a directory the system will not let rta write,
// or nil when err is anything else: a full disk and a file in the way are other
// problems with other fixes, and calling them this one sends a person to
// change an owner that was never the matter.
//
// **One error, from the place that finds out.** The data directory of a pod
// whose volume was never mounted, or is owned by another uid, failed in as many
// words as there were commands — `acquiring the notes.json lock: creating
// <dir>: mkdir <dir>: permission denied`, `acquiring the grant file lock`,
// `acquiring the config file lock` — the path twice, a permission problem
// called a lock, and nothing about whose directory it was or how to point rta
// somewhere it can write. This says the directory, whose it is, and the
// variable that moves it, and the first command to meet the problem says it
// the same as the rest.
func Unwritable(dir string, err error) *view.Error {
	var errno syscall.Errno
	if err == nil || !errors.As(err, &errno) {
		return nil
	}
	if !errors.Is(err, fs.ErrPermission) && errno != syscall.EROFS {
		return nil //nolint:nilerr // any other failure is not this one, and the caller keeps its own
	}
	kind := directoryKind(dir)
	verb := "write to"
	if _, statErr := os.Stat(dir); statErr != nil {
		verb = "create"
	}
	msg := "rta cannot " + verb + " " + kind.name(dir) + ": " + errno.Error()
	if errno != syscall.EROFS {
		msg += " — " + whoseIs(dir)
	}
	return view.Errorf(CodeUnwritable, "%s", msg).WithHint(kind.hint(dir, errno == syscall.EROFS))
}

// CheckWritable is Unwritable asked before anything is written: whether this
// account could create dir, or write in it if it is there. For a reader that
// must not write to find out, such as `rta doctor`.
//
// A directory that does not exist yet is asked of the nearest one that does,
// which is where it would be made.
func CheckWritable(dir string) *view.Error {
	probe := nearestExisting(dir)
	if probe == "" {
		return nil
	}
	if err := unix.Access(probe, unix.W_OK|unix.X_OK); err != nil {
		return Unwritable(dir, err)
	}
	return nil
}

// kind is what a directory is to rta, which decides what it is called and which
// variables move it.
type kind int

const (
	otherDir kind = iota
	dataDir
	configDir
)

func directoryKind(dir string) kind {
	dir = filepath.Clean(dir)
	switch {
	case within(dir, Data()):
		return dataDir
	case within(dir, filepath.Dir(ConfigFile())), within(dir, OwnConfigDir()):
		return configDir
	}
	return otherDir
}

func within(dir, root string) bool {
	if root == "" {
		return false
	}
	root = filepath.Clean(root)
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}

func (k kind) name(dir string) string {
	switch k {
	case dataDir:
		return "its data directory " + dir
	case configDir:
		return "its config directory " + dir
	}
	return dir
}

func (k kind) hint(dir string, readOnly bool) string {
	// What stands in the way, which is the directory itself only when it is there.
	target := nearestExisting(dir)
	if target == "" {
		target = dir
	}
	fix := "change who owns " + target
	if readOnly {
		fix = "mount " + target + " read-write"
	}
	switch k {
	case dataDir:
		return "point rta at a directory it can write with RTA_DATA_DIR (or XDG_DATA_HOME), or " + fix
	case configDir:
		return "point rta at a config file in a directory it can write with RTA_CONFIG (or XDG_CONFIG_HOME), or " + fix
	}
	return "make " + dir + " writable for " + currentUser() + ", or " + fix
}

// whoseIs says whose the directory is and what keeps the account out of it, or,
// when it is not there yet, the same of the nearest one that would hold it: the
// owner of what stands in the way.
func whoseIs(dir string) string {
	near := nearestExisting(dir)
	if near == "" {
		return dir + " cannot be looked at"
	}
	which := near + " "
	if near == dir {
		which = "it "
	}
	info, err := os.Stat(near)
	if err != nil {
		return which + "cannot be looked at"
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return which + "is not this account's to write"
	}
	if int(st.Uid) == os.Getuid() {
		return fmt.Sprintf("%sbelongs to %s, but its mode is %04o", which, currentUser(), info.Mode().Perm())
	}
	return which + "belongs to " + userName(strconv.Itoa(int(st.Uid))) + " and rta is running as " + currentUser()
}

// nearestExisting is dir if it exists and otherwise its closest parent that
// does, or "" when nothing on the way up can be looked at.
func nearestExisting(dir string) string {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}

func currentUser() string { return userName(strconv.Itoa(os.Getuid())) }

// userName is the account name for a uid, or the uid when the account database
// does not know it (a container run as an arbitrary uid), which is still what
// `ls -ln` shows beside the directory.
func userName(uid string) string {
	if u, err := user.LookupId(uid); err == nil && u.Username != "" {
		return u.Username
	}
	return "uid " + uid
}
