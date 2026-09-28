package pluginhost

// socketDir is nothing on Windows, where go-plugin listens on loopback TCP
// and never makes a socket file for anything to leave behind.
// socketdir_unix.go is the half that has one.
func socketDir() (string, error) { return "", nil }
