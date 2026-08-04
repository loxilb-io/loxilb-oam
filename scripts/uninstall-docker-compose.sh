#!/bin/bash

# Uninstall script for OAM-LoxiLB Docker Compose deployment
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_header() {
    echo -e "${BLUE}[UNINSTALL]${NC} $1"
}

print_header "OAM-LoxiLB Docker Compose Uninstallation"

print_status "Stopping and removing the deployment..."
if docker compose down -v 2>/dev/null; then
    print_status "Deployment stopped and removed"
else
    print_warning "No deployment found or already stopped"
fi

print_status "Removing loxilb-oam image..."
if docker rmi loxilb-oam:latest 2>/dev/null; then
    print_status "loxilb-oam:latest image removed"
else
    print_warning "loxilb-oam:latest image not found"
fi

print_status ""
print_header "Docker Compose uninstallation completed"
print_status ""
print_status "Optional cleanup commands:"
print_status "  Remove all unused Docker resources: docker system prune -a"
print_status "  Remove SSL certificates: rm -rf ssl/"
print_status "  Remove logs: rm -rf logs/"