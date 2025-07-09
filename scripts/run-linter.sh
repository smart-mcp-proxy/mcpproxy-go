#!/bin/bash
# Description: This script is for running golangci-lint linter locally
#
# How to use from project root:
#   ./scripts/run-linter.sh
#
# Referenced by:
#   n/a
#
set -euo pipefail

GOLANGCI_LINT_VERSION="v1.59.1"

# Function to check if a command exists
command_exists() {
    command -v "$1" &> /dev/null
}

# Check if golangci-lint is installed
if ! command_exists golangci-lint; then
    echo "golangci-lint is not installed. Please install it by following the instructions at https://golangci-lint.run/usage/install/"
    exit 1
fi

echo "Running golangci-lint..."
golangci-lint run ./... 