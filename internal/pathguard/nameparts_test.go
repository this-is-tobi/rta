package pathguard

import (
	"slices"
	"testing"
)

// A name is taken apart as the system that opens it reads it. Windows takes
// a forward slash for a separator as well as a backslash, and a link there
// written with forward slashes, which mklink makes as readily, was walked as
// one part: the directories on its way were never put to the gate, and a
// .. in it was taken off the name before anything asked where it led.
// Elsewhere a backslash is a character of a name like any other. And a
// target opening on a separator, with no volume, leads from the root of the
// volume the link is on, which only Windows has.
func TestALinksTargetIsTakenApartAsTheSystemReadsIt(t *testing.T) {
	for _, c := range []struct {
		goos, name string
		want       []string
	}{
		{"windows", `..\work/dotfiles\.gitconfig`, []string{"..", "work", "dotfiles", ".gitconfig"}},
		{"windows", "../outside/secret", []string{"..", "outside", "secret"}},
		{"windows", "work/dotfiles/.gitconfig", []string{"work", "dotfiles", ".gitconfig"}},
		{"linux", `work/dot\files/.gitconfig`, []string{"work", `dot\files`, ".gitconfig"}},
		{"darwin", "a//b/", []string{"a", "", "b", ""}},
	} {
		if got := NameParts(c.goos, c.name); !slices.Equal(got, c.want) {
			t.Errorf("NameParts(%s, %q) = %q, want %q", c.goos, c.name, got, c.want)
		}
	}
	for _, c := range []struct {
		goos, target string
		want         bool
	}{
		{"windows", `\Users\x`, true},
		{"windows", "/Users/x", true},
		{"windows", "Users/x", false},
		{"linux", `\Users`, false},
	} {
		if got := VolumeRooted(c.goos, c.target); got != c.want {
			t.Errorf("VolumeRooted(%s, %q) = %v, want %v", c.goos, c.target, got, c.want)
		}
	}
}
