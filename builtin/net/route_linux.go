package net

import (
	"errors"
	"os"
)

// The kernel's routing tables. The IPv6 one is absent on a host booted with
// ipv6.disable=1, which is a host with no IPv6 route and not one whose routes
// cannot be read.
const (
	procNetRoute     = "/proc/net/route"
	procNetIPv6Route = "/proc/net/ipv6_route"
)

// readDefaultRoute is the default route in force, IPv4 before IPv6 as a
// resolver prefers them, and found false when the tables hold none.
func readDefaultRoute() (route defaultRoute, found bool, err error) {
	v4, err := os.ReadFile(procNetRoute)
	if err != nil {
		return defaultRoute{}, false, err
	}
	if r, ok := parseProcNetRoute(string(v4)); ok {
		return r, true, nil
	}
	v6, err := os.ReadFile(procNetIPv6Route)
	if errors.Is(err, os.ErrNotExist) {
		return defaultRoute{}, false, nil
	}
	if err != nil {
		return defaultRoute{}, false, err
	}
	r, ok := parseProcNetIPv6Route(string(v6))
	return r, ok, nil
}
