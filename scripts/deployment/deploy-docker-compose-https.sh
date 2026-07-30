#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# The HTTPS stack is the base compose file plus the HTTPS override.
COMPOSE="docker compose -f docker-compose.yml -f docker-compose.https.yml"
GENERATE_CERTS="true"

print_status()  { echo -e "${GREEN}[INFO]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[WARN]${NC} $1"; }
print_error()   { echo -e "${RED}[ERROR]${NC} $1"; }
print_header()  { echo -e "${BLUE}[DEPLOY]${NC} $1"; }

show_usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  --no-certs   Skip certificate generation (use existing certificates)"
    echo "  -h, --help   Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0             # Deploy HTTPS, generating self-signed dev certificates"
    echo "  $0 --no-certs  # Deploy HTTPS with existing certificates"
}

while [[ $# -gt 0 ]]; do
    case $1 in
        --no-certs) GENERATE_CERTS="false"; shift ;;
        -h|--help)  show_usage; exit 0 ;;
        *) print_error "Unknown option: $1"; show_usage; exit 1 ;;
    esac
done

print_header "loxilb-oam HTTPS deployment with Docker Compose"
print_status "Generate certificates: $GENERATE_CERTS"

command_exists() { command -v "$1" >/dev/null 2>&1; }

# Prerequisites
print_status "Checking prerequisites..."
if ! command_exists docker; then
    print_error "Docker is not installed. Please install Docker first."
    exit 1
fi
if ! docker compose version >/dev/null 2>&1; then
    print_error "Docker Compose (v2) is not available. Please install the Docker Compose plugin first."
    exit 1
fi
if ! docker info >/dev/null 2>&1; then
    print_error "Docker is not running. Please start Docker first."
    exit 1
fi
print_status "Prerequisites check passed"

# SSL certificates
if [ "$GENERATE_CERTS" = "true" ]; then
    print_status "Generating development SSL certificates..."
    if [ ! -f "scripts/ssl/generate-dev-certs.sh" ]; then
        print_error "Certificate generation script scripts/ssl/generate-dev-certs.sh not found."
        exit 1
    fi
    chmod +x scripts/ssl/generate-dev-certs.sh
    if ! ./scripts/ssl/generate-dev-certs.sh; then
        print_warning "Advanced certificate generation failed, trying the simple method..."
        chmod +x scripts/ssl/generate-simple-certs.sh
        ./scripts/ssl/generate-simple-certs.sh
    fi
    if [ -d "ssl/dev_certs" ]; then
        print_status "Copying certificates to ssl/server_certs for Docker Compose..."
        mkdir -p ssl/server_certs
        cp ssl/dev_certs/server.crt ssl/server_certs/
        cp ssl/dev_certs/server.key ssl/server_certs/
        cp ssl/dev_certs/ca.crt ssl/server_certs/ 2>/dev/null || true
    fi
else
    print_status "Using existing SSL certificates..."
    if [ ! -f "ssl/server_certs/server.crt" ] || [ ! -f "ssl/server_certs/server.key" ]; then
        print_error "SSL certificates not found in ssl/server_certs/."
        print_error "Run without --no-certs to generate them, or place them there first."
        exit 1
    fi
fi

# Deploy
print_status "Cleaning up existing containers and volumes..."
$COMPOSE down -v 2>/dev/null || true

print_status "Building and deploying (HTTPS)..."
$COMPOSE up -d --build

# Wait for readiness
print_status "Waiting for services to be ready..."
sleep 30
$COMPOSE ps

print_status "Testing application health..."
max_attempts=30
attempt=1
while [ $attempt -le $max_attempts ]; do
    if curl -s -k https://localhost:443/oam/health >/dev/null 2>&1; then
        print_status "Application is healthy and ready"
        break
    fi
    print_warning "Attempt $attempt/$max_attempts: application not ready yet, waiting..."
    sleep 10
    attempt=$((attempt + 1))
done

if [ $attempt -gt $max_attempts ]; then
    print_error "Application failed to start within the expected time"
    print_error "Check logs with: $COMPOSE logs"
    exit 1
fi

print_status ""
print_header "HTTPS deployment completed successfully"
print_status ""
print_status "Access URLs:"
print_status "  Application:       https://localhost:443"
print_status "  Health check:      https://localhost:443/oam/health"
print_status "  API documentation: https://localhost:443/oam/swagger/index.html"
print_status ""
print_status "Default credentials:"
print_status "  Username: admin"
print_status "  Password: set via OAM_DEFAULT_ADMIN_PASSWORD (hashed in database)"
print_status ""
print_warning "Note: self-signed certificates will trigger browser security warnings."
print_status ""
print_status "Useful commands:"
print_status "  View logs:     $COMPOSE logs -f"
print_status "  Stop services: $COMPOSE down"
print_status "  Restart:       $COMPOSE restart"
