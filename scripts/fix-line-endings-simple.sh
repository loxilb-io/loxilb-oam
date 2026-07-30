#!/bin/bash

# Simple line endings fix for Ubuntu/Linux
set -e

echo "Fixing line endings for all shell scripts..."

# Method 1: Try dos2unix first (most reliable)
if command -v dos2unix >/dev/null 2>&1; then
    echo "Using dos2unix..."
    find scripts -name "*.sh" -type f -exec dos2unix {} \; 2>/dev/null || true
else
    # Method 2: Use sed with temporary files
    echo "Using sed..."
    find scripts -name "*.sh" -type f | while read -r file; do
        echo "Fixing: $file"
        sed 's/\r$//' "$file" > "${file}.fixed" && mv "${file}.fixed" "$file"
        chmod +x "$file"
    done
fi

# Make all scripts executable
find scripts -name "*.sh" -type f -exec chmod +x {} \;

echo "Line endings fixed for all shell scripts"
echo "All scripts are now Linux/Unix compatible."