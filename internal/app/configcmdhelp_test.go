package app

import (
	"regexp"
	"strings"
	"testing"
)

// A help that tells a person to set a key must name one that exists: the example
// under `rta config set` was a refusal for as long as the key it named was gone,
// and the line was copied from the page that shows how.
func TestTheConfigHelpNamesOnlyKeysThatExist(t *testing.T) {
	reg := configRegistry(t)
	root := NewRoot(reg, "test")
	keyNamed := regexp.MustCompile(`\b(dashboard\.[a-z]+|theme\.[a-z]+|output)\b`)
	for _, path := range [][]string{{"config"}, {"config", "set"}, {"config", "get"}, {"config", "unset"}} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		text := cmd.Long + "\n" + cmd.Example
		for _, arg := range cmd.Flags().Args() {
			text += "\n" + arg
		}
		for _, key := range keyNamed.FindAllString(text, -1) {
			if _, verr := resolveConfigKey(reg, key); verr != nil {
				t.Errorf("`rta %s --help` names %s, which is no key: %s", strings.Join(path, " "), key, verr.Message)
			}
		}
	}
	// And the example that sets one is a line that works.
	if _, errOut, _, err := configRun(t, reg, "", "config", "set", "dashboard.width", "6"); err != nil {
		t.Errorf("the example for dashboard.width is refused: %v\n%s", err, errOut)
	}
}
