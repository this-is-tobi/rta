// s3 is an external plugin — its own module, consuming the SDK exactly as a
// stranger would, the same reasoning plugins/pg documents.
//
// The replace is the one concession to nothing being published yet: a local
// path rather than a version, so `go build` here compiles against the SDK in
// this working tree.
module github.com/this-is-tobi/rule-them-all/plugins/s3

go 1.26.6

replace github.com/this-is-tobi/rule-them-all => ../..

require (
	github.com/minio/minio-go/v7 v7.3.0
	github.com/this-is-tobi/rule-them-all v0.0.0-00010101000000-000000000000
)

require (
	charm.land/glamour/v2 v2.0.1 // indirect
	charm.land/lipgloss/v2 v2.0.6 // indirect
	github.com/alecthomas/chroma/v2 v2.24.1 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260811164956-006e29f97886 // indirect
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/charmbracelet/x/exp/slice v0.0.0-20250904123553-b4e2667e5ad5 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dlclark/regexp2 v1.12.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/guptarohit/asciigraph v0.10.0 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-plugin v1.8.0 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/yuin/goldmark v1.8.5 // indirect
	github.com/yuin/goldmark-emoji v1.0.5 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.56.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260818201246-1b0934165a6f // indirect
	google.golang.org/grpc v1.83.1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/ini.v1 v1.67.3 // indirect
)
