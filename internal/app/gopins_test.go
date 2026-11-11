package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Go's version is stated in four files, and one pull request has to move all
// of them. go.mod's directive is what CI reads through go-version-file and
// what the release workflow builds the archives with, because cd.yml passes
// it no GO_VERSION. mise.toml is what a person working here builds with. And
// the golang image each Dockerfile builds in compiles the release image and
// the full image.
//
// Renovate's "Go toolchain" group moves them together, but only because
// renovate.json tells it to touch a go directive: by default it never
// proposes a new one. Without that, a Go security release moved the
// Dockerfiles and mise.toml and left go.mod on the release before it, so the
// image was built on the patched standard library and CI's dependency scan
// and the release archives were not. The scan failed on every pull request
// that touched Go until somebody read why, and nothing here compared the
// pins in the meantime.
//
// So the pins are compared here, and a pull request that moves some and not
// all of them fails in its own checks, whatever moved them.
var (
	goDirective = regexp.MustCompile(`(?m)^go (\d+\.\d+\.\d+)$`)
	misePin     = regexp.MustCompile(`(?m)^go = "(\d+\.\d+\.\d+)"$`)
	golangImage = regexp.MustCompile(`(?m)^FROM golang:(\d+\.\d+\.\d+)[-@ ]`)
)

var goPins = []struct {
	file string
	read *regexp.Regexp
}{
	{"go.mod", goDirective},
	{"mise.toml", misePin},
	{"Dockerfile", golangImage},
	{"Dockerfile.full", golangImage},
}

func TestEveryGoPinNamesTheSameRelease(t *testing.T) {
	root := repoRoot(t)
	var stated []string
	releases := map[string]bool{}
	for _, pin := range goPins {
		raw, err := os.ReadFile(filepath.Join(root, pin.file))
		if err != nil {
			t.Fatal(err)
		}
		found := pin.read.FindAllStringSubmatch(string(raw), -1)
		if len(found) == 0 {
			t.Fatalf("%s no longer states Go's version in a form this test reads; "+
				"if the line moved or changed shape, move this test with it", pin.file)
		}
		for _, m := range found {
			stated = append(stated, pin.file+" "+m[1])
			releases[m[1]] = true
		}
	}
	if len(releases) > 1 {
		t.Fatalf("Go's version differs between the files that state it: %s. One pull request "+
			"moves all of them (Renovate's \"Go toolchain\" group does, once renovate.json lets it "+
			"touch go.mod's directive), and a pin left behind is a pipeline or an image built on "+
			"the older standard library", strings.Join(stated, ", "))
	}
}
