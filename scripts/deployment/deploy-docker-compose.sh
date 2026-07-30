#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Check prerequisites
print_status "Checking prerequisites..."

if ! command_exists docker; then
    print_error "Docker is not installed. Please install Docker first."
    exit 1
fi

if ! docker compose version >/dev/null 2>&1; then
    print_error "Docker Compose (v2) is not available. Please install the Docker Compose plugin first."
    exit 1
fi

# Check if Docker is running
if ! docker info >/dev/null 2>&1; then
    print_error "Docker is not running. Please start Docker first."
    exit 1
fi

print_status "Prerequisites check passed"

# Build the application
print_status "Building OAM-LoxiLB application..."
docker build -t oam-loxilb:latest .

if [ $? -ne 0 ]; then
    print_error "Failed to build application"
    exit 1
fi

print_status "Application built successfully"

# Deploy with Docker Compose
print_status "Deploying with Docker Compose..."
docker compose up -d

if [ $? -ne 0 ]; then
    print_error "Failed to deploy with Docker Compose"
    exit 1
fi

# Wait for services to be ready
print_status "Waiting for services to be ready..."
sleep 30

# Check if services are running
print_status "Checking service status..."
docker compose ps

# Test application health
print_status "Testing application health..."
max_attempts=30
attempt=1

while [ $attempt -le $max_attempts ]; do
    if curl -s http://localhost:8080/oam/health >/dev/null 2>&1; then
        print_status "Application is healthy and ready"
        break
    else
        print_warning "Attempt $attempt/$max_attempts: Application not ready yet, waiting..."
        sleep 10
        attempt=$((attempt + 1))
    fi
done

if [ $attempt -gt $max_attempts ]; then
    print_error "Application failed to start within expected time"
    print_error "Check logs with: docker compose logs"
    exit 1
fi

print_status "Deployment completed successfully"
print_status ""
print_status "Access URLs:"
print_status "  Application: http://localhost:8080"
print_status "  Health Check: http://localhost:8080/oam/health"
print_status "  API Documentation: http://localhost:8080/oam/swagger/index.html"
print_status ""
print_status "Default credentials:"
print_status "  Username: admin"
print_status "  Password: set via OAM_DEFAULT_ADMIN_PASSWORD (hashed in database)"
print_status ""
print_status "Useful commands:"
print_status "  View logs: docker compose logs -f"
print_status "  Stop services: docker compose down"
print_status "  Restart services: docker compose restart"