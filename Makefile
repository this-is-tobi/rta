# How this repository is built, checked and released.
#
# `make help` lists every target with its description. The listing is read out
# of this file, so it cannot drift from what is actually here the way a
# hand-maintained one does.
#
# One module: rta itself, `./cmd/rta`, plus `examples/plugin-hello`, which has
# no go.mod of its own and compiles as part of it. The eleven first-party
# plugins are separate modules in github.com/this-is-tobi/rta-plugins — a
# first-party plugin consumes the SDK exactly as a stranger's would, from a
# released rta, and that repository's Makefile builds, checks and releases
# them.

.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Knobs
# ---------------------------------------------------------------------------

# Where `install` puts the binary: the same place `go install` uses.
BINDIR ?= $(shell go env GOBIN)
ifeq ($(BINDIR),)
BINDIR := $(shell go env GOPATH)/bin
endif

# Local build output. Deliberately not `dist/`, which is GoReleaser's and
# which `snapshot` empties.
BUILDDIR ?= bin

# `--tags` without `--always`: with no tag in reach this fails and the version
# stays `dev`, which is the honest answer for a build off a branch. The commit
# is reported separately — the Go toolchain records it in every binary built
# from a checkout and `rta --version` reads it back — so a bare hash here
# would only print the same thing twice.
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)

# Release flags on the ordinary build, because the ordinary build is what
# people run and what a release ships. -w drops DWARF and -s the symbol table:
# 21 MB of a 71 MB binary, none of which a user of a CLI reads. A panic still
# prints a full stack trace with function names and line numbers — that comes
# from pclntab, which stays. -trimpath keeps the build machine's directory
# layout out of the artifact, which is both smaller and nobody else's
# business. Debugging with dlv wants a plain `go build` without these.
#
# One `-ldflags` holding every flag, not one per flag. `go build` keeps the
# last `-ldflags` and discards the rest, so the `-ldflags=-s -ldflags=-w` this
# variable used to hold applied `-w` alone and left the symbol table in. It
# cost nothing only because nothing referenced the variable either — the two
# build lines spelled the flags out correctly by hand.
GOBUILD_CORE := go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)"

# The trees that make up the root module.
#
# `./examples` is in this list and was missing from the one it replaces.
# examples/plugin-hello has no go.mod — it is part of this module, not a
# separate one — so it compiles under `go build ./...` and was the single
# directory nothing formatted. It is also the first Go a plugin author reads.
FMT_PATHS := ./builtin ./cmd ./examples ./internal ./pkg

# Every target a release ships. Cross compilation is host-independent, so this
# runs once rather than per runner, and it builds to /dev/null: what it checks
# is that the build constraints resolve, not that the binary runs.
#
# **A laptop can check this and nobody did.** `builtin/fs/platform.go` asserted
# on `syscall.Stat_t` behind a comma-ok and documented the Windows case as
# degrading to "always the same device" — but that type does not exist on
# Windows, so the package did not compile there at all and the degradation it
# promised could never happen. Nothing noticed, because nothing ever built for
# a platform other than the one it was sitting on.
CROSS_TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64 windows/arm64

# The wire contract. buf compiles .proto in pure Go and both generator plugins
# are `go run`, so the only thing anybody needs installed is the Go toolchain —
# no protoc, no buf binary, no registry account. Versions are pinned in
# proto/buf.gen.yaml: generated code that moves because somebody's toolchain
# moved is a diff nobody authored.
BUF := go run github.com/bufbuild/buf/cmd/buf@v1.47.2

# Installed if you have it, downloaded on demand if you do not — the same deal
# as buf, so a release can be rehearsed on a clean machine.
GORELEASER ?= $(shell command -v goreleaser 2>/dev/null)
ifeq ($(GORELEASER),)
GORELEASER := go run github.com/goreleaser/goreleaser/v2@v2.18.0
endif

