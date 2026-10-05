package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func executeRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := &cobra.Command{Use: "rta", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringP("output", "o", "pretty", "")
	root.AddCommand(&cobra.Command{Use: "boom", RunE: func(*cobra.Command, []string) error {
		return errors.New("something nothing coded went wrong")
	}})
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = Execute(context.Background(), root, "1.2.3", "0123456789abcdef")
	return out.String(), errOut.String(), err
}

// The one thing the library that used to run the command gave besides its help
// was a version flag, with the commit as `git log --oneline` shows it.
func TestTheVersionFlagNamesTheBuildAndTheShortCommit(t *testing.T) {
	out, _, err := executeRoot(t, "--version")
	if err != nil || strings.TrimSpace(out) != "rta version 1.2.3 (0123456)" {
		t.Errorf("--version printed %q (%v)", out, err)
	}
}

// An error nothing coded is written as prose whatever -o says, with the exit a
// usage mistake has, as docs/20-using/10-cli.md says; it is not dropped, and it
// is not a coded error under a name nobody gave it.
func TestAnErrorNothingCodedIsWrittenAsProse(t *testing.T) {
	for _, args := range [][]string{{"boom"}, {"boom", "-o", "json"}} {
		_, errOut, err := executeRoot(t, args...)
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("`rta %s`: err = %v, exit %d, want an error and exit 2", strings.Join(args, " "), err, ExitCode(err))
		}
		if !strings.Contains(errOut, "ERROR") || !strings.Contains(errOut, "something nothing coded went wrong") {
			t.Errorf("`rta %s` wrote %q", strings.Join(args, " "), errOut)
		}
		if strings.Contains(errOut, "{") || strings.Contains(errOut, "core.error") {
			t.Errorf("`rta %s` wrote a structure or an invented code: %q", strings.Join(args, " "), errOut)
		}
	}
}
