package net

import (
	stdnet "net"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// **A lookup that could not be made is not a lookup that found nothing.**
//
// `--type auto` asks A, AAAA and CNAME, and a name with only an A record
// legitimately misses two of them — which is why a per-type error was
// suppressed there. With no rows at all, though, every type failed, and the
// errors stop being the misses that come with asking broadly and become the
// answer. Suppressed anyway, `net dns example.com` inside a `--network none`
// container replied "no A/AAAA/CNAME records for example.com": a diagnostic
// tool reporting that a name does not resolve when it never found out.
//
// Same class as a config value of the wrong shape being read as the zero —
// a failure wearing the costume of a fact.
func TestADNSLookupThatCouldNotBeMadeIsNotNoRecords(t *testing.T) {
	// --server pointed at the discard port: it answers nothing, the way a
	// container with no network answers nothing. Driven through the declared
	// input rather than a test-only seam, so what is exercised is what ships.
	_, err := runDNS(t.Context(), plugin.NewRequest(map[string]any{
		"name": "example.com", "type": "auto", "server": "127.0.0.1:9", "timeout": "2s",
	}, false, false))
	if err == nil {
		t.Fatal("an unreachable resolver produced no error at all")
	}
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("want a view.Error, got %T", err)
	}
	if verr.Code == "net.dns.norecords" {
		t.Errorf("an unreachable resolver was reported as an answer: %s — %s", verr.Code, verr.Message)
	}
	if verr.Code != "net.dns.failed" {
		t.Errorf("code = %s, want net.dns.failed: %s", verr.Code, verr.Message)
	}
	// The message names every type it asked about, not just the last one: an
	// auto query that says "resolving AUTO example.com" describes a query
	// nobody made.
	for _, want := range []string{"A", "AAAA", "CNAME"} {
		if !strings.Contains(verr.Message, want) {
			t.Errorf("the failure does not say it asked for %s: %s", want, verr.Message)
		}
	}
}

// The other side of the same line: a name that really does not exist is an
// answer, and keeps saying so. Without this the fix is indistinguishable from
// turning every empty result into a failure.
//
// Asked of a resolver the test runs, through the same declared input as the
// test above. It asked the machine's own, and a machine whose resolver could
// not be reached — a laptop on a network that drops it, a sandbox — reported
// the lookup failed, which is the other test's answer and the right one there:
// this one is about what rta makes of a resolver saying no such name.
func TestANameThatDoesNotExistIsStillNoRecords(t *testing.T) {
	_, err := runDNS(t.Context(), plugin.NewRequest(map[string]any{
		"name": "no-such-name-rta-test.invalid", "type": "auto", "server": nxdomainServer(t), "timeout": "5s",
	}, false, false))
	if err == nil {
		t.Fatal("a resolver that said no such name produced no error at all")
	}
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("want a view.Error, got %T", err)
	}
	if verr.Code != "net.dns.norecords" {
		t.Errorf("a nonexistent name reported %s, want net.dns.norecords: %s", verr.Code, verr.Message)
	}
}

// nxdomainServer answers every question with NXDOMAIN, what a resolver says of
// a name that does not exist, and returns its address. Only the header and the
// question go back: whatever followed the question in the query, an EDNS
// record among it, is not part of the answer.
func nxdomainServer(t *testing.T) string {
	t.Helper()
	pc, err := stdnet.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			end := 12
			for end < n && buf[end] != 0 {
				end += int(buf[end]) + 1
			}
			end += 1 + 4 // the name's last label, then its type and class
			if end > n {
				continue
			}
			answer := append([]byte(nil), buf[:end]...)
			answer[2] = 0x80 | buf[2]&0x79 // a response, with the opcode and recursion asked for
			answer[3] = 0x80 | 3           // recursion available, NXDOMAIN
			answer[4], answer[5] = 0, 1    // the one question
			clear(answer[6:12])            // no answers, authorities or additions
			_, _ = pc.WriteTo(answer, from)
		}
	}()
	return pc.LocalAddr().String()
}
