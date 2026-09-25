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

# The linter, at the version CI's reusable lint workflow is told to install
# (GOLANGCI_LINT_VERSION in ci.yml), so a finding here is a finding there and
# neither is a surprise about the tool's version. Installed under .tools on
# first use rather than run through `go run`: `make lint` also lints the tree
# as linux, and `go run` under GOOS=linux cross-builds the linter itself into
# a binary this machine cannot execute. Override GOLANGCI with a path to use
# another build.
# renovate: datasource=go depName=github.com/golangci/golangci-lint/v2
GOLANGCI_VERSION := v2.13.2
TOOLS := $(CURDIR)/.tools
GOLANGCI ?= $(TOOLS)/golangci-lint-$(GOLANGCI_VERSION)

# helm-docs, the same way: built from source at the release CI's lint-helm
# and update-helm-chart workflows build it at, so a README regenerated here
# is the README CI compares against. `go install` verifies the module
# against the Go checksum database, which is what a container image pulled
# by tag never was, and needs no Docker daemon. The footer of every README
# names the version, and a bare `go install` leaves that stamp empty — so
# the build carries the same -X main.version the upstream release does, or
# every regeneration would drop the last line.
# renovate: datasource=go depName=github.com/norwoodj/helm-docs
HELM_DOCS_VERSION := v1.14.2
HELM_DOCS ?= $(TOOLS)/helm-docs-$(HELM_DOCS_VERSION)

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
		/^[a-zA-Z0-9_-]+:.*##/ { printf "  $(CYAN)%-20s$(RESET) %s\n", $$1, $$2 }' \
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

# Binary size is a stated constraint (AGENTS.md), and until now it was a
# constraint nobody could fail: `make size` reports, it does not refuse. This
# does. The ceiling is on the linux/amd64 release binary, stripped the way a
# release strips it, because that is the one that ships in the image and the
# one most people download — measured 35.5 MB at v0.18.0 with 20 built-in
# plugins, so 40 MB is room for a release or two of ordinary growth and not
# for a dependency that brings a second runtime with it. Raise it here, on
# purpose, with the dependency that needed it named in the commit.
SIZE_LIMIT_MB ?= 40

size-check: ## Fail if the linux/amd64 release binary exceeds SIZE_LIMIT_MB
	@mkdir -p $(BUILDDIR)
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GOBUILD_CORE) -o $(BUILDDIR)/rta-size-probe ./cmd/rta
	@size=$$(wc -c < $(BUILDDIR)/rta-size-probe | tr -d ' '); \
	limit=$$(( $(SIZE_LIMIT_MB) * 1048576 )); \
	printf "linux/amd64 stripped: %d.%02d MB (ceiling %d MB)\n" $$((size/1048576)) $$((size%1048576*100/1048576)) $(SIZE_LIMIT_MB); \
	rm -f $(BUILDDIR)/rta-size-probe; \
	if [ "$$size" -gt "$$limit" ]; then \
	  echo "size-check: the binary exceeds SIZE_LIMIT_MB; raise the ceiling deliberately or drop what grew it"; exit 1; \
	fi

# Rewrites the official index's pin in Dockerfile.full's RTA_INDEXES line.
# Deciding *what* it should be is deliberately not this target's job — which
# commit of rta-plugins is newest is a question for whoever runs it, or for
# bump-plugins.yml, which asks GitHub — and keeping that lookup out of here
# is what lets the rewrite stay mechanical enough to trust. Only the official
# entry's ref moves; every other entry on the line is somebody's own and is
# left exactly as written.
#
# Through the environment rather than `$(REF)` in the recipe, because make
# expands a recipe's variables as *text* into the line it hands /bin/sh: a
# value spelled inline could close awk's quote and run. So it stays a value
# the shell expands rather than a string the shell parses, and awk takes it
# with -v for the same reason: never spliced into the program text.
bump-index: export BUMP_INDEX_REF := $(REF)
bump-index: ## Pin Dockerfile.full's official index at REF (a commit of rta-plugins)
	@test -n "$$BUMP_INDEX_REF" || { echo "bump-index needs REF=<commit>"; exit 1; }
	@printf '%s' "$$BUMP_INDEX_REF" | grep -qE '^[0-9a-f]{40}$$' || \
	  { echo "bump-index needs REF to be a 40-character commit sha, got '$$BUMP_INDEX_REF' — RTA_INDEXES is space-separated, so anything else in it would add an index rather than break the pin"; exit 1; }
	@awk -v ref="$$BUMP_INDEX_REF" '{ \
	  if ($$0 ~ /^ARG RTA_INDEXES="/) { \
	    n = split(substr($$0, 18, length($$0) - 18), entries, " "); line = ""; \
	    for (i = 1; i <= n; i++) { \
	      e = entries[i]; \
	      if (e ~ /^official(@|$$)/) e = "official@" ref; \
	      line = line (i > 1 ? " " : "") e; \
	    } \
	    print "ARG RTA_INDEXES=\"" line "\""; \
	  } else print }' Dockerfile.full > Dockerfile.full.tmp && mv Dockerfile.full.tmp Dockerfile.full

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

$(TOOLS)/golangci-lint-%:
	GOBIN=$(TOOLS) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$*
	mv $(TOOLS)/golangci-lint $@

$(TOOLS)/helm-docs-%:
	GOBIN=$(TOOLS) go install -trimpath -ldflags "-s -w -X main.version=$(patsubst v%,%,$*)" \
	  github.com/norwoodj/helm-docs/cmd/helm-docs@$*
	mv $(TOOLS)/helm-docs $@

