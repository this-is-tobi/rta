package sdktest

import (
	"context"
	"fmt"
	"net"
	"slices"
	"sync"
	"testing"
	"time"
)

// Nowhere is an address for a conformance run's connection inputs: a listener
// the suite opens for the run whose directory is dir, which accepts every
// connection, closes it at once, and remembers it. A Write or Destructive
// capability whose dry run connects to it fails RuleDryRun, since a dry run
// reports what a call would do and sends nothing.
//
// Call it from the function given to WithInputs, with the directory that
// function receives:
//
//	"acme.thing.set": {"address": sdktest.Nowhere(dir), "name": "conformance"},
//	"acme.page.put":  {"url": "http://" + sdktest.Nowhere(dir), "page": "home"},
//
// It replaces 127.0.0.1:1, where nothing listens: a dry run that stopped being
// dry failed there with a refused connection, which is a coded error, which the
// rule took for the dry run's answer. The suite passed a capability whose dry
// run sent the request.
func Nowhere(dir string) string {
	nowhereMu.Lock()
	defer nowhereMu.Unlock()
	if n, ok := nowheres[dir]; ok {
		return n.addr
	}
	panic("sdktest.Nowhere: " + dir + " is not the directory of a conformance run — call it from the " +
		"function given to sdktest.WithInputs, with the directory that function receives")
}

var (
	nowhereMu sync.Mutex
	nowheres  = map[string]*nowhere{}
)

// nowhere is the listener behind Nowhere: the remote address of every
// connection it accepted since it was last asked.
type nowhere struct {
	addr string
	mu   sync.Mutex
	seen []string
}

// openNowhere starts the listener for the run in dir, until the test ends.
func openNowhere(t *testing.T, dir string) *nowhere {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sdktest: opening the address sdktest.Nowhere gives: %v", err)
	}
	n := &nowhere{addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n.mu.Lock()
			n.seen = append(n.seen, c.RemoteAddr().String())
			n.mu.Unlock()
			_ = c.Close()
		}
	}()
	nowhereMu.Lock()
	nowheres[dir] = n
	nowhereMu.Unlock()
	t.Cleanup(func() {
		_ = ln.Close()
		nowhereMu.Lock()
		delete(nowheres, dir)
		nowhereMu.Unlock()
	})
	return n
}

// reached is how many connections arrived since it was last asked, and resets
// the count. A connection is complete in the kernel before the listener's own
// goroutine has taken it, so a count read at once could miss one a dry run had
// just made: this makes one of its own and waits until that one is taken.
// Connections are taken in the order they arrived, so every one made before it
// has been counted by then.
func (n *nowhere) reached() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", n.addr)
	if err != nil {
		return 0, fmt.Errorf("checking the address sdktest.Nowhere gives: %w", err)
	}
	mine := c.LocalAddr().String()
	defer func() { _ = c.Close() }()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		n.mu.Lock()
		at := slices.Index(n.seen, mine)
		if at >= 0 {
			others := len(n.seen) - 1
			n.seen = nil
			n.mu.Unlock()
			return others, nil
		}
		n.mu.Unlock()
	}
	return 0, fmt.Errorf("the address sdktest.Nowhere gives did not take a connection within 5s")
}

// watching is the listener opened for the run in dir, or nil for a run the
// suite did not open one for (its own tests drive rules directly).
func watching(dir string) *nowhere {
	nowhereMu.Lock()
	defer nowhereMu.Unlock()
	return nowheres[dir]
}
