package sdktest

import "slices"

// vocabulary is the shared verb set: the word rta already uses for each
// standard operation, so that the same act is spelled the same way in every
// namespace. It is derived from the built-in catalogue rather than invented —
// every entry is a word at least one shipped capability ends in.
//
// The line drawn is between a *shared operation* and a *subject*. `note.list`
// and `kv.list` and `net.hosts.list` are the same act on three different
// things, so `list` is vocabulary. `sys.cpu` and `codec.b64` are not acts at
// all: the last segment names what the capability is about, which the ID
// grammar's two-segment form permits and which no cross-plugin agreement can
// or should constrain. Ten of these are already used by two or more plugins
// (add, edit, get, list, overview, rm, search, set, show, tags); the rest are
// used once but are plainly the standard word for their action, and a second
// plugin needing that action should reach for the same word rather than
// coining a rival.
//
// This is a warning list, not a whitelist. The point is not that a plugin
// may only use these words — most of the catalogue does not — but that a
// plugin performing one of these operations must not call it something else.
var vocabulary = []string{
	"add",
	"done",
	"edit",
	"get",
	"init",
	"inspect",
	"list",
	"overview",
	"reopen",
	"rm",
	"search",
	"set",
	"show",
	"status",
	"tags",
	"toggle",
}

// Vocabulary returns the shared verb set, sorted. It is exported so that a
// plugin author who trips the verb warning can see the whole list without
// leaving their editor, and so the list has exactly one home.
func Vocabulary() []string { return slices.Clone(vocabulary) }

// synonyms maps a word onto the vocabulary word that already means it. It is
// the half of the verb rule that earns its keep: "your word is not in the
// list" is a nudge, while "rta spells this `rm`, in four places" is an
// instruction.
//
// Deliberately absent: delete, put, post, head, patch. They are HTTP methods,
// the catalogue ships three of them as `http.delete`/`http.put`/`http.post`,
// and telling the next plugin that speaks HTTP to rename its DELETE to `rm`
// would be confidently wrong advice given to exactly the author most likely
// to need the word. Warnings rather than errors for this shape of case: a
// warning that is wrong is still worth avoiding.
var synonyms = map[string]string{
	"create":    "add",
	"insert":    "add",
	"make":      "add",
	"new":       "add",
	"destroy":   "rm",
	"drop":      "rm",
	"purge":     "rm",
	"remove":    "rm",
	"all":       "list",
	"enumerate": "list",
	"index":     "list",
	"ls":        "list",
	"cat":       "get",
	"fetch":     "get",
	"read":      "get",
	"retrieve":  "get",
	"change":    "edit",
	"modify":    "edit",
	"update":    "edit",
	"find":      "search",
	"grep":      "search",
	"lookup":    "search",
	"query":     "search",
	"describe":  "show",
	"dump":      "show",
	"print":     "show",
	"view":      "show",
	"assign":    "set",
	"save":      "set",
	"store":     "set",
	"write":     "set",
	"dashboard": "overview",
	"stats":     "overview",
	"summary":   "overview",
	"close":     "done",
	"complete":  "done",
	"finish":    "done",
	"uncheck":   "reopen",
	"undone":    "reopen",
	"health":    "status",
	"bootstrap": "init",
	"setup":     "init",
	"flip":      "toggle",
	"switch":    "toggle",
	"labels":    "tags",
	"analyse":   "inspect",
	"analyze":   "inspect",
	"examine":   "inspect",
}
