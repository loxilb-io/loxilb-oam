#!/bin/bash

# Update Kubernetes SSL secret with generated certificates
# This script updates the oam-loxilb-ssl-certs secret in Kubernetes

set -e

CERT_DIR="./ssl/dev_certs"
NAMESPACE="oam-loxilb"
SECRET_NAME="oam-loxilb-ssl-certs"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
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

# Check if kubectl is installed
if ! command -v kubectl >/dev/null 2>&1; then
    print_error "kubectl is not installed. Please install kubectl first."
    exit 1
fi

# Check if certificates exist
if [ ! -f "$CERT_DIR/server.crt" ] || [ ! -f "$CERT_DIR/server.key" ]; then
    print_error "SSL certificates not found in $CERT_DIR"
    print_error "Please run './scripts/ssl/generate-dev-certs.sh' first"
    exit 1
fi

print_status "Updating Kubernetes SSL secret..."
print_status "Namespace: $NAMESPACE"
print_status "Secret: $SECRET_NAME"
print_status "Certificate directory: $CERT_DIR"

# Check if namespace exists
if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
    print_warning "Namespace $NAMESPACE does not exist. Creating it..."
    kubectl create namespace "$NAMESPACE"
fi

# Delete existing secret if it exists
if kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
    print_status "Deleting existing secret..."
    kubectl delete secret "$SECRET_NAME" -n "$NAMESPACE"
fi

# Create new secret
print_status "Creating new SSL secret..."
kubectl create secret tls "$SECRET_NAME" \
    --cert="$CERT_DIR/server.crt" \
    --key="$CERT_DIR/server.key" \
    --namespace="$NAMESPACE"

# Label the secret
kubectl label secret "$SECRET_NAME" -n "$NAMESPACE" \
    app.kubernetes.io/name=oam-loxilb \
    app.kubernetes.io/component=ssl-certificates

print_status "SSL secret updated successfully!"
print_status ""
print_status "Verify the secret:"
print_status "  kubectl describe secret $SECRET_NAME -n $NAMESPACE"
print_status ""
print_status "The secret contains:"
print_status "  tls.crt - Server certificate"
print_status "  tls.key - Server private key"