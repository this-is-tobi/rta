package app

import "net"

// exposedBind reports whether a bound address answers to other machines: a
// wildcard, or any address that is not loopback. Said once at startup,
// because `--http 0.0.0.0:8443` is a two-character difference from the
// loopback bind the docs show, and nothing else in the banner would tell an
// operator that the bearer wall is now the machine's edge.
func exposedBind(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	return len(tcp.IP) == 0 || tcp.IP.IsUnspecified() || !tcp.IP.IsLoopback()
}
