package app

import "testing"

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
