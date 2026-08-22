.PHONY: plugins build install test hard vet check coverage clean proto proto-lint proto-check

build:
	go build -o rta ./cmd/rta

# The gate is "install -> value < 60s", which needs something to install.
install:
	go install ./cmd/rta

test:
	go test ./...

# What CI should run, and what a change to shared state should be checked
# against: no cached results, no ordering luck, no data races.
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

ci: vet hard coverage proto-lint proto-check plugins
	@test -z "$$(gofmt -l ./builtin ./cmd ./internal ./pkg)" || (echo "gofmt needed:"; gofmt -l ./builtin ./cmd ./internal ./pkg; exit 1)
