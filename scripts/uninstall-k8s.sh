#!/bin/bash

# Uninstall script for OAM-LoxiLB Kubernetes deployment
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

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_header() {
    echo -e "${BLUE}[UNINSTALL]${NC} $1"
}

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    print_error "kubectl is not installed or not in PATH"
    exit 1
fi

print_header "OAM-LoxiLB Kubernetes Uninstallation"

# Uninstall development deployment
print_status "Uninstalling development deployment..."
if kubectl delete -k k8s/overlays/development 2>/dev/null; then
    print_status "Development deployment uninstalled"
else
    print_warning "Development deployment not found or already uninstalled"
fi

# Uninstall production deployment  
print_status "Uninstalling production deployment..."
if kubectl delete -k k8s/overlays/production 2>/dev/null; then
    print_status "Production deployment uninstalled"
else
    print_warning "Production deployment not found or already uninstalled"
fi

# Uninstall base deployment
print_status "Uninstalling base deployment..."
if kubectl delete -k k8s/base 2>/dev/null; then
    print_status "Base deployment uninstalled"
else
    print_warning "Base deployment not found or already uninstalled"
fi

# Remove namespaces
print_status "Removing namespaces..."

if kubectl delete namespace oam-loxilb 2>/dev/null; then
    print_status "Development namespace removed"
else
    print_warning "Development namespace not found"
fi

if kubectl delete namespace oam-loxilb 2>/dev/null; then
    print_status "Production namespace removed"
else
    print_warning "Production namespace not found"
fi

if kubectl delete namespace oam-loxilb 2>/dev/null; then
    print_status "Base namespace removed"
else
    print_warning "Base namespace not found"
fi

# Check for persistent volumes
print_status "Checking for persistent volumes..."
PVS=$(kubectl get pv -o jsonpath='{.items[?(@.spec.claimRef.name=="mysql-pvc")].metadata.name}' 2>/dev/null || true)
if [ -n "$PVS" ]; then
    print_warning "Found persistent volumes that may need manual cleanup:"
    for pv in $PVS; do
        print_warning "  - $pv"
    done
    print_warning "To remove: kubectl delete pv $PVS"
fi

print_status ""
print_header "Kubernetes uninstallation completed"
print_status ""
print_status "Optional cleanup commands:"
print_status "  Check remaining resources: kubectl get all --all-namespaces"
print_status "  Remove persistent volumes: kubectl get pv"
print_status "  Remove images from nodes: docker rmi oam-loxilb:* (on each node)"