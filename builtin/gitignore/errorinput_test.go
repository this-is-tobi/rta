package gitignore

import (
	"net/http"
	"strconv"
	"testing"
)

// A refusal of the names asked for names the input they were given in
// (view.Error.Input), so a form opens again on the templates box and an agent
// knows which argument to send again. What the repository answered names none:
// no name typed differently gets a listing the host does not have.
func TestARefusalOfTheNamesAskedForNamesTheTemplatesInput(t *testing.T) {
	tooMany := make([]string, maxTemplates+1)
	for i := range tooMany {
		tooMany[i] = "Go" + strconv.Itoa(i)
	}
	cases := []struct {
		name  string
		asked []string
		code  string
	}{
		{"a typo", []string{"Go", "pythom"}, "gitignore.template.unknown"},
		{"a name two templates share", []string{"ColdBox"}, "gitignore.template.ambiguous"},
		{"no name at all", []string{" ", ","}, "gitignore.templates.none"},
		{"more names than one file holds", tooMany, "gitignore.templates.toomany"},
	}
	c := showCapability()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, src := newRepo(t, sample())
			_, err := show(t, src, tc.asked...)
			ve := asViewError(t, err)
			if ve.Code != tc.code {
				t.Fatalf("refused as %+v, want %s", ve, tc.code)
			}
			if f, ok := c.ErrorInput(ve); !ok || f.Name != "templates" {
				t.Errorf("%s names the input %q, want templates, which %s declares", ve.Code, ve.Input, c.ID)
			}
		})
	}

	repo, src := newRepo(t, sample())
	repo.treeStatus = http.StatusNotFound
	_, err := show(t, src, "Go")
	if ve := asViewError(t, err); ve.Code != codeNotFound || ve.Input != "" {
		t.Errorf("a listing the host does not have = %+v, want %s naming no input", ve, codeNotFound)
	}
}
