#!/bin/sh

# Admin Reset Helper Script
# This script provides a convenient way to reset admin credentials.
# POSIX sh (no bashisms): the OAM runtime image is Alpine, which has no bash.

set -e

# DB connection — sourced from the canonical DB_* env family (the same surface
# the reset_admin binary reads). No credential defaults ship here: DB_PASSWORD
# must come from the environment (the binary aborts if it is unset).
DB_USER="${DB_USER:-oamuser}"
DB_PASSWORD="${DB_PASSWORD:-}"
DB_HOST="${DB_HOST:-127.0.0.1}"
DB_PORT="${DB_PORT:-5432}"
DB_NAME="${DB_NAME:-loxioam}"
SSL_OPTION="${SSL_OPTION:-false}"

# Status message helpers (plain output — portable across sh implementations).
print_info() {
    echo "ℹ️  $1"
}

print_warning() {
    echo "⚠️  $1"
}

print_error() {
    echo "❌ $1"
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
        if command -v go >/dev/null 2>&1; then
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
    print_warning "This will reset the admin account to its bootstrap credentials:"
    echo "  Username: admin"
    echo "  Password: (the value of OAM_DEFAULT_ADMIN_PASSWORD)"
    echo "  Email: admin@oam-loxilb.local"
    echo ""
    print_warning "All existing admin sessions will be invalidated!"
    echo ""
    printf "Are you sure you want to continue? (yes/no): "
    read -r confirmation

    if [ "$confirmation" != "yes" ]; then
        print_info "Reset cancelled"
        exit 0
    fi
    CONFIRM_FLAG="--confirm"
fi

# Build command with database connection parameters. Omit --db-password when it
# is not set in the environment so the binary can read DB_PASSWORD (or its legacy
# OAM_DB_PASSWORD alias) itself and abort with a clear error if neither is set.
CMD="$RESET_ADMIN_CMD \
    --db-user=$DB_USER \
    --db-host=$DB_HOST \
    --db-port=$DB_PORT \
    --db-name=$DB_NAME \
    --ssl-option=$SSL_OPTION \
    $CONFIRM_FLAG"

if [ -n "$DB_PASSWORD" ]; then
    CMD="$CMD --db-password=$DB_PASSWORD"
fi

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
eval "$CMD"

echo ""
print_info "Reset completed successfully!"
echo ""
