#!/bin/bash

# Simple SSL certificate generation for Kubernetes deployment
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
    echo -e "${BLUE}[SSL]${NC} $1"
}

# Default values
NAMESPACE="${1:-oam-loxilb}"
DOMAIN="${2:-oam-loxilb.local}"
ENVIRONMENT="${3:-development}"

print_header "Generating Simple SSL Certificates for Kubernetes"
print_status "Namespace: $NAMESPACE"
print_status "Domain: $DOMAIN"
print_status "Environment: $ENVIRONMENT"

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    print_error "kubectl is not installed or not in PATH"
    exit 1
fi

# Check if openssl is available
if ! command -v openssl &> /dev/null; then
    print_error "openssl is not installed or not in PATH"
    exit 1
fi

# Create temporary directory for certificate generation
TEMP_DIR=$(mktemp -d)
print_status "Working directory: $TEMP_DIR"

# Generate CA private key
print_status "Generating CA private key..."
openssl genrsa -out "$TEMP_DIR/ca.key" 2048

# Generate CA certificate
print_status "Generating CA certificate..."
openssl req -new -x509 -days 365 -key "$TEMP_DIR/ca.key" -out "$TEMP_DIR/ca.crt" \
    -subj "/C=US/ST=CA/L=San Francisco/O=OAM-LoxiLB/OU=Certificate Authority/CN=OAM-LoxiLB-CA"

# Generate server private key
print_status "Generating server private key..."
openssl genrsa -out "$TEMP_DIR/server.key" 2048

# Generate server certificate (self-signed, simple approach)
print_status "Generating server certificate..."
openssl req -new -x509 -days 365 -key "$TEMP_DIR/server.key" -out "$TEMP_DIR/server.crt" \
    -subj "/C=US/ST=CA/L=San Francisco/O=OAM-LoxiLB/OU=Application/CN=$DOMAIN"

# Check if namespace exists
if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
    print_warning "Namespace '$NAMESPACE' does not exist, creating it..."
    kubectl create namespace "$NAMESPACE"
fi

# Delete existing secret if it exists
if kubectl get secret ssl-certs -n "$NAMESPACE" >/dev/null 2>&1; then
    print_status "Deleting existing ssl-certs secret..."
    kubectl delete secret ssl-certs -n "$NAMESPACE"
fi

# Create Kubernetes secret with the certificates
print_status "Creating Kubernetes SSL secret..."
kubectl create secret generic ssl-certs \
    --from-file=server.crt="$TEMP_DIR/server.crt" \
    --from-file=server.key="$TEMP_DIR/server.key" \
    --from-file=ca.crt="$TEMP_DIR/ca.crt" \
    --namespace="$NAMESPACE"

# Label the secret
kubectl label secret ssl-certs -n "$NAMESPACE" \
    app=oam-loxilb \
    component=ssl \
    environment="$ENVIRONMENT"

# Verify the secret was created
if kubectl get secret ssl-certs -n "$NAMESPACE" >/dev/null 2>&1; then
    print_status "SSL secret created successfully"
else
    print_error "Failed to create SSL secret"
    exit 1
fi

# Clean up temporary directory
rm -rf "$TEMP_DIR"

print_status ""
print_header "Simple SSL Certificate Generation Complete"
print_status ""
print_status "Certificate details:"
print_status "  Secret name: ssl-certs"
print_status "  Namespace: $NAMESPACE"
print_status "  Domain: $DOMAIN"
print_status "  Valid for: 365 days"
print_status ""
print_status "Certificate files in secret:"
print_status "  server.crt - Server certificate"
print_status "  server.key - Server private key"
print_status "  ca.crt - Certificate Authority certificate"
print_status ""
print_warning "Note: These are simple self-signed certificates for development only"
print_warning "For production, use certificates from a trusted CA."
print_status ""
print_status "To view the secret:"
print_status "  kubectl get secret ssl-certs -n $NAMESPACE -o yaml"