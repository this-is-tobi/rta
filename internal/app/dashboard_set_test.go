package app

import (
	"strings"
	"testing"
)

// --set was a StringSlice, which splits its argument on commas: a value
// holding one — net.port's own ports syntax, 22,80 — became two arguments,
// the second refused as not a key=value pair. It is one value now, as profile
// set's --set always was.
func TestDashboardAddTakesACommaAsPartOfTheValue(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("dashboard", "add", "db.status", "--set", "dbname=a,b"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if add := loadedConfig(t).Dashboard.Add; len(add) != 1 || add[0].With["dbname"] != "a,b" {
		t.Errorf("add = %+v, want dbname written as a,b", add)
	}
}

// The way back `dashboard rm` prints is a command to paste, and it joined the
// entry's arguments unquoted: a value with a space, a ';' or a '$(' made a
// line that passed a stray argument or ran a second command.
func TestDashboardRmPrintsAWayBackThatPastesAsTheTile(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("dashboard", "add", "db.status", "--set", "dbname=a b; $(id)"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err := run("dashboard", "rm", "db.status")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if want := "rta dashboard add db.status --set 'dbname=a b; $(id)'"; !strings.Contains(out, want) {
		t.Errorf("receipt = %q, want the way back as %s", out, want)
	}
}
