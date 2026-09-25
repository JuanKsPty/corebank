# =============================================================================
# corebank API
#
# Debian, not Alpine, and that is not a preference. The TigerBeetle Go client
# links a precompiled native library that ships only for glibc targets
# (aarch64-linux, x86_64-linux) — there is no musl variant. Building on Alpine
# fails with every type in the package reported as undefined, because the build
# constraints exclude the package outright. The same reason forces CGO_ENABLED=1:
# without cgo there is nothing to link the client against.
#
# The container this produces needs `seccomp=unconfined` to run: the client sets up
# its own io_uring, which Docker's default profile blocks. See docker-compose.yml.
# =============================================================================

# --- build -------------------------------------------------------------------
FROM golang:1.26-bookworm AS build

WORKDIR /src

# Dependencies first: they change far less often than the source, so this layer
# survives most rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Every binary comes out of one build stage because they share every dependency,
# and compiling them in separate stages would download and compile the
# TigerBeetle client once per stage.
#
# No `-ldflags "-s -w"`: stripping symbols saves a few megabytes and takes the
# stack traces with it, which is a poor trade for a service that moves money.
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -o /out/api      ./cmd/api \
 && CGO_ENABLED=1 GOOS=linux go build -trimpath -o /out/ibkrsync ./cmd/ibkrsync \
 && CGO_ENABLED=1 GOOS=linux go build -trimpath -o /out/import   ./cmd/import

# --- runtime -----------------------------------------------------------------
FROM debian:bookworm-slim

# ca-certificates is needed to reach the Anthropic API over TLS when a key is
# configured. tzdata so timestamps render in a real zone rather than UTC-only.
# curl is for the healthcheck: /bin/sh here is dash, which has no /dev/tcp, and a
# healthcheck an operator cannot also run by hand is a liability.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata curl \
 && rm -rf /var/lib/apt/lists/*

# A service that never needs to write to its own filesystem should not run as
# the user that can.
RUN useradd --system --uid 10001 --no-create-home corebank

WORKDIR /app

COPY --from=build /out/api      /usr/local/bin/api
COPY --from=build /out/ibkrsync /usr/local/bin/ibkrsync
COPY --from=build /out/import   /usr/local/bin/import

USER corebank

EXPOSE 8080

# The API applies its embedded migrations on startup, so this is the whole
# entrypoint — there is no separate migrate step to forget.
CMD ["api"]
