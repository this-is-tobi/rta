package plugindist

import "testing"

// The index is written by the release pipeline one plugin at a time, so every
// entry published before rta stopped shipping for Windows still lists two
// windows rows, and keeps listing them until that plugin is next released.
// An install from such an entry has to read it and pick the host's own row:
// a parser that rejected the rows it no longer builds would break every
// plugin install from the official index until all of them were re-released.
func TestAnIndexEntryThatStillListsAPlatformRtaDoesNotBuildIsRead(t *testing.T) {
	const sum = "339662602bcfd8597988642a6cde474574652cd81b607ca4199d3dfd44b5353d"
	raw := []byte(`name: pg
version: 0.6.0
summary: "PostgreSQL"
platforms:
- os: linux
  arch: amd64
  url: https://example.test/rta-plugin-pg_0.6.0_linux_amd64.tar.gz
  sha256: ` + sum + `
  bin: rta-plugin-pg
- os: windows
  arch: amd64
  url: https://example.test/rta-plugin-pg_0.6.0_windows_amd64.tar.gz
  sha256: ` + sum + `
  bin: rta-plugin-pg.exe
capabilities:
- id: pg.status
  summary: connection health
  safety: read
`)
	m, verr := ParseManifest(raw)
	if verr != nil {
		t.Fatalf("an entry that lists a windows row was refused: %v", verr)
	}
	p, ok := m.PlatformFor("linux", "amd64")
	if !ok || p.Bin != "rta-plugin-pg" {
		t.Fatalf("the host's own row was not found beside the old one: %+v %v", p, ok)
	}
}
