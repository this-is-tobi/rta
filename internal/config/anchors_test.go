package config

import (
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A write that reaches the file without the loader having read it first still
// refuses an anchor before any decode, and says so in the loader's own words
// rather than as an encoding failure.
func TestAWriteOverAFileWithAnAnchorRefusesBeforeDecodingIt(t *testing.T) {
	path := configAt(t)
	const bomb = "a0: &a0 [x, x, x, x, x, x, x, x, x, x]\na1: &a1 [*a0, *a0, *a0, *a0, *a0, *a0, *a0, *a0, *a0, *a0]\n"
	if err := os.WriteFile(path, []byte("output: json\n"+bomb), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Write(Config{Output: "yaml"})
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "config.invalid" || !strings.Contains(verr.Message, "anchor") {
		t.Fatalf("err = %#v", err)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "output: json") {
		t.Errorf("the file was written over: %s", got)
	}
}
