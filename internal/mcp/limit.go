package mcp

import (
	"fmt"
	"io"

	"github.com/this-is-tobi/rta/pkg/format"
)

// LimitRequests is r with each JSON value on it held to the size the listener
// holds a request body to (maxRequestBody), and the stream ended with an error
// at the first that is not.
//
// **A request is held whole while it is read, and nothing bounded one over
// stdio.** The listener has always refused a body over maxRequestBody; the
// stdio transport hands the SDK's decoder the stream as it comes, and the
// decoder holds a message until it ends, so a single argument of 100 MB took
// the server to 2 GB before any tool, gate or schema had seen it. A request an
// agent can write is the least trusted input the server has, and no model
// writes four mebibytes of arguments to one call.
//
// The stream is not cut at a newline, because the decoder does not read it by
// lines: a message may carry newlines between its tokens, and a hostile
// client would only have to. The value is followed as JSON is structured, by
// depth and by strings with their escapes, and its size counted from where it
// opens to where it closes, so what is held to max is what the decoder holds.
// White space between messages costs nothing.
//
// Ended and not skipped: the message's id is not known without holding it, so a
// request dropped could not be answered, and a call its client waits on for
// good is worse than a session that says why it ended.
func LimitRequests(r io.ReadCloser) io.ReadCloser { return limitTo(r, maxRequestBody) }

func limitTo(r io.ReadCloser, max int) io.ReadCloser { return &limited{ReadCloser: r, max: max} }

type limited struct {
	io.ReadCloser
	max int

	depth            int
	inString, escape bool
	size             int
	err              error
}

func (l *limited) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	n, err := l.ReadCloser.Read(p)
	if !l.feed(p[:n]) {
		l.err = fmt.Errorf("a request over %s: no tool takes arguments that large (the listener holds one to the "+
			"same size), so the session is ended", format.Bytes(l.max))
		return 0, l.err
	}
	return n, err
}

// feed follows b through the structure of the JSON on the stream and says
// whether the message it is part of is still within max.
func (l *limited) feed(b []byte) bool {
	for _, c := range b {
		closed := false
		switch {
		case l.inString:
			l.size++
			switch {
			case l.escape:
				l.escape = false
			case c == '\\':
				l.escape = true
			case c == '"':
				l.inString = false
			}
		case c == '"':
			l.size++
			l.inString = true
		case c == '{' || c == '[':
			l.size++
			l.depth++
		case c == '}' || c == ']':
			l.size++
			if l.depth > 0 {
				l.depth--
			}
			closed = l.depth == 0
		case l.depth == 0 && (c == ' ' || c == '\n' || c == '\r' || c == '\t'):
			l.size = 0
		default:
			l.size++
		}
		if l.size > l.max {
			return false
		}
		// After the check, so the byte that completes a message is counted in it.
		if closed {
			l.size = 0
		}
	}
	return true
}
