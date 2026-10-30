package pluginhost

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// landlockABI is the Landlock ABI this kernel speaks, 0 for none: a kernel
// built without it, or one that has it off among its security modules.
func landlockABI() int {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(abi)
}

// The rights a rule grants, by what the policy says of a path.
const (
	fsRead    = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR
	fsExecute = unix.LANDLOCK_ACCESS_FS_EXECUTE
	fsWrite   = unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK | unix.LANDLOCK_ACCESS_FS_MAKE_SYM | unix.LANDLOCK_ACCESS_FS_REFER |
		unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	// fsFile is the rights a rule on a file, not a directory, can carry: the
	// kernel refuses a rule on a file that names a directory's.
	fsFile = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
)

// handledFor is every filesystem right the ABI knows, which is what the
// ruleset handles: a right it handles is refused wherever no rule grants it,
// and one it does not know is a right a newer kernel would refuse and this one
// cannot be asked to.
func handledFor(abi int) uint64 {
	h := uint64(fsRead | fsExecute | fsWrite)
	if abi < 2 {
		h &^= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi < 3 {
		h &^= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi < 5 {
		h &^= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return h
}

// ConfineMain is `rta __confine --rx|--ro|--rw <path>... -- <plugin> <args>`:
// restrict this thread to the paths named, then become the plugin. It never
// returns. Called first thing in main, before anything has touched the
// descriptors the plugin is handed, and never from anywhere else.
//
// **Every failure ends here, and nothing is run.** The host asked for strict
// confinement, so a plugin that would start without it is a plugin that must
// not start: the reason goes to stderr, where the host reads a plugin's last
// words, and the exit is non-zero.
func ConfineMain(args []string) {
	if err := confine(args); err != nil {
		fmt.Fprintln(os.Stderr, "rta: the plugin was not started, because it could not be confined:", err)
		os.Exit(70)
	}
}

func confine(args []string) error {
	// One thread holds the domain, and it is the one that execs: the other
	// threads the runtime started end with execve, so the plugin is this
	// thread alone.
	runtime.LockOSThread()
	type rule struct {
		path   string
		access uint64
	}
	var rules []rule
	for len(args) > 0 && args[0] != "--" {
		if len(args) < 2 {
			return fmt.Errorf("%s names no path", args[0])
		}
		var access uint64
		switch args[0] {
		case "--rx":
			access = fsRead | fsExecute
		case "--ro":
			access = fsRead
		case "--rw":
			access = fsRead | fsWrite
		default:
			return fmt.Errorf("%q is not a policy flag", args[0])
		}
		rules = append(rules, rule{args[1], access})
		args = args[2:]
	}
	if len(args) < 2 {
		return errors.New("no command after --")
	}
	command := args[1:]
	abi := landlockABI()
	if abi < 1 {
		return errNoLandlock
	}
	handled := handledFor(abi)
	attr := unix.LandlockRulesetAttr{Handled_access_fs: handled}
	// The kernel takes the attribute by address and copies it during the call;
	// x/sys has no wrapper for these three syscalls, so the pointer is the ABI.
	ruleset, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0) //nolint:gosec // the syscall's ABI, see above
	if errno != 0 {
		return fmt.Errorf("creating the ruleset: %w", errno)
	}
	for _, r := range rules {
		if err := addRule(int(ruleset), r.path, r.access&handled); err != nil {
			return err
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("setting no_new_privs: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, ruleset, 0, 0); errno != 0 {
		return fmt.Errorf("restricting itself: %w", errno)
	}
	_ = unix.Close(int(ruleset))
	return syscall.Exec(command[0], command, os.Environ()) //nolint:gosec // the host's own command line: the plugin it hashed and trusted
}

// addRule grants access beneath path, or on path alone when it is a file. A
// path that is gone since the host listed it grants nothing, and is no error.
func addRule(ruleset int, path string, access uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("opening %s for its rule: %w", path, err)
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("reading %s for its rule: %w", path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		access &= fsFile
	}
	beneath := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)} //nolint:gosec // a descriptor fits
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH,
		uintptr(unsafe.Pointer(&beneath)), 0, 0, 0); errno != 0 { //nolint:gosec // the syscall's ABI, as confine says
		return fmt.Errorf("allowing %s: %w", path, errno)
	}
	return nil
}
