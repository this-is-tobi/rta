module github.com/this-is-tobi/rta

go 1.27.3

// macOS keeps its own certificate verifier. From Go 1.27 a set SSL_CERT_FILE
// or SSL_CERT_DIR makes crypto/x509 on macOS load its roots from those files
// and verify with Go's own verifier, as it does on Linux, which reads none of
// the keychain's roots and checks no revocation, no Certificate Transparency
// and none of Apple's policy. Many Macs set one of the two for some other
// tool, rta hands both to every plugin (internal/pluginhost/env.go), and
// pkg/plugin/netdiag.go and docs/40-plugins/22-answers-errors-and-hints.md
// rest on a CA file being the one way around the system's checks.
//
// Go plans to remove the setting in Go 1.31, and that toolchain refuses this
// line ("removed GODEBUG ... set to old value"). Delete it then, and say in
// netdiag.go and on that page that either variable now runs Go's verifier on
// a Mac, as a CA file does.
godebug x509sslcertoverrideplatform=0

require (
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.1.1
	charm.land/huh/v2 v2.0.3
	charm.land/lipgloss/v2 v2.0.6
	filippo.io/age v1.3.2
	github.com/charmbracelet/colorprofile v0.4.3
	github.com/charmbracelet/ultraviolet v0.0.0-20261023060817-a45b7c86333f
	github.com/charmbracelet/x/ansi v0.11.9
	github.com/charmbracelet/x/exp/teatest/v2 v2.0.0-20261101151554-27ecf3e4d454
	github.com/coreos/go-oidc/v3 v3.21.0
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376
	github.com/go-git/go-billy/v5 v5.9.2
	github.com/go-git/go-git/v5 v5.19.3
	github.com/go-jose/go-jose/v4 v4.1.7
	github.com/goccy/go-yaml v1.19.2
	github.com/google/uuid v1.6.0
	github.com/guptarohit/asciigraph v0.10.0
	github.com/hashicorp/go-hclog v1.6.3
	github.com/hashicorp/go-plugin v1.8.0
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/prometheus-community/pro-bing v0.9.1
	github.com/shirou/gopsutil/v4 v4.26.10
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/tyler-smith/go-bip39 v1.1.0
	golang.org/x/crypto v0.58.0
	golang.org/x/net v0.62.0
	golang.org/x/sys v0.49.0
	golang.org/x/term v0.47.0
	golang.org/x/text v0.43.0
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
)

require (
	dario.cat/mergo v1.0.0 // indirect
	filippo.io/edwards25519 v1.2.0 // indirect
	filippo.io/hpke v0.4.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ProtonMail/go-crypto v1.1.6 // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aymanbagabas/go-udiff v0.4.1 // indirect
	github.com/catppuccin/go v0.2.0 // indirect
	github.com/charmbracelet/x/exp/golden v0.0.0-20251109135125-8916d276318f // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/exp/strings v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/cyphar/filepath-securejoin v0.6.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ebitengine/purego v0.11.2 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/lufia/plan9stats v0.0.0-20211012122336-39d0f177ccd0 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/mattn/go-runewidth v0.0.30 // indirect
	github.com/mitchellh/hashstructure/v2 v2.0.2 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/pjbgf/sha1cd v0.7.0 // indirect
	github.com/power-devops/perfstat v0.0.0-20260805114148-88456608a4f6 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sahilm/fuzzy v0.1.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/skeema/knownhosts v1.3.1 // indirect
	github.com/tklauser/go-sysconf v0.4.0 // indirect
	github.com/tklauser/numcpus v0.12.0 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	github.com/xo/terminfo v1.0.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260818201246-1b0934165a6f // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
)
