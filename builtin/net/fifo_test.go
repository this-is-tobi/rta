package net

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The overview's resolver line opened resolv.conf blocking too, with nobody
// having named it: a named pipe in its place held net.overview for good, on
// every surface. It is a file the caller did not name, so it is read as one.
func TestTheOverviewsResolverLineDoesNotWaitOnAFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "resolv.conf")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	orig := resolvConf
	resolvConf = fifo
	defer func() { resolvConf = orig }()

	done := make(chan string, 1)
	go func() { done <- dnsServers() }()
	select {
	case got := <-done:
		if got != "unknown" {
			t.Errorf("dnsServers() = %q, want unknown", got)
		}
	case <-time.After(5 * time.Second):
		if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("dnsServers on a FIFO with no writer did not return")
	}
}

// The hosts and resolver readers opened --file blocking, so a named pipe with
// nothing writing to it held the call — and an OS thread — for good. Over
// MCP, where both listings take the file from the caller, it is refused.
func TestTheListingsRefuseAFIFOOverMCPAtOnce(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	for name, h := range map[string]plugin.Handler{
		"net.hosts.list":    runHostsList,
		"net.resolver.list": runResolverList,
	} {
		done := make(chan error, 1)
		go func() {
			_, err := h(context.Background(), plugin.NewRequest(
				map[string]any{"file": fifo}, false, false).WithSurface(plugin.SurfaceMCP))
			done <- err
		}()
		select {
		case err := <-done:
			verr, ok := err.(*view.Error)
			if !ok || verr.Code != "net.sysfile.notafile" {
				t.Errorf("%s: err = %v, want net.sysfile.notafile", name, err)
			}
		case <-time.After(5 * time.Second):
			if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
				_ = w.Close()
			}
			t.Fatalf("%s on a FIFO with no writer did not return", name)
		}
	}
}
