#!/bin/bash

# Admin Reset Helper Script
# This script provides a convenient way to reset admin credentials

set -e

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Default values
DB_USER="${DB_USER:-netlox}"
DB_PASSWORD="${DB_PASSWORD:-r00tr00t}"
DB_HOST="${DB_HOST:-127.0.0.1}"
DB_PORT="${DB_PORT:-3306}"
DB_NAME="${DB_NAME:-loxioam}"
SSL_OPTION="${SSL_OPTION:-false}"

# Function to print colored messages
print_info() {
    echo -e "${GREEN}ℹ️  $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

# Print header
echo ""
echo "======================================"
echo "   OAM-LoxiLB Admin Reset Tool"
echo "======================================"
echo ""

# Check if running in Docker container
if [ -f "/.dockerenv" ]; then
    RESET_ADMIN_CMD="./reset_admin"
    print_info "Running inside Docker container"
else
    # Check if binary exists
    if [ -f "./reset_admin" ]; then
        RESET_ADMIN_CMD="./reset_admin"
    elif [ -f "$(dirname "$0")/../reset_admin" ]; then
        RESET_ADMIN_CMD="$(dirname "$0")/../reset_admin"
    else
        # Try using go run
        if command -v go &> /dev/null; then
            RESET_ADMIN_CMD="go run cmd/reset_admin/main.go"
            print_info "Using 'go run' to execute reset tool"
        else
            print_error "Cannot find reset_admin binary or Go compiler"
            exit 1
        fi
    fi
fi

# Parse command line arguments
CONFIRM_FLAG=""
for arg in "$@"; do
    if [ "$arg" = "--confirm" ] || [ "$arg" = "-y" ]; then
        CONFIRM_FLAG="--confirm"
    fi
done

# If no confirmation flag, show warning and prompt
if [ -z "$CONFIRM_FLAG" ]; then
    print_warning "This will reset the admin account to default credentials:"
    echo "  Username: admin"
    echo "  Password: AdminNetlox132!"
    echo "  Email: admin@oam-loxilb.local"
    echo ""
    print_warning "All existing admin sessions will be invalidated!"
    echo ""
    read -p "Are you sure you want to continue? (yes/no): " confirmation
    
    if [ "$confirmation" != "yes" ]; then
        print_info "Reset cancelled"
        exit 0
    fi
    CONFIRM_FLAG="--confirm"
fi

# Build command with database connection parameters
CMD="$RESET_ADMIN_CMD \
    --db-user=$DB_USER \
    --db-password=$DB_PASSWORD \
    --db-host=$DB_HOST \
    --db-port=$DB_PORT \
    --db-name=$DB_NAME \
    --ssl-option=$SSL_OPTION \
    $CONFIRM_FLAG"

# Add SSL parameters if SSL is enabled
if [ "$SSL_OPTION" = "true" ]; then
    if [ -n "$SSL_CA_CERT_FILE" ]; then
        CMD="$CMD --ssl-ca-cert-file=$SSL_CA_CERT_FILE"
    fi
    if [ -n "$SSL_CA_CLIENT_CERT_FILE" ]; then
        CMD="$CMD --ssl-ca-client-cert-file=$SSL_CA_CLIENT_CERT_FILE"
    fi
    if [ -n "$SSL_CA_CLIENT_KEY_FILE" ]; then
        CMD="$CMD --ssl-ca-client-key-file=$SSL_CA_CLIENT_KEY_FILE"
    fi
fi

# Execute the reset command
print_info "Executing reset..."
echo ""
eval $CMD

echo ""
print_info "Reset completed successfully!"
echo ""
