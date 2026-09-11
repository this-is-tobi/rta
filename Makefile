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

# Rewrites the pins. Deciding *what* they should be is deliberately not this
# target's job: which rta-plugins tag is newest is a question for whoever runs
# it — `gh release list --repo this-is-tobi/rta-plugins` answers it — and
# keeping that lookup out of here is what lets the rewrite stay mechanical
# enough to trust. Never adds or removes a name: PLUGINS is meant to be built
# from the line already in the file, one entry bumped per entry, so widening
# the image's plugin allowlist stays the deliberate, by-hand edit
# Dockerfile.full's own comment describes.
#
# Through the environment rather than `$(PLUGINS)` in the recipe, because make
# expands a recipe's variables as *text* into the line it hands /bin/sh: with
# the value spelled inline, PLUGINS='pg/v9.9.9"; curl …|sh; "' closes awk's
# quote and runs. Nothing feeds this but a person today, which is exactly when
# the habit is cheap to keep: a version string is the kind of value that later
# arrives from somewhere else, and a git tag may legally contain `$`, a
# backtick, a quote or a semicolon — git forbids spaces and `:` `?` `*` `~`
# `^`, not those. So it stays a value the shell expands rather than a string
# the shell parses, and awk takes it with -v for the same reason: never
# spliced into the program text.
bump-plugins: export BUMP_PLUGINS := $(PLUGINS)
bump-plugins: ## Rewrite Dockerfile.full's plugin pins to PLUGINS (e.g. PLUGINS="pg/v0.3.3 s3/v0.2.0")
	@test -n "$$BUMP_PLUGINS" || { echo "bump-plugins needs PLUGINS=\"name/vX.Y.Z ...\""; exit 1; }
	@awk -v plugins="$$BUMP_PLUGINS" '{ if ($$0 ~ /^ARG PLUGINS="/) print "ARG PLUGINS=\"" plugins "\""; else print }' \
		Dockerfile.full > Dockerfile.full.tmp && mv Dockerfile.full.tmp Dockerfile.full

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

chart-docs: ## Regenerate the chart's README from values.yaml
	docker run --rm --volume "$(PWD)/charts:/helm-docs" -u "$$(id -u):$$(id -g)" \
	  docker.io/jnorwood/helm-docs:v1.14.2

##@ Housekeeping

clean: ## Remove build output and coverage artifacts
	rm -f rta coverage.out coverage.html
	rm -rf $(BUILDDIR) dist

# `proto` has to be here: a directory of that name exists, so without this
# make would find it up to date and report success for a target that ran
# nothing.
.PHONY: help setup download tidy fmt build install \
	cross snapshot size bump-plugins test hard vet check \
	fmt-check coverage coverage-html ci proto proto-lint proto-check \
	chart-schema chart-schema-check chart-lint chart-docs \
	clean
