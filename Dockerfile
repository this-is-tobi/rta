# syntax=docker/dockerfile:1

# Pinned by digest, not by tag - the same reason `rta plugin trust` binds to a
# digest rather than a name: a tag can move under you, a digest cannot.
FROM golang:1.26.10-bookworm@sha256:632dff7351799a5a9954a48dc06670ade72ee44e8c351cdc0e5a57af2fea21af AS build
WORKDIR /src

# go.mod/go.sum copied first so this layer only invalidates on a dependency
# change, not on every source edit.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# TARGETOS/TARGETARCH are buildx's automatic per-platform ARGs; VERSION/COMMIT
# arrive from cd.yml so `rta version` inside the image matches the release it
# was built from, the same two values .goreleaser.yaml stamps into the
# binaries it ships. CGO_ENABLED=0 for the same reasons .goreleaser.yaml
# gives: no dynamic link to discover missing in a scratch-derived image, no
# cgo dependency the Landlock removal already dropped everywhere else.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/rta \
    ./cmd/rta

# A named volume mounted at a path that already exists in the image inherits
# that path's ownership the first time Docker creates it (documented volume
# behavior) - this is what lets `-v rta-home:/rta-home` in the container
# recipe (docs/30-boundary/20-mcp.md) be writable by the nonroot user below without the
# operator chowning it by hand first.
RUN mkdir /rta-home && chown 65532:65532 /rta-home

# gcr.io/distroless/static: a CA bundle and the /etc/passwd entry for its
# nonroot user, nothing else - no shell, no package manager, no libc for
# anything to reach. Plain `scratch` would build and then fail every TLS
# handshake (cert.go, http.go, and every plugin that dials TLS - pg, s3,
# vault, qdrant...) with no /etc/ssl/certs to verify against; distroless's
# static variant is the standard fix for exactly that gap in a CGO_ENABLED=0
# binary that still needs to do real TLS.
#
# The Debian 13 variant, because Debian 12 left regular security support for
# its LTS tail and the debian12 image followed: its current build carries the
# advisories for ca-certificates and tzdata that Debian has fixed, so rebuilding
# on it cleared nothing and the weekly scan of the released image stayed red.
# The binary is static, so the base's libc never mattered; what the move buys
# is a base that is still rebuilt on Debian's regular security releases.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

COPY --from=build /out/rta /usr/local/bin/rta
COPY --from=build /rta-home /rta-home

# Explicit rather than relied-on: the base image already defaults to this
# user, but a hardening choice nobody can see in this file is one somebody
# removes by switching base images later without noticing what it cost.
USER nonroot:nonroot

# /usr/local/bin, not the base image's own default: a derived image that
# bakes in plugins (docs/30-boundary/20-mcp.md's "share the image" recipe) COPYs them
# beside rta here and expects them on PATH without also having to repeat
# this line.
ENV PATH=/usr/local/bin

ENTRYPOINT ["/usr/local/bin/rta"]
