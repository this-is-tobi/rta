package paths

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

type ownedInfo struct {
	sys  any
	mode fs.FileMode
}

func (ownedInfo) Name() string        { return "roster" }
func (ownedInfo) Size() int64         { return 0 }
func (i ownedInfo) Mode() fs.FileMode { return i.mode }
func (ownedInfo) ModTime() time.Time  { return time.Time{} }
func (ownedInfo) IsDir() bool         { return false }
func (i ownedInfo) Sys() any          { return i.sys }

// A trust anchor is vouched for by its owner: this account or root, who could
// have written it anyway. Another account's file, whatever its mode, is one
// that account rewrites, and a FileInfo with no owner in it vouches for none.
func TestATrustAnchorIsOwnedByThisAccountOrRoot(t *testing.T) {
	for _, c := range []struct {
		label string
		sys   any
		want  bool
	}{
		{"this account", &syscall.Stat_t{Uid: uint32(os.Geteuid())}, true},
		{"root", &syscall.Stat_t{Uid: 0}, true},
		{"another account", &syscall.Stat_t{Uid: uint32(os.Geteuid()) + 4242}, false},
		{"no owner", nil, false},
	} {
		if _, ok := ownedByUsOrRoot(ownedInfo{sys: c.sys}); ok != c.want {
			t.Errorf("%s: ownedByUsOrRoot = %v, want %v", c.label, ok, c.want)
		}
	}
}

// Every bit but the owner's and group read refuses, as ErrWeakMode, which a
// chmod answers; group read loads and says so; an owner the check does not
// vouch for refuses whatever the mode, and not as ErrWeakMode, since a chmod
// would not answer it. Each refusal names the file and what it decides.
func TestATrustAnchorRefusesAWideModeOrAnotherOwner(t *testing.T) {
	us := &syscall.Stat_t{Uid: uint32(os.Geteuid())}
	for _, c := range []struct {
		label     string
		info      ownedInfo
		groupRead bool
		weak      bool
		refused   bool
	}{
		{"0600", ownedInfo{sys: us, mode: 0o600}, false, false, false},
		{"0400", ownedInfo{sys: us, mode: 0o400}, false, false, false},
		{"0640", ownedInfo{sys: us, mode: 0o640}, true, false, false},
		{"0620", ownedInfo{sys: us, mode: 0o620}, false, true, true},
		{"0610", ownedInfo{sys: us, mode: 0o610}, false, true, true},
		{"0604", ownedInfo{sys: us, mode: 0o604}, false, true, true},
		{"0602", ownedInfo{sys: us, mode: 0o602}, false, true, true},
		{"0601", ownedInfo{sys: us, mode: 0o601}, false, true, true},
		{"root's 0600", ownedInfo{sys: &syscall.Stat_t{Uid: 0}, mode: 0o600}, false, false, false},
		{"another account's 0600", ownedInfo{sys: &syscall.Stat_t{Uid: uint32(os.Geteuid()) + 4242}, mode: 0o600}, false, false, true},
	} {
		groupRead, err := CheckTrustAnchor("/etc/rta/operators", c.info, "it decides who may operate this server")
		if (err != nil) != c.refused {
			t.Errorf("%s: err = %v, want refused = %v", c.label, err, c.refused)
			continue
		}
		if groupRead != c.groupRead {
			t.Errorf("%s: groupReadable = %v, want %v", c.label, groupRead, c.groupRead)
		}
		if err == nil {
			continue
		}
		if errors.Is(err, ErrWeakMode) != c.weak {
			t.Errorf("%s: errors.Is(err, ErrWeakMode) = %v, want %v: %v", c.label, !c.weak, c.weak, err)
		}
		if msg := err.Error(); !strings.Contains(msg, "/etc/rta/operators") || !strings.Contains(msg, "who may operate this server") {
			t.Errorf("%s: the refusal names neither the file nor what it decides: %s", c.label, msg)
		}
	}
}
