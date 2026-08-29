.PHONY: plugins build install test hard vet check coverage clean cross proto proto-lint proto-check

# Release flags on the ordinary build, because the ordinary build is what
# people run and what a release ships. -w drops DWARF and -s the symbol
# table: 21 MB of a 71 MB binary, none of which a user of a CLI reads. A
# panic still prints a full stack trace with function names and line
# numbers — that comes from pclntab, which stays. -trimpath keeps the build
# machine's directory layout out of the artifact, which is both smaller and
# nobody else's business. Debugging with dlv wants `go build` without these.
GOFLAGS_REL := -trimpath -ldflags=-s -ldflags=-w

build:
	go build -trimpath -ldflags="-s -w" -o rta ./cmd/rta

# The gate is "install -> value < 60s", which needs something to install.
install:
	go install -trimpath -ldflags="-s -w" ./cmd/rta

test:
	go test ./...

# What CI should run, and what a change to shared state should be checked
# against: no cached results, no ordering luck, no data races. All three
# have caught something real.
hard:
	go test -count=1 -race -shuffle=on ./...

vet:
	go vet ./...

check: vet hard

# -coverpkg because most of internal/ is exercised from other packages'
# tests; without it the shared code reads as untested when it is not.
coverage:
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

# The wire contract. buf compiles .proto in pure Go and both generator
# plugins are `go run`, so the only thing anybody needs installed is the Go
# toolchain — no protoc, no buf binary, no registry account. Versions are
# pinned in proto/buf.gen.yaml: generated code that moves because somebody's
# toolchain moved is a diff nobody authored.
#
# Generated files are committed. A plugin author must be able to build
# against the contract with `go get`, and `go install ./cmd/rta` must work on
# a machine that has never heard of protobuf.
BUF := go run github.com/bufbuild/buf/cmd/buf@v1.47.2

proto:
	cd proto && $(BUF) generate

proto-lint:
	cd proto && $(BUF) lint

# Refuses a change that would break a plugin already compiled against v1.
# Against main rather than the working tree, so it answers "is this release
# compatible" and not "did I edit anything since the last run".
proto-check:
	cd proto && $(BUF) breaking --against '../.git#branch=main,subdir=proto'

# Every target a release ships, in both build configurations.
#
# **A laptop can check this and nobody did.** `builtin/fs/platform.go` asserted
# on `syscall.Stat_t` behind a comma-ok and documented the Windows case as
# degrading to "always the same device" — but that type does not exist on
# Windows, so the package did not compile there at all and the degradation it
# promised could never happen. Nothing noticed, because nothing ever built for
# a platform other than the one it was sitting on.
#
# Cross-compilation is host-independent, so this runs once rather than per
# runner. It builds and discards: what it is checking is that the constraints
# resolve, not that the binary runs.
CROSS_TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64 windows/arm64

cross:
	@for t in $(CROSS_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		echo "==> $$t"; \
		GOOS=$$os GOARCH=$$arch go build -o /dev/null ./cmd/rta || exit 1; \
	done
	@for mod in $$(find plugins -name go.mod -maxdepth 2); do \
		dir=$$(dirname $$mod); \
		echo "==> $$dir (windows/amd64)"; \
		(cd $$dir && GOOS=windows GOARCH=amd64 go build -o /dev/null .) || exit 1; \
	done

clean:
	rm -f rta coverage.out

# Everything CI runs, so a green laptop means a green pipeline.
#
# That sentence was false in both directions and had been since the proto was
# frozen: this target called proto-lint, which the pipeline did not, and
# neither called proto-check — the gate that actually enforces the freeze. A
# renumbered field passed gofmt, vet, build and the full race suite.
# Every module under plugins/ is a separate go.mod, so `go test ./...` from
# the root does not see any of it. That is the point of them — they consume
# the SDK the way a stranger does — and it is also how the first external
# plugin came to be absent from CI entirely: a change to pkg/plugin that broke
# it would have gone green, which is precisely the breakage it exists to
# catch. Same shape as D37, where the proto gate ran nowhere.
plugins:
	@for mod in $$(find plugins -name go.mod -maxdepth 2); do \
		dir=$$(dirname $$mod); \
		echo "==> $$dir"; \
		(cd $$dir && go build ./... && go vet ./... && go test ./... -count=1) || exit 1; \
		test -z "$$(cd $$dir && gofmt -l .)" || (echo "gofmt needed in $$dir"; exit 1); \
	done

ci: vet hard coverage proto-lint proto-check plugins cross
	@test -z "$$(gofmt -l ./builtin ./cmd ./internal ./pkg)" || (echo "gofmt needed:"; gofmt -l ./builtin ./cmd ./internal ./pkg; exit 1)
