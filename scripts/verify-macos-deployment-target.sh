#!/bin/bash

# Verify macOS deployment target for the built binary
# This script helps verify that the macOS compatibility fix is working

set -e

BINARY_PATH="./mcpproxy"

if [ ! -f "$BINARY_PATH" ]; then
    echo "❌ Binary not found at $BINARY_PATH"
    echo "Run 'go build ./cmd/mcpproxy' first"
    exit 1
fi

echo "🔍 Checking macOS deployment target for: $BINARY_PATH"
echo ""

# Check for LC_VERSION_MIN_MACOSX (older SDKs)
echo "=== Checking LC_VERSION_MIN_MACOSX ==="
if otool -l "$BINARY_PATH" | grep -A2 LC_VERSION_MIN_MACOSX; then
    echo "✅ Found LC_VERSION_MIN_MACOSX"
else
    echo "ℹ️  No LC_VERSION_MIN_MACOSX found (normal for newer SDKs)"
fi

echo ""

# Check for LC_BUILD_VERSION (newer SDKs) 
echo "=== Checking LC_BUILD_VERSION ==="
if otool -l "$BINARY_PATH" | grep -A3 LC_BUILD_VERSION; then
    echo "✅ Found LC_BUILD_VERSION"
else
    echo "ℹ️  No LC_BUILD_VERSION found"
fi

echo ""

# Extract and display minimum OS version
echo "=== Summary ==="
echo "Extracting minimum macOS version requirement..."

# Try to extract version from either format
MIN_VERSION=$(otool -l "$BINARY_PATH" | grep -A2 -E "(LC_VERSION_MIN_MACOSX|LC_BUILD_VERSION)" | grep -E "(version|minos)" | head -1 | awk '{print $2}' || echo "unknown")

if [ "$MIN_VERSION" != "unknown" ] && [ -n "$MIN_VERSION" ]; then
    echo "✅ Minimum macOS version: $MIN_VERSION"
    
    # Convert to readable format (e.g., 786432 -> 12.0)
    if [[ "$MIN_VERSION" =~ ^[0-9]+$ ]]; then
        MAJOR=$((MIN_VERSION >> 16))
        MINOR=$(((MIN_VERSION >> 8) & 0xFF))
        PATCH=$((MIN_VERSION & 0xFF))
        READABLE="${MAJOR}.${MINOR}"
        if [ $PATCH -ne 0 ]; then
            READABLE="${READABLE}.${PATCH}"
        fi
        echo "✅ Readable version: macOS $READABLE"
        
        # Check if it's 12.0 or lower (what we want)
        if [ $MAJOR -le 12 ]; then
            echo "✅ PASS: Binary should work on macOS Sonoma (14.x) and newer"
        else
            echo "❌ FAIL: Binary requires macOS $MAJOR+ which won't work on older systems"
        fi
    else
        echo "✅ Raw version string: $MIN_VERSION"
    fi
else
    echo "❌ Could not determine minimum macOS version"
    echo "Full otool output:"
    otool -l "$BINARY_PATH" | grep -A5 -E "(LC_VERSION_MIN_MACOSX|LC_BUILD_VERSION)"
fi

echo ""
echo "=== Test Build Command ==="
echo "To test locally with deployment target:"
echo 'env CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \'
echo 'MACOSX_DEPLOYMENT_TARGET=12.0 \'
echo 'CGO_CFLAGS="-mmacosx-version-min=12.0" \'
echo 'CGO_LDFLAGS="-mmacosx-version-min=12.0" \'
echo 'go build -o mcpproxy-test ./cmd/mcpproxy'
echo ""
echo "Then run: ./scripts/verify-macos-deployment-target.sh"
