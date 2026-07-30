#!/bin/bash

# Generate simple SSL certificates for OAM-LoxiLB (fallback version)
# This script creates basic self-signed certificates

set -e

CERT_DIR="./ssl/dev_certs"
DOMAIN="oam-loxilb.local"
DAYS=365

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

# Check if openssl is installed
if ! command -v openssl >/dev/null 2>&1; then
    print_error "OpenSSL is not installed. Please install OpenSSL first."
    exit 1
fi

# Create certificate directory
mkdir -p "$CERT_DIR"

print_status "Generating simple SSL certificates (fallback method)..."
print_status "Domain: $DOMAIN"
print_status "Certificate directory: $CERT_DIR"
print_status "Valid for: $DAYS days"

# Generate CA private key
print_status "Generating CA private key..."
openssl genrsa -out "$CERT_DIR/ca.key" 2048

# Generate CA certificate
print_status "Generating CA certificate..."
openssl req -new -x509 -key "$CERT_DIR/ca.key" -out "$CERT_DIR/ca.crt" -days $DAYS -subj "/C=US/ST=CA/L=San Francisco/O=OAM-LoxiLB Dev/OU=Development/CN=OAM-LoxiLB Dev CA"

# Generate server private key
print_status "Generating server private key..."
openssl genrsa -out "$CERT_DIR/server.key" 2048

# Generate server certificate (self-signed, simple version)
print_status "Generating server certificate..."
openssl req -new -x509 -key "$CERT_DIR/server.key" -out "$CERT_DIR/server.crt" -days $DAYS -subj "/C=US/ST=CA/L=San Francisco/O=OAM-LoxiLB/OU=Application/CN=$DOMAIN"

# Set appropriate permissions
chmod 600 "$CERT_DIR/ca.key" "$CERT_DIR/server.key"
chmod 644 "$CERT_DIR/ca.crt" "$CERT_DIR/server.crt"

print_status "Simple SSL certificates generated successfully!"
print_status ""
print_status "Certificate files created:"
print_status "  CA Certificate: $CERT_DIR/ca.crt"
print_status "  CA Private Key: $CERT_DIR/ca.key"
print_status "  Server Certificate: $CERT_DIR/server.crt"
print_status "  Server Private Key: $CERT_DIR/server.key"
print_status ""
print_warning "These are basic self-signed certificates for development only!"
print_warning "For production, use certificates from a trusted CA."
print_status ""
print_status "Add to /etc/hosts for local testing:"
print_status "  127.0.0.1 $DOMAIN"