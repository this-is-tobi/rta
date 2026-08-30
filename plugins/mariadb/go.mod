// mysql is a separate module, like every plugin here: a first-party plugin is
// the proof the SDK works, and it only proves it by consuming the SDK exactly
// as a stranger would. A stranger cannot reach into rta's internal packages,
// and neither can this.
//
// The replace is the one concession to nothing being published yet. It is a
// local path rather than a version, so `go build` here compiles against the
// SDK in this working tree — which is the point: a change that breaks a plugin
// author breaks this build, in the same commit that made it.
module github.com/this-is-tobi/rule-them-all/plugins/mariadb

go 1.26.6

replace github.com/this-is-tobi/rule-them-all => ../..

require (
	github.com/go-sql-driver/mysql v1.10.0
	github.com/this-is-tobi/rule-them-all v0.0.0-00010101000000-000000000000
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-plugin v1.8.0 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/oklog/run v1.1.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260818201246-1b0934165a6f // indirect
	google.golang.org/grpc v1.83.1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
