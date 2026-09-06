package app

import (
	"net"
	"testing"
)

func TestOnlyALoopbackBindIsNotAnnouncedAsExposed(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8443": false,
		"[::1]:8443":     false,
		"0.0.0.0:8443":   true,
		"[::]:8443":      true,
		"10.0.0.7:8443":  true,
	}
	for raw, want := range cases {
		addr, err := net.ResolveTCPAddr("tcp", raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := exposedBind(addr); got != want {
			t.Errorf("exposedBind(%s) = %v, want %v", raw, got, want)
		}
	}
	if exposedBind(&net.UnixAddr{Name: "/tmp/x", Net: "unix"}) {
		t.Error("a unix socket is not a network edge")
	}
}
