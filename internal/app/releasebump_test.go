package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The bump workflow commits a pin move to Dockerfile.full, and that commit
// must never be a release: an rta release makes rta-plugins release every
// plugin against it, a plugin release changes the index, the workflow moves
// the pin, and a bump that released rta would turn the two repositories into
// a loop that released each other forever — which happened once, for one
// turn, while the bump was typed `build`. What stops it is nothing more than
// the commit's type being one release-please treats as hidden: it opens a
// release pull request only for commits whose type is visible in the
// changelog sections, and says "No user facing commits found" otherwise.
//
// Two files, one rule, and a change to either alone reintroduces the loop
// with nothing failing until the next release. So the rule is checked here:
// the type the workflow commits with is a changelog section, and that
// section is hidden.
func TestTheBumpCommitIsATypeReleasePleaseHides(t *testing.T) {
	root := repoRoot(t)
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "bump-plugins.yml"))
	if err != nil {
		t.Fatal(err)
	}
	typed := regexp.MustCompile(`git commit -m "([a-z]+)(?:\([^)]*\))?: `).FindStringSubmatch(string(workflow))
	if typed == nil {
		t.Fatal("bump-plugins.yml no longer commits with a conventional type this test can read; " +
			"if the commit line moved, move this test with it")
	}
	commitType := typed[1]

	raw, err := os.ReadFile(filepath.Join(root, "release-please-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Packages map[string]struct {
			Sections []struct {
				Type   string `json:"type"`
				Hidden bool   `json:"hidden"`
			} `json:"changelog-sections"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	pkg, ok := cfg.Packages["."]
	if !ok {
		t.Fatal("release-please-config.json has no package at \".\"")
	}
	for _, s := range pkg.Sections {
		if s.Type != commitType {
			continue
		}
		if !s.Hidden {
			t.Fatalf("bump-plugins.yml commits as %q, and release-please-config.json lists that type as "+
				"visible: every merged bump would open a release pull request, and rta and "+
				"rta-plugins would release each other in a loop", commitType)
		}
		return
	}
	t.Fatalf("bump-plugins.yml commits as %q, which release-please-config.json does not list at all; "+
		"list it hidden, so a merged bump is never a release", commitType)
}
