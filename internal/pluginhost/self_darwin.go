package pluginhost

// HardenSelf does nothing on macOS, which has no PR_SET_DUMPABLE. The
// equivalent protection is the hardened runtime, which is a property of a
// *signed, notarised* binary and therefore of the release pipeline rather than
// of anything this process can do to itself. Until a release is signed,
// `vmmap`/`lldb` against rta at the same uid work, and this says so rather
// than implying the sandbox covers it — the sandbox confines plugins, not
// readers of rta.
//
// Stated here rather than left as an empty function on the reasoning that an
// unexplained no-op is indistinguishable from a forgotten one — which is
// exactly how this control came to be documented in two places and
// implemented in none.
func HardenSelf() {}
