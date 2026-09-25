# =============================================================================
# corebank API
#
# A static Go binary: nothing in the API needs cgo, so it builds with
# CGO_ENABLED=0 and runs on any base. The race detector in CI is the only thing
# that still uses cgo.
# =============================================================================

# --- build -------------------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first: they change far less often than the source, so this layer
# survives most rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# No `-ldflags "-s -w"`: stripping symbols saves a few megabytes and takes the
# stack traces with it, which is a poor trade for a service that holds somebody's
# finances.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/api      ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/ibkrsync ./cmd/ibkrsync \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/import   ./cmd/import

# --- runtime -----------------------------------------------------------------
FROM alpine:3.22

# ca-certificates to reach the Anthropic API and IBKR over TLS. curl is for the
# healthcheck, which the deployment's compose file runs as
# `curl --fail http://127.0.0.1:8080/healthz`; it stays so the image and that
# file can change independently.
RUN apk add --no-cache ca-certificates curl \
 && adduser -S -u 10001 -H corebank

WORKDIR /app

COPY --from=build /out/api      /usr/local/bin/api
COPY --from=build /out/ibkrsync /usr/local/bin/ibkrsync
COPY --from=build /out/import   /usr/local/bin/import

USER corebank

EXPOSE 8080

# The API applies its embedded migrations on startup, so this is the whole
# entrypoint — there is no separate migrate step to forget.
CMD ["api"]
