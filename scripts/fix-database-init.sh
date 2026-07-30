#!/bin/bash

# Fix database initialization by manually running SQL scripts
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Database connection parameters
DB_USER="oamuser"
DB_PASSWORD="${DB_PASSWORD:?DB_PASSWORD must be set}"
DB_HOST="127.0.0.1"
DB_PORT="3306"
DB_NAME="loxioam"

print_status "Fixing database initialization..."
print_status "Database: $DB_NAME on $DB_HOST:$DB_PORT"

# Check if we should use safe mode (drop and recreate)
SAFE_MODE="${1:-false}"
if [ "$SAFE_MODE" = "safe" ] || [ "$SAFE_MODE" = "--safe" ]; then
    print_warning "SAFE MODE: Will drop and recreate database (data will be lost)"
    SQL_FILE="database/init/01-create-database-safe.sql"
else
    print_status "NORMAL MODE: Will try to create tables if they don't exist"
    SQL_FILE="database/init/01-create-database.sql"
fi

# Check if MySQL is accessible
if ! mysql -u"$DB_USER" -p"$DB_PASSWORD" -h"$DB_HOST" -P"$DB_PORT" -e "SELECT 1" >/dev/null 2>&1; then
    print_error "Cannot connect to MySQL. Please ensure the database is running."
    exit 1
fi

print_status "MySQL connection successful"

# Run the database creation script
if [ -f "$SQL_FILE" ]; then
    print_status "Running database schema creation using: $SQL_FILE"
    if mysql -u"$DB_USER" -p"$DB_PASSWORD" -h"$DB_HOST" -P"$DB_PORT" < "$SQL_FILE"; then
        print_status "Database schema created successfully"
    else
        print_error "Failed to create database schema. Check the SQL syntax."
        print_warning "Try running with safe mode: make fix-database-init-safe"
        exit 1
    fi
else
    print_error "Database schema file not found: $SQL_FILE"
    exit 1
fi

# Run the data insertion script
if [ -f "database/init/02-insert-dummy-data.sql" ]; then
    print_status "Running dummy data insertion..."
    mysql -u"$DB_USER" -p"$DB_PASSWORD" -h"$DB_HOST" -P"$DB_PORT" < database/init/02-insert-dummy-data.sql
    print_status "Dummy data inserted successfully"
else
    print_warning "Dummy data file not found: database/init/02-insert-dummy-data.sql"
fi

# Verify tables were created
print_status "Verifying database tables..."
TABLE_COUNT=$(mysql -u"$DB_USER" -p"$DB_PASSWORD" -h"$DB_HOST" -P"$DB_PORT" -D"$DB_NAME" -e "SHOW TABLES;" | wc -l)

if [ "$TABLE_COUNT" -gt 1 ]; then
    print_status "Database initialization successful"
    print_status "Tables created:"
    mysql -u"$DB_USER" -p"$DB_PASSWORD" -h"$DB_HOST" -P"$DB_PORT" -D"$DB_NAME" -e "SHOW TABLES;"
else
    print_error "Database initialization failed - no tables found"
    exit 1
fi

print_status "Database is now ready for use."