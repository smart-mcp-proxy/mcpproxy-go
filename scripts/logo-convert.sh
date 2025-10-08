#!/bin/bash
set -e

# Color icons
inkscape assets/logo.svg --export-width=16 --export-filename=assets/icons/icon-16.png
inkscape assets/logo.svg --export-width=32 --export-filename=assets/icons/icon-32.png
inkscape assets/logo.svg --export-width=64 --export-filename=assets/icons/icon-64.png
inkscape assets/logo.svg --export-width=128 --export-filename=assets/icons/icon-128.png
inkscape assets/logo.svg --export-width=256 --export-filename=assets/icons/icon-256.png
inkscape assets/logo.svg --export-width=512 --export-filename=assets/icons/icon-512.png

# Monochrome tray icon (44x44 PNG)
inkscape assets/logo.svg --export-width=44 --export-filename=cmd/mcpproxy-tray/icon-mono-44.png
convert cmd/mcpproxy-tray/icon-mono-44.png -colorspace Gray cmd/mcpproxy-tray/icon-mono-44.png

# Create Windows ICO file from PNG assets (requires Python with Pillow)
if command -v python3 &> /dev/null; then
    echo "🔨 Generating Windows .ico file..."
    python3 scripts/create-ico.py
else
    echo "⚠️  Warning: python3 not found, skipping .ico generation"
    echo "   Install Python 3 with Pillow to generate Windows icon: pip install Pillow"
fi

echo "✅ Generated all icon files from assets/logo.svg"