package net

import (
	"context"
	"crypto/tls"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// legacyTLSPort starts a TLS server that speaks nothing newer than TLS 1.1
// and returns its port.
func legacyTLSPort(t *testing.T) int {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{
		MinVersion: tls.VersionTLS10, //nolint:gosec // the legacy host under inspection
		MaxVersion: tls.VersionTLS11,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	_, port, err := stdnet.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(port)
	return n
}

// net probe --tls kept Go's client floor of TLS 1.2, so a host speaking only
// 1.0 or 1.1 failed the handshake: the probe reported a TLS failure about a
// host whose protocol was the thing to report. The handshake inspects, so it
// takes what the host speaks and names it.
func TestProbeNamesALegacyProtocol(t *testing.T) {
	s := probeSections(t, map[string]any{
		"host": "127.0.0.1", "port": legacyTLSPort(t), "tls": true, "timeout": 2, "wait": 1,
	})
	var got string
	for _, p := range s.Items[0].View.(view.KeyValue).Pairs {
		if p.Key == "tls" {
			got = p.Value
		}
	}
	if got != "TLS 1.1" {
		t.Errorf("tls = %q, want TLS 1.1", got)
	}
}

// net send carries the caller's bytes, which have no business crossing a
// retired protocol: its handshake keeps Go's floor, and the payload is never
// written to a host that speaks nothing newer.
func TestSendKeepsTheTLSFloor(t *testing.T) {
	_, err := runSend(context.Background(), plugin.NewRequest(map[string]any{
		"host": "127.0.0.1", "port": legacyTLSPort(t), "tls": true,
		"data": `PING\r\n`, "timeout": 2, "wait": 1,
	}, false, false))
	if ve := view.AsError(err, ""); ve == nil || ve.Code != "net.probe.tls" {
		t.Fatalf("err = %v, want net.probe.tls", err)
	}
}
