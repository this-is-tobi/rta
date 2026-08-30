package net

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
		"name": "example.com", "type": "auto", "server": "127.0.0.1:9", "timeout": 2,
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
func TestANameThatDoesNotExistIsStillNoRecords(t *testing.T) {
	_, err := runDNS(t.Context(), plugin.NewRequest(map[string]any{
		"name": "no-such-name-rta-test.invalid", "type": "auto", "timeout": 5,
	}, false, false))
	if err == nil {
		t.Skip("this resolver answers for .invalid, so there is nothing to assert")
	}
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("want a view.Error, got %T", err)
	}
	if verr.Code != "net.dns.norecords" {
		t.Errorf("a nonexistent name reported %s, want net.dns.norecords: %s", verr.Code, verr.Message)
	}
}
