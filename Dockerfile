# Use the official Golang image as the base image
FROM golang:1.26-alpine

# Set the Current Working Directory inside the container
WORKDIR /app

# Install dependencies
RUN apk add --no-cache ca-certificates

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Copy the source from the current directory to the Working Directory inside the container
COPY . .

# Install swag tool for API documentation generation
RUN go install github.com/swaggo/swag/cmd/swag@latest

# Generate Swagger documentation
RUN swag init

# Build the main Go app
RUN go build -o loxilb-oam .

# Build the reset_admin CLI tool
RUN go build -o reset_admin cmd/reset_admin/main.go

# Create directories for logs with proper permissions
RUN mkdir -p /var/log/oam /var/log && \
    chmod 755 /var/log

# Expose ports (HTTP and HTTPS)
EXPOSE 8080 443

# Image version (set by `make docker-build`, defaults to the :latest tag).
# Declared here — after the build steps — so changing the version only rebuilds
# the label layer, not the whole image.
ARG VERSION=latest

# OCI labels: link the published GHCR package back to its source repo (so it
# inherits the repo's visibility for public releases) and record its metadata.
LABEL org.opencontainers.image.title="loxilb-oam" \
      org.opencontainers.image.description="LoxiLB OAM — Operations, Administration & Maintenance service" \
      org.opencontainers.image.source="https://github.com/loxilb-io/loxilb-oam" \
      org.opencontainers.image.url="https://github.com/loxilb-io/loxilb-oam" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"

# Command to run the executable
CMD ["sh", "-c", "./loxilb-oam -db-user=${DB_USER:-oamuser} -db-password=${DB_PASSWORD:?DB_PASSWORD must be set} -db-host=${DB_HOST:-127.0.0.1} -db-port=${DB_PORT:-3306} -db-name=${DB_NAME:-loxioam} -token-expiration=${TOKEN_EXPIRATION:-} -port=${SERVER_PORT:-8080}"]