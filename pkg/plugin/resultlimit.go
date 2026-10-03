package plugin

import (
	"fmt"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// DefaultResultLimit is the most a result may be over MCP unless the operator
// says otherwise: 8 MiB.
//
// Chosen from what the catalogue answers honestly. The largest results the
// built-ins give are an HTTP body, which is cut at 1 MiB, and listings that
// run to a few hundred kilobytes; a table dump or a cluster listing from a
// plugin is the largest there is, and a few thousand rows of it is a few
// megabytes. Past that an answer is no longer one a model can read, and what it
// costs the host is not proportional to its size: a plugin that returned 100
// MB took the server to 1.68 GB, the answer held in the gRPC buffer, in the
// decoded view, in the cleaned copy and in the JSON twice over. So the
// refusal comes while the answer is still being received, at a size an
// operator can raise (rta mcp serve --max-result) and a caller cannot.
const DefaultResultLimit = 8 << 20

// ResultTooLarge is the refusal of a result over the limit, with its size where
// it is known (0 where only that it was over is), and how to ask for less.
//
// One error for every place that notices — the host reading a plugin's answer,
// the bridge measuring a built-in's — so an agent reads the same sentence
// wherever it was caught. The call ran: what a write or a destroy did is done,
// and it is the answer that is withheld.
func ResultTooLarge(id string, size, limit int) *view.Error {
	was := "more than the limit"
	if size > 0 {
		was = format.Bytes(size)
	}
	return view.Errorf("core.result.toolarge",
		"%s ran and its result, %s, is over the %s a result may be over MCP", id, was, format.Bytes(limit)).
		WithHint(fmt.Sprintf("narrow what the call asks for — a smaller limit, a tighter filter, a narrower path — "+
			"and ask again; %s if the whole result is needed",
			AskOperator("mcp serve --max-result <MiB>")))
}
