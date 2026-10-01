package pathguard

import "strings"

// NameParts is p taken apart as goos's open takes it: at a forward slash, and
// on Windows at a backslash as well, which takes either for a separator — a
// link there may hold a target written with forward slashes, which mklink
// makes and the kernel follows a part at a time. Split at the backslash
// alone, such a target was one part: filepath.Join took its .. off the name
// before anything asked where the link before it led, and the directories on
// its way were never put to the gate. Empty parts are kept, for a walk to
// pass over as it passes over ".".
//
// One for every walk that follows links a part at a time — the gate's own
// (resolve) and builtin/git's, which walks a repository's links through the
// roots — since two that took a name apart differently would judge one path
// and open another.
func NameParts(goos, p string) []string {
	if goos == "windows" {
		p = strings.ReplaceAll(p, "/", `\`)
		return strings.Split(p, `\`)
	}
	return strings.Split(p, "/")
}

// VolumeRooted reports whether target, a link's on goos that names no volume
// (filepath.IsAbs is asked first), leads from the root of the volume the link
// is on: one that opens on a separator, which only Windows has. Taken from
// the directory holding the link, it was walked somewhere Windows does not
// open.
func VolumeRooted(goos, target string) bool {
	return goos == "windows" && target != "" && (target[0] == '/' || target[0] == '\\')
}
