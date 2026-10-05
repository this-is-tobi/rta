package app

import (
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// What the doctor's report amounts to, and the order that lets a person see it.
//
// The rows are written group by group, in the order a person would walk the
// machine: what is installed, what is configured, what agents may do. That is
// the order to write them in and the wrong order to read them in, because the
// reader came to learn whether anything needs them, and the answer sat between
// two rows that did not. A report that opens on sixteen ok rows and ends on the
// one that mattered is read the way a log is read, from the bottom, and a
// newcomer who does not yet know which rows are noise reads none of them.

// agentFacing names the checks whose info state is a statement about what an
// agent started from this shell can reach: the secrets store unlocking from the
// environment, and a grant that anything running as the operator can issue.
// Every other note describes the machine; these two describe the boundary, and
// are the ones to read first.
var agentFacing = map[string]bool{"kv store": true, "grant guard": true}

// rowRank is where a row sits in the report: what failed, what is
// questionable, the notes that decide what an agent can reach, the other notes,
// and last what is simply fine.
func rowRank(check, status string) int {
	switch status {
	case "error":
		return 0
	case "warn":
		return 1
	case "info":
		if agentFacing[check] {
			return 2
		}
		return 3
	}
	return 4
}

// orderRows puts the rows a person has to read first. Stable, so rows of one
// rank keep the order they were written in, which is the logical one.
func orderRows(rows [][]string) {
	sort.SliceStable(rows, func(i, j int) bool {
		return rowRank(rows[i][0], rows[i][1]) < rowRank(rows[j][0], rows[j][1])
	})
}

// doctorVerdict is the report's rows counted, by the checks they came from.
type doctorVerdict struct {
	errors, warnings, notes []string
	agent                   []string
}

func judge(t view.Table) doctorVerdict {
	var v doctorVerdict
	for _, r := range t.Rows {
		if len(r) < 2 {
			continue
		}
		switch r[1] {
		case "error":
			v.errors = append(v.errors, r[0])
		case "warn":
			v.warnings = append(v.warnings, r[0])
		case "info":
			v.notes = append(v.notes, r[0])
			if agentFacing[r[0]] {
				v.agent = append(v.agent, r[0])
			}
		}
	}
	return v
}

// status is the worst thing the report found, in the words the rows use.
func (v doctorVerdict) status() string {
	switch {
	case len(v.errors) > 0:
		return "error"
	case len(v.warnings) > 0:
		return "warn"
	}
	return "ok"
}

// line is the sentence under the table. "All ok" is only ever said of a
// machine with nothing to fix, and it still says how many notes there are,
// because a note is not a failure and is not nothing either: it is a fact the
// operator is better off having read.
func (v doctorVerdict) line() string {
	var parts []string
	if n := len(v.errors); n > 0 {
		parts = append(parts, format.Count(n, "error", "errors"))
	}
	if n := len(v.warnings); n > 0 {
		parts = append(parts, format.Count(n, "warning", "warnings"))
	}
	notes := ""
	if n := len(v.notes); n > 0 {
		notes = format.Count(n, "note", "notes") + " worth reading"
	}
	line := "all ok"
	switch {
	case len(parts) > 0 && notes != "":
		line = strings.Join(append(parts, notes), ", ")
	case len(parts) > 0:
		line = strings.Join(parts, ", ")
	case notes != "":
		line += " — " + notes
	}
	switch len(v.agent) {
	case 0:
	case 1:
		line += "; " + v.agent[0] + " bears most on what an agent can reach"
	default:
		line += "; " + strings.Join(v.agent, " and ") + " bear most on what an agent can reach"
	}
	return line
}

// styled is line, painted the colour of the worst row when the output takes colour.
func (v doctorVerdict) styled(color bool) string {
	if !color {
		return v.line()
	}
	return theme.StatusStyle(v.status()).Render(v.line())
}

// failure is the error doctor exits with when what it found says to, and nil
// when it found nothing that does. An error row always does; a warning does
// under strict.
//
// The report is on stdout by then, whole: this is the exit status of a check
// that has already said what it found, so a script gating on it and a person
// reading the log read the same rows.
func (v doctorVerdict) failure(strict bool) error {
	failing := append([]string(nil), v.errors...)
	var counts []string
	if n := len(v.errors); n > 0 {
		counts = append(counts, format.Count(n, "error", "errors"))
	}
	hint := "the rows marked error above say what to change"
	if strict {
		hint = "the rows marked error or warn above say what to change"
		if n := len(v.warnings); n > 0 {
			counts = append(counts, format.Count(n, "warning", "warnings"))
			failing = append(failing, v.warnings...)
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return view.Errorf("core.doctor.failed", "doctor found %s: %s",
		strings.Join(counts, " and "), strings.Join(unique(failing), ", ")).WithHint(hint)
}

// unique is names without repeats, in order: two rows can come from one check.
func unique(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}
