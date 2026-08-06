# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BINARY_NAME=loxilb-oam
BINARY_UNIX=$(BINARY_NAME)_unix

# Default values for command-line flags.
# Credentials default to placeholders; override via environment or a .env file.
SSL_OPTION=false
DB_USER ?= oamuser
DB_PASSWORD ?= CHANGE_ME
DB_HOST=127.0.0.1
DB_PORT=3306
DB_NAME=loxioam
TOKEN_EXPIRATION=1440
SERVER_PORT=8080

SSL_OPTION=true
SSL_DB_USER ?= oamuser
SSL_DB_PASSWORD ?= CHANGE_ME
SSL_DB_HOST=127.0.0.1
SSL_DB_PORT=3308
SSL_DB_NAME=loxioam
SSL_TOKEN_EXPIRATION=1440
SSL_SERVER_PORT=443

SSL_OPTION=false
AWS_DB_USER ?= root
AWS_DB_PASSWORD ?= CHANGE_ME
AWS_DB_HOST ?= 127.0.0.1
AWS_DB_PORT=3306
AWS_DB_NAME=loxilb_db
AWS_TOKEN_EXPIRATION=1440
AWS_SERVER_PORT=8080

# ── Version ──────────────────────────────────────────────────────────────────
# loxilb-oam versions in lockstep with loxilb-io/loxilb and uses the same
# vMAJOR.MINOR.PATCH.BUILD scheme: loxilb-oam vX ships against loxilb vX.
# This is the single source of truth for a local build — it is stamped into the
# binary (main.version), the OpenAPI spec, and the image's OCI version label.
# A release build takes the version from the git tag instead (release.yml).
#   make build VERSION=v0.9.8.8
VERSION ?= v0.9.8.7
LDFLAGS  = -X main.version=$(VERSION)

# ── Container image (public releases go to GHCR) ─────────────────────────────
# The published image is $(IMAGE):$(TAG) == $(REGISTRY)/$(IMAGE_NAME):$(TAG),
# and TAG defaults to the version above. Override any part on the command line:
#   make docker-build VERSION=v0.9.8.8
#   make docker-build IMAGE_NAME=myorg/loxilb-oam REGISTRY=docker.io
#   make docker-build docker-push TAG=latest
REGISTRY   ?= ghcr.io
IMAGE_NAME ?= loxilb-io/loxilb-oam
TAG        ?= $(VERSION)
IMAGE      ?= $(REGISTRY)/$(IMAGE_NAME)
DOCKER_IMAGE ?= $(IMAGE):$(TAG)

# All target
all: test build

# Build the project. -ldflags stamps $(VERSION) into main.version; without it
# the binary honestly reports "dev".
build:
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) -v

# Run the unit tests. Mirrors the CI gate: the suites under tests/rest_api and
# tests/e2e need a live server + database, so they are excluded here and run
# by the `integration` CI job instead.
test:
	$(GOTEST) $(shell go list ./... | grep -v '/tests/rest_api' | grep -v '/tests/e2e')

# Clean the build files
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)

# Run the application
run:
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) -v
	./$(BINARY_NAME) -db-user=$(DB_USER) -db-password=$(DB_PASSWORD) -db-host=$(DB_HOST) -db-port=$(DB_PORT) -db-name=$(DB_NAME) -token-expiration=$(TOKEN_EXPIRATION) -port=$(SERVER_PORT)

run-https:
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) -v
	nohup ./$(BINARY_NAME) -db-user=$(DB_USER) -db-password=$(DB_PASSWORD) -db-host=$(DB_HOST) -db-port=$(DB_PORT) -db-name=$(DB_NAME) -token-expiration=$(TOKEN_EXPIRATION) -port=$(SSL_SERVER_PORT) --enable-https=true --ssl-cert-file=./ssl/server_certs/server.crt --ssl-key-file=./ssl/server_certs/server.key > output.log 2>&1 &

