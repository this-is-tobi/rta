package toolcall

// moved are the inputs a built-in capability used to take and another answers
// now, by the capability that lost the input, the input's name, and the
// capability that has it.
//
// Said in the refusal an unknown input would be, on both surfaces: a script, an
// alias or an agent holding the schema it was handed before is told only that
// the name is unknown, with the list of what the tool takes, and nothing there
// says the answer it wants is one call over. It keeps no behaviour, since a
// refusal that still ran the old call would be a shim, and it is a few lines
// that are deleted when nobody holds the old name any more.
var moved = map[string]map[string]string{
	"kv.status": {"detail": "kv.overview"},
}

// MovedTo is the capability that has an input another used to, and false for an
// input nothing moved.
func MovedTo(capability, input string) (string, bool) {
	to, ok := moved[capability][input]
	return to, ok
}
