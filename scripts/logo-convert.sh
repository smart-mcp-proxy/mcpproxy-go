#!/bin/bash
set -e

# Color icons
inkscape assets/logo.svg --export-width=16 --export-filename=assets/icons/icon-16.png
inkscape assets/logo.svg --export-width=32 --export-filename=assets/icons/icon-32.png
inkscape assets/logo.svg --export-width=64 --export-filename=assets/icons/icon-64.png
inkscape assets/logo.svg --export-width=128 --export-filename=assets/icons/icon-128.png
inkscape assets/logo.svg --export-width=256 --export-filename=assets/icons/icon-256.png
inkscape assets/logo.svg --export-width=512 --export-filename=assets/icons/icon-512.png

# Monochrome tray icon (44x44)
inkscape assets/logo.svg --export-width=44 --export-filename=internal/tray/icon-mono-44.png
convert internal/tray/icon-mono-44.png -colorspace Gray internal/tray/icon-mono-44.png
echo "✅ Generated all icon files from assets/logo.svg"