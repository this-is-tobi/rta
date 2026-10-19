// Package verdict is the first line of an overview: the worst thing a page found,
// in a few words and led by the word the theme draws it by.
//
// A dashboard tile is the first eight lines of a page, and cut from the bottom, so
// the line that decides whether anybody reads on goes first and is graded by the
// words ClassifyStatus knows — ok, warn, fail — rather than by a column a key/value
// page does not have. A page notes what it finds in the order it looks, and the
// verdict keeps the worst, and the first of two equals: the order a page looks in is
// the order of what it names first.
package verdict

type grade int

const (
	ok grade = iota
	warn
	fail
)

// Verdict is what a page has found so far. The zero value is ok.
type Verdict struct {
	grade grade
	why   string
}

// Warn notes something the reader should look at: it becomes the verdict unless a
// warning or a failure is already.
func (v *Verdict) Warn(why string) {
	if v.grade < warn {
		v.grade, v.why = warn, why
	}
}

// Fail notes something broken: it becomes the verdict unless a failure is already.
func (v *Verdict) Fail(why string) {
	if v.grade < fail {
		v.grade, v.why = fail, why
	}
}

// String is the line: "ok", or "warn — <why>" or "fail — <why>".
func (v Verdict) String() string {
	switch v.grade {
	case warn:
		return "warn — " + v.why
	case fail:
		return "fail — " + v.why
	}
	return "ok"
}
