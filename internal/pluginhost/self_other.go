//go:build !linux

package pluginhost

// HardenSelf does nothing off Linux, and the reason differs per platform
// rather than being one shrug.
//
// **macOS** has no PR_SET_DUMPABLE. The equivalent protection is the hardened
// runtime, which is a property of a *signed* binary and therefore of the
// release pipeline (M3) rather than of anything this process can do to
// itself. Between now and then, `vmmap`/`lldb` against rta at the same uid
// work, and this says so rather than implying the sandbox covers it —
// the sandbox confines plugins, not readers of rta.
//
// **Windows** protects a process's memory from same-user readers only via an
// ACL on the process object, which a caller with SeDebugPrivilege bypasses;
// there is no cheap self-applied equivalent worth claiming.
//
// Stated here rather than left as an empty function on the reasoning that an
// unexplained no-op is indistinguishable from a forgotten one — which is
// exactly how this control came to be documented in two places and
// implemented in none.
func HardenSelf() {}