# Colours, unless the caller said not to. NO_COLOR is the convention, and
# `make help | tee NOTES` should not paste escape codes into somebody's notes.
ifdef NO_COLOR
CYAN  :=
BOLD  :=
RESET :=
else
CYAN  := \033[36m
BOLD  := \033[1m
RESET := \033[0m
endif

##@ General

help: ## Print this help
	@awk 'BEGIN {FS = ":.*##"} \
		/^##@/ { printf "\n$(BOLD)%s$(RESET)\n", substr($$0, 5); next } \
		/^[a-zA-Z0-9_-]+:.*##/ { printf "  $(CYAN)%-16s$(RESET) %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST)
	@printf "\n$(BOLD)Notes$(RESET)\n"
	@printf "  Installed here:                          %s\n" "$(BINDIR)"
	@printf "  Version this build would stamp:          %s\n\n" "$(VERSION)"

##@ Setup

setup: download ## Fetch the module's dependencies — the one command after cloning
	@printf "\nReady. $(CYAN)make build$(RESET) for ./rta, $(CYAN)make help$(RESET) for everything else.\n"

download:
	go mod download

tidy: ## Tidy go.mod and go.sum
	go mod tidy

fmt: ## Format every Go file in the repository
	gofmt -w $(FMT_PATHS)

##@ Build

build: ## Build ./rta
	$(GOBUILD_CORE) -o rta ./cmd/rta

install: ## Install rta where `go install` puts it
	go install -trimpath -ldflags "-s -w -X main.version=$(VERSION)" ./cmd/rta

cross: ## Compile every release target and discard the output
	@for t in $(CROSS_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		echo "==> $$t"; \
		GOOS=$$os GOARCH=$$arch go build -o /dev/null ./cmd/rta || exit 1; \
	done

# Needs a git remote, even though it publishes nothing: GoReleaser reads the
# remote to work out what it would be releasing against, and refuses with `no
# remote configured to list refs from` without one. Nothing else in this file
# cares, so on a checkout that has no remote yet this is the one target that
# will not run.
snapshot: ## Rehearse a release locally — every archive and package, no tag, nothing published
	$(GORELEASER) release --snapshot --clean

size: build ## Build rta and report what it weighs
	@printf "\n$(BOLD)%-26s %10s$(RESET)\n" "ARTIFACT" "SIZE"
	@ls -l rta | awk '{ printf "%-26s %7.2f MB\n", $$NF, $$5/1048576 }'
	@printf "\nEvery plugin is a separate binary from rta-plugins, so the ones nobody installs cost nothing.\n\n"

##@ Test

test: ## Run the root module's tests
	go test ./...

# What CI runs, and what a change to shared state should be checked against:
# no cached results, no ordering luck, no data races. All three have caught
# something real.
hard: ## Run them with no cache, shuffled, under the race detector
	go test -count=1 -race -shuffle=on ./...

vet: ## go vet the root module
	go vet ./...

check: vet hard ## vet, then the hard test run

fmt-check: ## Fail if anything is unformatted — `make fmt` fixes it
	@out=$$(gofmt -l $(FMT_PATHS)); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out" | sed 's/^/  /'; exit 1; fi

# -coverpkg because most of internal/ is exercised from other packages' tests;
# without it the shared code reads as untested when it is not.
coverage: ## Coverage across the whole root module
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

coverage-html: coverage ## ... and write coverage.html to open in a browser
	go tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

# Everything CI runs, so a green laptop means a green pipeline.
#
# That sentence was false in both directions and had been since the proto was
# frozen: this target called proto-lint, which the pipeline did not, and
# neither called proto-check — the gate that actually enforces the freeze. A
# renumbered field passed gofmt, vet, build and the full race suite.
#
# fmt-check runs first now rather than last. Learning that a file is
# unformatted after the whole race suite has run is learning it ten minutes
# too late.
ci: fmt-check vet hard coverage proto-lint proto-check cross ## Everything CI runs
	@printf "\nci: green — every gate the pipeline runs.\n\n"

##@ Protocol

# Generated files are committed. A plugin author must be able to build against
# the contract with `go get`, and `go install ./cmd/rta` must work on a machine
# that has never heard of protobuf.
proto: ## Regenerate the gRPC contract from proto/
	cd proto && $(BUF) generate

proto-lint: ## Lint the .proto files
	cd proto && $(BUF) lint

# Against main rather than the working tree, so it answers "is this release
# compatible" and not "did I edit anything since the last run".
proto-check: ## Refuse a change that breaks a plugin already compiled against v1
	cd proto && $(BUF) breaking --against '../.git#branch=main,subdir=proto'

##@ Housekeeping

clean: ## Remove build output and coverage artifacts
	rm -f rta coverage.out coverage.html
	rm -rf $(BUILDDIR) dist

# `proto` has to be here: a directory of that name exists, so without this
# make would find it up to date and report success for a target that ran
# nothing.
.PHONY: help setup download tidy fmt build install \
	cross snapshot size test hard vet check \
	fmt-check coverage coverage-html ci proto proto-lint proto-check \
	clean
