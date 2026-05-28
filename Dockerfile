# syntax=docker/dockerfile:1
#
# vor server image. CGO is required (tree-sitter grammars are C, statically
# linked into the binary), so we build with a C toolchain and run on a
# glibc base. modernc sqlite + pgx are pure Go, and git history uses go-git
# — so the runtime needs no git binary, only libc + CA certs.

# ---- build -----------------------------------------------------------------
FROM golang:1.25-bookworm AS build
WORKDIR /src

# Dependency layer — cached unless go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=docker
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ENV CGO_ENABLED=1
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath \
      -ldflags "-s -w \
        -X github.com/HundredAcreStudio/vor/internal/version.Version=${VERSION} \
        -X github.com/HundredAcreStudio/vor/internal/version.Commit=${COMMIT} \
        -X github.com/HundredAcreStudio/vor/internal/version.BuildDate=${BUILD_DATE}" \
      -o /out/vor ./cmd/vor

# ---- runtime ---------------------------------------------------------------
FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

# Non-root. /data holds the sqlite DB, /config the global config.yaml,
# /repos the source trees to register + watch — all mounted at run time.
RUN useradd --uid 10001 --create-home --shell /usr/sbin/nologin vor \
    && mkdir -p /data /config /repos \
    && chown -R vor:vor /data /config /repos
COPY --from=build /out/vor /usr/local/bin/vor

USER vor
ENV XDG_STATE_HOME=/data \
    XDG_CONFIG_HOME=/config \
    VOR_DB_URL=sqlite:/data/wiki.db \
    VOR_HOST=0.0.0.0 \
    VOR_PORT=7337
EXPOSE 7337

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD curl -fsS http://localhost:7337/api/health || exit 1

# Machine-wide daemon: serves the registered repos from /data/wiki.db.
# Register repos with `vor register /repos/<name>` (CLI in another shell,
# the REST endpoint, or the vor_track MCP tool).
ENTRYPOINT ["vor"]
CMD ["serve"]
