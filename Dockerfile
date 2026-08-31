# syntax=docker/dockerfile:1

# Pinned by digest, not by tag - the same reason `rta plugin trust` binds to a
# digest rather than a name: a tag can move under you, a digest cannot.
FROM golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36 AS build
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
# recipe (docs/20-mcp.md) be writable by the nonroot user below without the
# operator chowning it by hand first.
RUN mkdir /rta-home && chown 65532:65532 /rta-home

# gcr.io/distroless/static: a CA bundle and the /etc/passwd entry for its
# nonroot user, nothing else - no shell, no package manager, no libc for
# anything to reach. Plain `scratch` would build and then fail every TLS
# handshake (cert.go, http.go, and every plugin that dials TLS - pg, s3,
# vault, qdrant...) with no /etc/ssl/certs to verify against; distroless's
# static variant is the standard fix for exactly that gap in a CGO_ENABLED=0
# binary that still needs to do real TLS.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=build /out/rta /usr/local/bin/rta
COPY --from=build /rta-home /rta-home

# Explicit rather than relied-on: the base image already defaults to this
# user, but a hardening choice nobody can see in this file is one somebody
# removes by switching base images later without noticing what it cost.
USER nonroot:nonroot

# /usr/local/bin, not the base image's own default: a derived image that
# bakes in plugins (docs/20-mcp.md's "share the image" recipe) COPYs them
# beside rta here and expects them on PATH without also having to repeat
# this line.
ENV PATH=/usr/local/bin

ENTRYPOINT ["/usr/local/bin/rta"]
