# How this repository is built, checked and released.
#
# `make help` lists every target with its description. The listing is read out
# of this file, so it cannot drift from what is actually here the way a
# hand-maintained one does.
#
# **Two kinds of module live here, and they are built differently.** The root
# module is rta itself — one binary, `./cmd/rta`. Everything under `plugins/`
# is a *separate* module with its own go.mod, on purpose: a first-party plugin
# consumes the SDK exactly as a stranger would, and cannot reach into rta's
# internal packages. The cost is that `go test ./...` from here compiles none
# of them, which is why every plugin target below walks the modules one at a
# time — and why the first external plugin once sat outside every gate.

.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Knobs
# ---------------------------------------------------------------------------

# Where `install` and `plugins-install` put binaries. The same place `go
# install` uses, so a plugin lands beside the rta that has to find it — rta
# discovers plugins on $PATH, and two different directories is the way to get
# a plugin installed and invisible.
BINDIR ?= $(shell go env GOBIN)
ifeq ($(BINDIR),)
BINDIR := $(shell go env GOPATH)/bin
endif

# Local build output. Deliberately not `dist/`, which is GoReleaser's and
# which `snapshot` empties.
BUILDDIR ?= bin

# Where `index` writes the generated plugin index. Under dist/ because it is
# release output rather than source: the manifests state a checksum per
# artifact, so a copy committed here would be a set of claims that stopped
# being true the next time anything was built. `clean` already takes dist/.
INDEXDIR ?= dist/index

# What the generated manifests point a reader at. An index entry is mostly
# prose, and the one link on it should reach the thing being described.
INDEX_HOMEPAGE ?= https://github.com/this-is-tobi/rule-them-all

# The platform whose artifacts `index` can describe: this one. Reading a
# plugin's declaration means running it, so a manifest generated here claims
# the machine it was generated on and nothing else. A release claims all six
# because it has all six to hash.
HOST_PLATFORM := $(shell go env GOOS)/$(shell go env GOARCH)

# A git identity for the generated index, which is a real repository because
# attaching one is a clone. Supplied inline so this works on a machine that
# has never set a global one.
GIT_AS_RTA := -c user.email=rta@localhost -c user.name=rta -c commit.gpgsign=false

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
#
# The plugins take the same version as the core, and that is a claim rather
# than a shortcut: each one is a separate module, but it is built from this
# tree against this SDK and released by this repository's own pipeline, so a
# release genuinely produces eleven new artifacts. `rta plugin trust` binds to
# a digest, so a rebuild is a new artifact needing a new approval — a plugin
# that reported an unchanged version across a release would be denying exactly
# the event rta makes an operator look at. Nothing in the distribution path
# compares versions semantically (plugindist.Outdated is a string `!=`), so
# per-plugin numbering later is a different value passed here, not a migration.
GOBUILD_CORE   := go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
GOBUILD_PLUGIN := go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)"

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

# The rta that `plugins-trust` calls. Whatever is on $PATH by default — the
# trust store is one per machine, so it does not matter which binary writes
# it. `RTA=./rta` uses the one you just built.
RTA ?= rta

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

# ---------------------------------------------------------------------------
# The plugin modules, discovered rather than listed
# ---------------------------------------------------------------------------

