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

# ── Container image (public releases go to GHCR) ─────────────────────────────
# The published image is $(IMAGE):$(TAG) == $(REGISTRY)/$(IMAGE_NAME):$(TAG).
# Override any part on the command line, e.g.
#   make docker-build TAG=v1.4.0
#   make docker-build IMAGE_NAME=myorg/loxilb-oam REGISTRY=docker.io
#   make docker-build docker-push TAG=v1.4.0
REGISTRY   ?= ghcr.io
IMAGE_NAME ?= loxilb-io/loxilb-oam
TAG        ?= latest
IMAGE      ?= $(REGISTRY)/$(IMAGE_NAME)
DOCKER_IMAGE ?= $(IMAGE):$(TAG)

# All target
all: test build

# Build the project
build:
	$(GOBUILD) -o $(BINARY_NAME) -v

# Run the local tests
test:
	$(GOTEST) -v ./tests/local/...

# Clean the build files
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)

# Run the application
run:
	$(GOBUILD) -o $(BINARY_NAME) -v
	./$(BINARY_NAME) -db-user=$(DB_USER) -db-password=$(DB_PASSWORD) -db-host=$(DB_HOST) -db-port=$(DB_PORT) -db-name=$(DB_NAME) -token-expiration=$(TOKEN_EXPIRATION) -port=$(SERVER_PORT) --google-redirect-url=$(GOOGLE_REDIRECT_URL) --github-redirect-url=$(GITHUB_REDIRECT_URL) --facebook-redirect-url=$(FACEBOOK_REDIRECT_URL)

run-https:
	$(GOBUILD) -o $(BINARY_NAME) -v
	nohup ./$(BINARY_NAME) -db-user=$(DB_USER) -db-password=$(DB_PASSWORD) -db-host=$(DB_HOST) -db-port=$(DB_PORT) -db-name=$(DB_NAME) -token-expiration=$(TOKEN_EXPIRATION) -port=$(SSL_SERVER_PORT) --google-redirect-url=$(GOOGLE_REDIRECT_URL) --github-redirect-url=$(GITHUB_REDIRECT_URL) --facebook-redirect-url=$(FACEBOOK_REDIRECT_URL) --enable-https=true --ssl-cert-file=./ssl/server_certs/server.crt --ssl-key-file=./ssl/server_certs/server.key > output.log 2>&1 &

run-ssl:
	$(GOBUILD) -o $(BINARY_NAME) -v
	./$(BINARY_NAME) -db-user=$(SSL_DB_USER) -db-password=$(SSL_DB_PASSWORD) -db-host=$(SSL_DB_HOST) -db-port=$(SSL_DB_PORT) -db-name=$(SSL_DB_NAME) -token-expiration=$(SSL_TOKEN_EXPIRATION) -port=$(SSL_SERVER_PORT) -ssl-option=$(SSL_OPTION) --google-redirect-url=$(GOOGLE_REDIRECT_URL) --github-redirect-url=$(GITHUB_REDIRECT_URL) --facebook-redirect-url=$(FACEBOOK_REDIRECT_URL)

run-aws:
	$(GOBUILD) -o $(BINARY_NAME) -v
	./$(BINARY_NAME) -db-user=$(AWS_DB_USER) -db-password=$(AWS_DB_PASSWORD) -db-host=$(AWS_DB_HOST) -db-port=$(AWS_DB_PORT) -db-name=$(AWS_DB_NAME) -token-expiration=$(AWS_TOKEN_EXPIRATION) -port=$(AWS_SERVER_PORT) --google-redirect-url=$(GOOGLE_REDIRECT_URL) --github-redirect-url=$(GITHUB_REDIRECT_URL) --facebook-redirect-url=$(FACEBOOK_REDIRECT_URL)

# Cross compile for Linux
build-linux:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BINARY_UNIX) -v

# Install dependencies
deps:
	$(GOGET) -u github.com/gin-gonic/gin
	$(GOGET) -u github.com/stretchr/testify
	$(GOGET) -u github.com/DATA-DOG/go-sqlmock
	$(GOGET) -u github.com/golang-jwt/jwt/v5

# Build the public container image: $(IMAGE):$(TAG)
docker-build:
	docker build --build-arg VERSION=$(TAG) -t $(IMAGE):$(TAG) .

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
		-e GOOGLE_REDIRECT_URL=$(GOOGLE_REDIRECT_URL) \
		-e GITHUB_REDIRECT_URL=$(GITHUB_REDIRECT_URL) \
		-e FACEBOOK_REDIRECT_URL=$(FACEBOOK_REDIRECT_URL) \
		$(DOCKER_IMAGE)

# Deployment targets
deploy-docker-compose:
	@echo "Deploying with Docker Compose..."
	@chmod +x scripts/deployment/deploy-docker-compose.sh
	@scripts/deployment/deploy-docker-compose.sh

deploy-k8s:
	@echo "Deploying to Kubernetes ..."
	@chmod +x scripts/deployment/deploy-kubernetes.sh
	@scripts/deployment/deploy-kubernetes.sh -e development

deploy-k8s-https:
	@echo "Deploying to Kubernetes  with HTTPS..."
	@chmod +x scripts/k8s-generate-simple-ssl-certs.sh
	@scripts/k8s-generate-simple-ssl-certs.sh oam-loxilb oam-loxilb.local development
	@echo "Waiting for SSL secret to be ready..."
	@sleep 3
	@kubectl get secret ssl-certs -n oam-loxilb >/dev/null 2>&1 && echo "SSL secret exists" || echo "SSL secret check completed"
	@chmod +x scripts/deployment/deploy-kubernetes.sh
	@scripts/deployment/deploy-kubernetes.sh -e development --https 

# Docker Compose management
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

# Fix line endings and database initialization
fix-line-endings:
	@echo "Fixing line endings for all scripts..."
	@chmod +x scripts/fix-line-endings-simple.sh
	@scripts/fix-line-endings-simple.sh

fix-database-init:
	@echo "Fixing database initialization..."
	@chmod +x scripts/fix-database-init.sh
	@scripts/fix-database-init.sh

fix-database-init-safe:
	@echo "Fixing database initialization (SAFE MODE - will drop and recreate database)..."
	@chmod +x scripts/fix-database-init.sh
	@scripts/fix-database-init.sh safe

fix-all:
	@echo "Fixing all common issues..."
	@make fix-line-endings
	@make fix-database-init-safe

# Build OAM-LoxiLB image with proper naming
build-image:
	docker build -t oam-loxilb:latest .

build-image-dev:
	docker build -t oam-loxilb:latest .

# Uninstall targets
uninstall-docker-compose:
	@echo "Uninstalling Docker Compose deployment..."
	@chmod +x scripts/uninstall-docker-compose.sh
	@scripts/uninstall-docker-compose.sh

uninstall-k8s:
	@echo "Uninstalling Kubernetes deployment..."
	@chmod +x scripts/uninstall-k8s.sh
	@scripts/uninstall-k8s.sh

uninstall-all:
	@echo "Uninstalling all deployments..."
	@make uninstall-docker-compose
	@make uninstall-k8s

.PHONY: all build clean run test build-linux deps docker-build docker-push docker-run deploy-docker-compose deploy-k8s-dev deploy-k8s-prod deploy-k8s-dev-https deploy-k8s-prod-https docker-compose-up docker-compose-down docker-compose-logs generate-ssl-certs generate-simple-ssl-certs update-k8s-ssl-secret fix-line-endings fix-database-init fix-database-init-safe fix-all build-image build-image-dev uninstall-docker-compose uninstall-k8s uninstall-all
