package paths

// SetUserConfigDir makes the platform's own config directory whatever a test
// says, so a macOS machine can be had on any host. The returned function puts
// the real one back.
func SetUserConfigDir(f func() (string, error)) (restore func()) {
	was := userConfigDir
	userConfigDir = f
	return func() { userConfigDir = was }
}
