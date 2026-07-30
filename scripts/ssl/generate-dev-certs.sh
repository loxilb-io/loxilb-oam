#!/bin/bash

# Generate development SSL certificates for OAM-LoxiLB
# This script creates self-signed certificates for development and testing

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

print_status "Generating development SSL certificates..."
print_status "Domain: $DOMAIN"
print_status "Certificate directory: $CERT_DIR"
print_status "Valid for: $DAYS days"

# Generate CA private key
print_status "Generating CA private key..."
openssl genpkey -algorithm RSA -out "$CERT_DIR/ca.key" -pkeyopt rsa_keygen_bits:2048

# Generate CA certificate
print_status "Generating CA certificate..."
openssl req -new -x509 -key "$CERT_DIR/ca.key" -out "$CERT_DIR/ca.crt" -days $DAYS -subj "/C=US/ST=CA/L=San Francisco/O=OAM-LoxiLB Dev/OU=Development/CN=OAM-LoxiLB Dev CA"

# Generate server private key
print_status "Generating server private key..."
openssl genpkey -algorithm RSA -out "$CERT_DIR/server.key" -pkeyopt rsa_keygen_bits:2048

# Generate server certificate signing request
print_status "Generating server certificate signing request..."
openssl req -new -key "$CERT_DIR/server.key" -out "$CERT_DIR/server.csr" -subj "/C=US/ST=CA/L=San Francisco/O=OAM-LoxiLB/OU=Application/CN=$DOMAIN"

# Create extensions file for SAN
cat > "$CERT_DIR/server.ext" << EOF
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = $DOMAIN
DNS.2 = localhost
DNS.3 = *.oam-loxilb.local
IP.1 = 127.0.0.1
IP.2 = ::1
EOF

# Generate server certificate
print_status "Generating server certificate..."
openssl x509 -req -in "$CERT_DIR/server.csr" -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial -out "$CERT_DIR/server.crt" -days $DAYS -extfile "$CERT_DIR/server.ext"

# Set appropriate permissions
chmod 600 "$CERT_DIR/ca.key" "$CERT_DIR/server.key"
chmod 644 "$CERT_DIR/ca.crt" "$CERT_DIR/server.crt"

# Clean up temporary files
rm -f "$CERT_DIR/server.csr" "$CERT_DIR/server.ext" "$CERT_DIR/ca.srl"

print_status "SSL certificates generated successfully!"
print_status ""
print_status "Certificate files created:"
print_status "  CA Certificate: $CERT_DIR/ca.crt"
print_status "  CA Private Key: $CERT_DIR/ca.key"
print_status "  Server Certificate: $CERT_DIR/server.crt"
print_status "  Server Private Key: $CERT_DIR/server.key"
print_status ""
print_warning "These are self-signed certificates for development only!"
print_warning "For production, use certificates from a trusted CA."
print_status ""
print_status "To trust the CA certificate:"
print_status "  macOS: sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain $CERT_DIR/ca.crt"
print_status "  Linux: sudo cp $CERT_DIR/ca.crt /usr/local/share/ca-certificates/ && sudo update-ca-certificates"
print_status ""
print_status "Add to /etc/hosts for local testing:"
print_status "  127.0.0.1 $DOMAIN"