# A hand-maintained list is a list that goes stale, and this one has: the CI
# matrix enumerated ten plugin modules while eleven were in the tree, so the
# newest plugin — the one most likely to be broken — was the one nothing
# checked. Everything here reads the filesystem instead, and `make
# plugins-list` is what anything outside this file should read.
PLUGINS := $(sort $(notdir $(patsubst %/go.mod,%,$(wildcard plugins/*/go.mod))))

# PLUGIN=<name> narrows every plugin target to one module.
#
# Validated rather than filtered. `make plugins-build PLUGIN=cngp` matching
# nothing would build nothing and exit 0 — a green run that did no work, which
# is the one failure mode a build system must never have.
ifdef PLUGIN
ifeq ($(filter $(PLUGIN),$(PLUGINS)),)
$(error no plugin module named '$(PLUGIN)'. Have: $(PLUGINS))
endif
PLUGIN_LIST := $(PLUGIN)
else
PLUGIN_LIST := $(PLUGINS)
endif

# Each plugin target below is one static pattern rule over module names, so
# the aggregate and the single-module form are the same code path: `make
# plugins` and `make check-plugin-cnpg` cannot disagree about what checking a
# plugin means, and `make -j` parallelises either.
#
# Static rather than implicit, which is not a style choice. **make skips
# implicit-rule search for a target declared .PHONY**, so the ordinary
# `check-plugin-%:` form silently matched nothing here: `make plugins` printed
# not one line and exited 0. A build system reporting success for work it did
# not do is the exact failure the PLUGIN check above exists to prevent, and it
# took writing that check to notice this one.
#
# Defined over every module, while the aggregates below depend on the narrowed
# list — so `make build-plugin-cnpg` works whether or not PLUGIN is set.
CHECK_PLUGINS    := $(PLUGINS:%=check-plugin-%)
BUILD_PLUGINS    := $(PLUGINS:%=build-plugin-%)
INSTALL_PLUGINS  := $(PLUGINS:%=install-plugin-%)
TIDY_PLUGINS     := $(PLUGINS:%=tidy-plugin-%)
DOWNLOAD_PLUGINS := $(PLUGINS:%=download-plugin-%)

##@ General

help: ## Print this help
	@awk 'BEGIN {FS = ":.*##"} \
		/^##@/ { printf "\n$(BOLD)%s$(RESET)\n", substr($$0, 5); next } \
		/^[a-zA-Z0-9_-]+:.*##/ { printf "  $(CYAN)%-16s$(RESET) %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST)
	@printf "\n$(BOLD)Notes$(RESET)\n"
	@printf "  Narrow any plugin target to one module:  $(CYAN)make plugins PLUGIN=cnpg$(RESET)\n"
	@printf "  Or address it directly:                  $(CYAN)make build-plugin-cnpg$(RESET)\n"
	@printf "  Installed here:                          %s\n" "$(BINDIR)"
	@printf "  Version this build would stamp:          %s\n\n" "$(VERSION)"

##@ Setup

setup: download $(PLUGIN_LIST:%=download-plugin-%) ## Fetch every module's dependencies — the one command after cloning
	@printf "\nReady. $(CYAN)make build$(RESET) for ./rta, $(CYAN)make help$(RESET) for everything else.\n"

download:
	go mod download

$(DOWNLOAD_PLUGINS): download-plugin-%:
	@echo "==> plugins/$* (download)"
	@cd plugins/$* && go mod download

tidy: $(PLUGIN_LIST:%=tidy-plugin-%) ## Tidy go.mod and go.sum, root module and every plugin
	go mod tidy

$(TIDY_PLUGINS): tidy-plugin-%:
	@echo "==> plugins/$* (tidy)"
	@cd plugins/$* && go mod tidy

fmt: ## Format every Go file in the repository, plugins included
	gofmt -w $(FMT_PATHS)
	@for p in $(PLUGIN_LIST); do gofmt -w plugins/$$p; done

##@ Build

build: ## Build ./rta
	$(GOBUILD_CORE) -o rta ./cmd/rta

install: ## Install rta where `go install` puts it
	go install -trimpath -ldflags "-s -w -X main.version=$(VERSION)" ./cmd/rta

plugins-build: $(PLUGIN_LIST:%=build-plugin-%) ## Build every plugin binary into ./bin

$(BUILD_PLUGINS): build-plugin-%:
	@mkdir -p $(BUILDDIR)
	@echo "==> $(BUILDDIR)/rta-plugin-$*"
	@cd plugins/$* && $(GOBUILD_PLUGIN) -o ../../$(BUILDDIR)/rta-plugin-$* .

# The notice below is skipped when plugins-trust asked for this, because that
# target approves them in the next breath and telling somebody their plugins
# are not allowed to run immediately before allowing them is noise that
# teaches people to skip the notice that matters.
plugins-install: $(PLUGIN_LIST:%=install-plugin-%) ## Install every plugin binary beside rta
ifeq ($(filter plugins-trust,$(MAKECMDGOALS)),)
	@echo
	@echo "Installed, and not yet allowed to run."
	@echo
	@echo "rta finds anything named rta-plugin-* on your PATH and loads it by"
	@echo "executing it, so discovery and approval are separate acts and the"
	@echo "second one stays yours:"
	@echo
	@echo "    rta plugin trust           # what was found and not run"
	@echo "    rta plugin trust <name>    # approve that artifact"
	@echo "    make plugins-trust         # approve the ones built here, and only those"
	@echo
endif

# Approving the plugins this repository just built, in one command.
#
# **Only the ones built here.** The loop is over the modules in `plugins/`,
# never over what rta discovered on $PATH, so this cannot approve an
# `rta-plugin-*` that arrived from somewhere else — which is the entire thing
# trust exists to stop. PLUGIN=<name> narrows it further.
#
# Nothing depends on this target and nothing acquires it as a side effect.
# `make plugins-install` does not trust, `make ci` does not trust, and there
# is no flag that folds it into either: the value of trust is that a person
# decided, and a decision taken automatically as part of a build is not one.
# Typing this is the decision, which is why it is spelled out in the name.
#
# Every rebuild invalidates it, because trust attaches to the artifact's
# digest and the digest just changed. That is not friction to route around —
# it is the feature — and it is the reason this is one command rather than
# eleven. Each approval prints what it approved: the path, the size, the
# timestamp and the digest. Read them; that is what the step is for.
plugins-trust: plugins-install ## Build, install and approve this repository's plugins
	@command -v $(RTA) >/dev/null || { \
		echo "cannot run '$(RTA)': 'make install' puts rta on your PATH,"; \
		echo "or 'make build' and pass RTA=./rta"; \
		exit 1; \
	}
	@for p in $(PLUGIN_LIST); do \
		$(RTA) plugin trust $$p || exit 1; \
	done

$(INSTALL_PLUGINS): install-plugin-%:
	@mkdir -p "$(BINDIR)"
	@echo "==> $(BINDIR)/rta-plugin-$*"
	@cd plugins/$* && $(GOBUILD_PLUGIN) -o "$(BINDIR)/rta-plugin-$*" .

cross: plugins-name-check ## Compile every release target, core and plugins, and discard the output
	@for t in $(CROSS_TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		echo "==> $$t"; \
		GOOS=$$os GOARCH=$$arch go build -o /dev/null ./cmd/rta || exit 1; \
	done
	@for p in $(PLUGIN_LIST); do \
		echo "==> plugins/$$p (windows/amd64)"; \
		(cd plugins/$$p && GOOS=windows GOARCH=amd64 go build -o /dev/null .) || exit 1; \
	done

# Needs a git remote, even though it publishes nothing: GoReleaser reads the
# remote to work out what it would be releasing against, and refuses with `no
# remote configured to list refs from` without one. Nothing else in this file
# cares, so on a checkout that has no remote yet this is the one target that
# will not run.
snapshot: ## Rehearse a release locally — every archive and package, no tag, nothing published
	$(GORELEASER) release --snapshot --clean

# An index is a git repository of plugins/<name>.yaml, and every claim in one
# is checked against the artifact at somebody else's install. So none of it is
# written by hand: `rta plugin manifest` runs each binary the way a load does
# and writes down what it declares, which makes a manifest that disagrees with
# its plugin unrepresentable rather than merely discouraged.
#
# What comes out is attachable, not a sketch. It is a real repository whose
# artifact URLs point at the binaries this just built, so installing from it
# performs the same fetch, the same hash and the same sandboxed declaration
# check a published index gets — the whole path, minus the publishing.
index: build plugins-build ## Generate an attachable plugin index from the binaries just built
	@rm -rf $(INDEXDIR)
	@mkdir -p $(INDEXDIR)/plugins
	@for p in $(PLUGIN_LIST); do \
		./rta plugin manifest $(BUILDDIR)/rta-plugin-$$p \
			--index $(INDEXDIR) \
			--homepage $(INDEX_HOMEPAGE) \
			--platform $(HOST_PLATFORM)=$(BUILDDIR)/rta-plugin-$$p >/dev/null || exit 1; \
		echo "==> $(INDEXDIR)/plugins/$$p.yaml"; \
	done
	@git -C $(INDEXDIR) init --quiet
	@git -C $(INDEXDIR) $(GIT_AS_RTA) add .
	@git -C $(INDEXDIR) $(GIT_AS_RTA) commit --quiet -m "$(VERSION) ($(HOST_PLATFORM))"
	@printf "\n$(BOLD)%s$(RESET) holds %s manifests for %s. Attach it:\n\n" \
		"$(INDEXDIR)" "$(words $(PLUGIN_LIST))" "$(HOST_PLATFORM)"
	@printf "  $(CYAN)rta plugin index add local $(CURDIR)/$(INDEXDIR)$(RESET)\n"
	@printf "  $(CYAN)rta plugin search$(RESET)\n"
	@printf "  $(CYAN)rta plugin install local/%s$(RESET)\n\n" "$(firstword $(PLUGIN_LIST))"

size: build plugins-build ## Build everything and report what it weighs
	@printf "\n$(BOLD)%-26s %10s$(RESET)\n" "ARTIFACT" "SIZE"
	@{ ls -l rta; ls -l $(BUILDDIR)/rta-plugin-*; } | \
		awk '{ printf "%-26s %7.2f MB\n", $$NF, $$5/1048576 }'
	@printf "\nEach plugin is a separate binary, so the ones nobody installs cost nothing.\n\n"

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

# The test line carries the same flags `hard` gives the root module, for the
# same reasons — and because CI runs this exact target rather than a
# per-module matrix carrying its own flags, this line is the only place the
# plugin suites meet the race detector at all. Dropping the flags here would
# drop them everywhere, silently. CI runs this with -k so one broken module
# does not hide the ten behind it; plain `make plugins` keeps stop-on-first
# for the tight local loop.
plugins: $(PLUGIN_LIST:%=check-plugin-%) plugins-replace-check ## Build, vet, test and format-check every plugin module

$(CHECK_PLUGINS): check-plugin-%:
	@echo "==> plugins/$*"
	@cd plugins/$* && go build -o /dev/null . && go vet ./... && go test ./... -count=1 -race -shuffle=on
	@test -z "$$(cd plugins/$* && gofmt -l .)" || { echo "gofmt needed in plugins/$*"; exit 1; }

# A `replace` naming an absolute path builds on exactly one machine.
#
# Four plugins here shipped `=> /Users/<somebody>/dev/rule-them-all` and did
# not compile for any other clone or any CI runner, from the day each landed.
# Every local gate stayed green throughout, because locally the directory is
# there — which is precisely why this has to be a gate and not a habit.
plugins-name-check:
	@bad=$$(ls -1 plugins 2>/dev/null | grep -vE '^[a-z0-9][a-z0-9-]*$$' || true); \
	if [ -n "$$bad" ]; then \
		echo "plugin directory name is not a plugin namespace:"; \
		echo "$$bad" | sed 's/^/  /'; \
		echo "lowercase letters, digits and dashes — a name make splices into a recipe"; \
		echo "is a name the shell expands"; \
		exit 1; \
	fi

# PLUGINS comes from $(wildcard plugins/*/go.mod) — which is to say from
# whatever directory names are in the tree, and a pull request adds those.
# Make does not expand `$(...)` inside a value it read from the filesystem; it
# splices the characters into the recipe line and hands them to the shell,
# which performs the command substitution. Verified here rather than reasoned
# about: a directory named `plugins/$(touch${IFS}rta-pwned)/` makes
#
#	printf '[%s]\n' $(PLUGINS)
#
# reach the shell as
#
#	printf '[%s]\n' $(touch${IFS}rta-pwned) cnpg docker ...
#
# and the file appears. CI runs `make plugins-list` and `make cross` on a fork
# pull request before anybody reads the Go source, which is the whole point of
# having a gate rather than a habit.
#
# **The obvious gate is itself vulnerable, which is worth the warning.** The
# first version of this rule was `printf '%s\n' $(PLUGINS) | grep -vE ...` —
# and it ran the payload while trying to detect it, then reported the tree
# clean, because the shell substituted the name before grep ever saw it. So
# the names must never reach a command line: `ls -1 plugins` is executed by
# the shell and its *output* is the names, which no later expansion touches.
#
# Quoting is not the fix either. Make expands $(PLUGINS) before the shell sees
# it, and per-word quoting across a make list does not stay correct. A
# name-shape gate does, and it closes every splice site at once — the loops in
# fmt, cross, plugins-install, index and clean as well as the two CI reaches —
# rather than the two that were reported.
#
# Every real plugin already matches, so this only rejects names no plugin
# would have. Wired as a prerequisite of the targets that splice the list, so
# make stops before expanding their recipes.
plugins-replace-check: plugins-name-check
	@bad=$$(grep -lE '^replace .*=> +(/|[A-Za-z]:)' plugins/*/go.mod 2>/dev/null); \
	if [ -n "$$bad" ]; then \
		echo "absolute replace directive — these build only on the machine that wrote them:"; \
		echo "$$bad" | sed 's/^/  /'; \
		echo "point it at a path relative to the module instead, e.g. => ../.."; \
		exit 1; \
	fi

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
ci: fmt-check vet hard coverage proto-lint proto-check plugins cross ## Everything CI runs
	@printf "\nci: green — every gate the pipeline runs, and the plugin modules too.\n\n"

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

plugins-list: plugins-name-check ## Print the plugin module names, one per line
	@printf '%s\n' $(PLUGINS)

clean: ## Remove build output, coverage artifacts and stray plugin binaries
	rm -f rta coverage.out coverage.html
	rm -rf $(BUILDDIR) dist
	@for p in $(PLUGINS); do rm -f plugins/$$p/rta-plugin-$$p plugins/$$p/$$p; done

# `plugins` is the one that has to be here: a directory of that name exists, so
# without this make would find it up to date and report success for a target
# that ran nothing.
.PHONY: help setup download tidy fmt build install plugins-build plugins-install plugins-trust \
	cross snapshot index size test hard vet check plugins plugins-replace-check \
	fmt-check coverage coverage-html ci proto proto-lint proto-check \
	plugins-list clean \
	$(CHECK_PLUGINS) $(BUILD_PLUGINS) $(INSTALL_PLUGINS) $(TIDY_PLUGINS) \
	$(DOWNLOAD_PLUGINS)
