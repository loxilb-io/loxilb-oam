# ---- Build stage: compile the binaries with the full Go toolchain ----
FROM golang:1.27-alpine AS build

WORKDIR /app

# Download modules first so this layer caches on go.mod/go.sum alone.
COPY go.mod go.sum ./
RUN go mod download

# Copy the source and generate the Swagger docs (compiled into the binary).
COPY . .
RUN go install github.com/swaggo/swag/cmd/swag@latest && swag init

# Release identifier, stamped into the binary and repeated on the OCI label.
# Set by `make docker-build` / the release workflow (vMAJOR.MINOR.PATCH.BUILD,
# in lockstep with loxilb-io/loxilb); an unset build honestly reports "dev".
ARG VERSION=dev

# Static binaries (CGO off so they run on a minimal base; -trimpath/-s -w shrink them).
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o loxilb-oam . && \
    go build -trimpath -ldflags="-s -w" -o reset_admin ./cmd/reset_admin

# ---- Runtime stage: minimal Alpine with just the binaries ----
FROM alpine:3.21

WORKDIR /app

# ca-certificates: outbound TLS to managed LoxiLB instances. Alpine's busybox
# already provides the `sh` (CMD) and `wget` (compose healthcheck) used at runtime.
RUN apk add --no-cache ca-certificates && \
    mkdir -p /var/log/oam /var/log && chmod 755 /var/log

# Copy only what the runtime needs: the two binaries and the helper scripts
# (scripts/reset-admin.sh is invoked via `docker exec`).
COPY --from=build /app/loxilb-oam /app/reset_admin ./
COPY --from=build /app/scripts ./scripts

# Re-declared here because ARG is scoped per stage — same value as the build
# stage above, so the label matches the version compiled into the binary.
ARG VERSION=dev

# OCI labels: link the published GHCR package back to its source repo (so it
# inherits the repo's visibility for public releases) and record its metadata.
LABEL org.opencontainers.image.title="loxilb-oam" \
      org.opencontainers.image.description="LoxiLB OAM — Operations, Administration & Maintenance service" \
      org.opencontainers.image.source="https://github.com/loxilb-io/loxilb-oam" \
      org.opencontainers.image.url="https://github.com/loxilb-io/loxilb-oam" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"

# Expose ports (HTTP and HTTPS)
EXPOSE 8080 443

# Command to run the executable
CMD ["sh", "-c", "./loxilb-oam -db-user=${DB_USER:-oamuser} -db-password=${DB_PASSWORD:?DB_PASSWORD must be set} -db-host=${DB_HOST:-127.0.0.1} -db-port=${DB_PORT:-5432} -db-name=${DB_NAME:-loxioam} -token-expiration=${TOKEN_EXPIRATION:-} -port=${SERVER_PORT:-8080}"]
