#!/bin/bash
# Copy docs from repo root to website/docs for Docusaurus build
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Remove old docs copy
rm -rf docs

# Copy docs from repo root
cp -r ../docs ./docs

echo "Docs copied successfully from ../docs to ./docs"