run-ssl:
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) -v
	./$(BINARY_NAME) -db-user=$(SSL_DB_USER) -db-password=$(SSL_DB_PASSWORD) -db-host=$(SSL_DB_HOST) -db-port=$(SSL_DB_PORT) -db-name=$(SSL_DB_NAME) -token-expiration=$(SSL_TOKEN_EXPIRATION) -port=$(SSL_SERVER_PORT) -ssl-option=$(SSL_OPTION)

run-aws:
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) -v
	./$(BINARY_NAME) -db-user=$(AWS_DB_USER) -db-password=$(AWS_DB_PASSWORD) -db-host=$(AWS_DB_HOST) -db-port=$(AWS_DB_PORT) -db-name=$(AWS_DB_NAME) -token-expiration=$(AWS_TOKEN_EXPIRATION) -port=$(AWS_SERVER_PORT)

# Cross compile for Linux
build-linux:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BINARY_UNIX) -v

# Download the dependencies pinned in go.mod/go.sum. This deliberately does not
# upgrade anything: use `go get <module>@<version>` for a deliberate bump.
deps:
	$(GOCMD) mod download

# Build the public container image: $(IMAGE):$(TAG). VERSION is stamped into
# the binary and the OCI version label; TAG only names the image.
docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(TAG) .

# Push the public image. Requires a prior `docker login $(REGISTRY)`
# (for GHCR: a token with write:packages).
docker-push:
	docker push $(IMAGE):$(TAG)

# Run Docker container
docker-run:
	docker run -p $(SERVER_PORT):$(SERVER_PORT) \
		-e DB_USER=$(DB_USER) \
		-e DB_PASSWORD=$(DB_PASSWORD) \
		-e DB_HOST=$(DB_HOST) \
		-e DB_PORT=$(DB_PORT) \
		-e DB_NAME=$(DB_NAME) \
		-e TOKEN_EXPIRATION=$(TOKEN_EXPIRATION) \
		-e SERVER_PORT=$(SERVER_PORT) \
		$(DOCKER_IMAGE)

# Docker Compose management
#
# These drive the root docker-compose.yml (the single-service HTTP stack).
# The full management plane — UI + API + MySQL behind a TLS edge — is the
# bundle in deploy/compose/; drive that one with its own overlay pair
# (see docs/deployment-compose.md), not with these targets.
docker-compose-up:
	docker compose up -d

docker-compose-down:
	docker compose down

docker-compose-logs:
	docker compose logs -f

# SSL certificate management
generate-ssl-certs:
	@echo "Generating SSL certificates..."
	@chmod +x scripts/ssl/generate-dev-certs.sh
	@scripts/ssl/generate-dev-certs.sh || (echo "Advanced cert generation failed, trying simple method..." && chmod +x scripts/ssl/generate-simple-certs.sh && scripts/ssl/generate-simple-certs.sh)

generate-simple-ssl-certs:
	@echo "Generating simple SSL certificates..."
	@chmod +x scripts/ssl/generate-simple-certs.sh
	@scripts/ssl/generate-simple-certs.sh

update-k8s-ssl-secret:
	@echo "Updating Kubernetes SSL secret..."
	@chmod +x scripts/ssl/update-k8s-ssl-secret.sh
	@scripts/ssl/update-k8s-ssl-secret.sh

# Build the image under the local name the Kubernetes manifests expect.
# (Public releases are built and pushed with docker-build / docker-push above.)
build-image:
	docker build --build-arg VERSION=$(VERSION) -t oam-loxilb:latest .

# Print the version this tree builds as (scripts and CI can consume it).
version:
	@echo $(VERSION)

.PHONY: all build clean run run-https run-ssl run-aws test build-linux deps \
	version docker-build docker-push docker-run build-image \
	docker-compose-up docker-compose-down docker-compose-logs \
	generate-ssl-certs generate-simple-ssl-certs update-k8s-ssl-secret
