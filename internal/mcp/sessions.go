package mcp

import (
	"net/http"
	"slices"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxSessions is how many sessions one bearer credential holds open at once on
// the HTTP listener. An agent client holds one, or one per window it runs.
const maxSessions = 16

// limitSessions keeps each bearer credential to maxSessions open sessions on the
// HTTP listener, closing its oldest when it opens one more.
//
// The SDK keeps a session until its client deletes it, and opening one is a
// single POST: 5000 initialize requests on one valid token took 0.8 s and held
// 64 MiB that nothing ever gave back (measured on `rta mcp serve --http`), so
// any holder of a credential, the least-privileged agent included, could
// exhaust the server's memory, and the backoff on failed attempts never saw it,
// the token being valid.
//
// Closed, never refused: a client that crashed and came back owns the sessions
// it left behind, and turning it away over them would punish the wrong one.
// Counted per credential, so one holder's sessions never close another's. And
// no idle timeout (the SDK's SessionTimeout) on top: the cap already bounds what
// a credential holds, and a session closed under a client that is merely quiet
// costs that client a failed call wherever it does not open a new one on the
// 404, as the specification asks.
//
// It runs inside the bearer check, which is what puts the credential on the
// request, and looks only at what opens a session: a POST that names none. The
// session is counted once the SDK has opened it, from the header it answers
// with, so a burst of them is cut back to the cap as each one lands.
func limitSessions(server *sdk.Server, next http.Handler) http.Handler {
	var mu sync.Mutex
	opened := map[string][]string{} // credential → its session IDs, oldest first
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		next.ServeHTTP(w, req)
		id := w.Header().Get("Mcp-Session-Id")
		if req.Method != http.MethodPost || req.Header.Get("Mcp-Session-Id") != "" || id == "" {
			return
		}
		var who string
		if info := auth.TokenInfoFromContext(req.Context()); info != nil {
			who = info.UserID
		}
		live := map[string]*sdk.ServerSession{}
		for ss := range server.Sessions() {
			live[ss.ID()] = ss
		}
		mu.Lock()
		ids := slices.DeleteFunc(append(opened[who], id), func(id string) bool { return live[id] == nil })
		var oldest []string
		if over := len(ids) - maxSessions; over > 0 {
			oldest, ids = slices.Clone(ids[:over]), ids[over:]
		}
		opened[who] = ids
		mu.Unlock()
		for _, id := range oldest {
			_ = live[id].Close()
		}
	})
}