# CI lints on linux and most of the work here happens on macOS. A conversion
# that one port's syscall types need and another's do not is a finding on
# exactly one of them, so the tree is linted as the host and as linux both.
lint: $(GOLANGCI) ## golangci-lint, with .golangci.yml's linters, as the host and as linux
	$(GOLANGCI) run ./...
	GOOS=linux $(GOLANGCI) run ./...

# The parsers that read hostile bytes — terminal and model text cleaning, the
# path gate, an index's OCI reference, the token file, the roster, the YAML
# anchor check, the mail domain a grant is written from, the mail grader
# that reads a third party's zone and the terminal output debug.ansi explains
# — each with a Fuzz target beside its tests. `go test` runs
# their seed corpora on every ordinary run; this is the mutating run, which
# a pull request does not pay for and the scheduled scan does. FUZZTIME is
# per target.
FUZZTIME ?= 20s
FUZZ_TARGETS := internal/textclean:FuzzTerminal internal/textclean:FuzzModel \
	internal/pathguard:FuzzCheck internal/plugindist:FuzzParseOCIRef \
	internal/mcp:FuzzLoadTokenFile internal/operator:FuzzLoadRoster \
	internal/yamlguard:FuzzRefuseAnchors \
	builtin/audit:FuzzGradeMail builtin/audit:FuzzMailDomain \
	builtin/debug:FuzzExplainAnsi

fuzz: ## Fuzz every hostile-input parser for FUZZTIME each
	@for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; name=$${t#*:}; \
		echo "==> $$pkg $$name"; \
		go test ./$$pkg -run '^$$' -fuzz="^$$name\$$" -fuzztime=$(FUZZTIME) || exit 1; \
	done

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
ci: fmt-check vet lint hard coverage proto-lint proto-check cross size-check ## Everything CI runs
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

##@ Chart

CHART_DIR ?= charts/rta-chart

# The chart's values schema does not restate what a config file may hold — it
# embeds the schema rta already publishes.
#
# Without this there would be two hand-maintained descriptions of the same
# shape, in this repository and in the chart beside it, and they would drift the
# first time a config key was added and only one of them was updated. The
# failure that drift produces is the quiet kind: `helm install` accepts a values
# file whose config block rta will reject, or refuses one rta would have been
# happy with, and either way the message names the wrong thing.
#
# What is embedded is the envelope. A `plugins:` section holds keys each plugin
# declares about itself, and those stay open objects here exactly as they are
# there — `rta doctor` remains the deep validator, and the Kind-cluster install
# is where it gets to run against what the chart rendered.
#
# The `$$ref`s are rewritten because rta's schema refers to its own definitions
# from the document root (`#/$$defs/profile`), and nested under this chart's
# `rtaConfig` that path resolves against the chart's root instead — silently, to
# nothing, leaving those subtrees unvalidated rather than erroring.
define CHART_SCHEMA_PY
import json, pathlib, sys

check = "--check" in sys.argv
chart = pathlib.Path(sys.argv[1])
config = json.loads(pathlib.Path(sys.argv[2]).read_text())

config.pop("$$schema", None)


def rewrite(node):
    if isinstance(node, dict):
        ref = node.get("$$ref")
        if isinstance(ref, str) and ref.startswith("#/$$defs/"):
            node["$$ref"] = ref.replace("#/$$defs/", "#/$$defs/rtaConfig/$$defs/", 1)
        for value in node.values():
            rewrite(value)
    elif isinstance(node, list):
        for value in node:
            rewrite(value)


rewrite(config)

values = json.loads(chart.read_text())
if check:
    if values["$$defs"]["rtaConfig"] != config:
        sys.exit(
            "chart values schema is stale: its embedded config schema no longer "
            "matches `rta config schema`. Run `make chart-schema`."
        )
    print("chart-schema-check: the embedded config schema matches `rta config schema`.")
else:
    values["$$defs"]["rtaConfig"] = config
    chart.write_text(json.dumps(values, indent=2) + "\n")
    print("wrote", chart)
endef
export CHART_SCHEMA_PY

chart-schema: ## Embed `rta config schema` into the chart's values schema
	@mkdir -p $(BUILDDIR)
	@go run ./cmd/rta config schema > $(BUILDDIR)/config.schema.json
	@python3 -c "$$CHART_SCHEMA_PY" $(CHART_DIR)/values.schema.json $(BUILDDIR)/config.schema.json

chart-schema-check: ## Fail if the chart's embedded config schema has drifted
	@mkdir -p $(BUILDDIR)
	@go run ./cmd/rta config schema > $(BUILDDIR)/config.schema.json
	@python3 -c "$$CHART_SCHEMA_PY" $(CHART_DIR)/values.schema.json $(BUILDDIR)/config.schema.json --check

chart-lint: chart-schema-check ## Lint the chart and validate values.yaml against its schema
	helm lint $(CHART_DIR) --values $(CHART_DIR)/ci/test-values.yaml
	helm template rta $(CHART_DIR) --values $(CHART_DIR)/ci/test-values.yaml >/dev/null

chart-docs: $(HELM_DOCS) ## Regenerate the chart's README from values.yaml
	$(HELM_DOCS) --chart-search-root charts

##@ Housekeeping

clean: ## Remove build output and coverage artifacts
	rm -f rta coverage.out coverage.html
	rm -rf $(BUILDDIR) dist

# `proto` has to be here: a directory of that name exists, so without this
# make would find it up to date and report success for a target that ran
# nothing.
.PHONY: help setup download tidy fmt build install \
	cross snapshot size size-check bump-index test hard vet lint check \
	fmt-check coverage coverage-html ci proto proto-lint proto-check \
	chart-schema chart-schema-check chart-lint chart-docs \
	clean
