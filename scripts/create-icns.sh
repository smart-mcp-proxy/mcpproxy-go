#!/bin/bash
set -e

# Create .icns file from PNG icons for macOS app bundle
ICONS_DIR="assets/icons"
OUTPUT_ICNS="assets/mcpproxy.icns"

if [[ "$OSTYPE" == "darwin"* ]]; then
    # Create iconset directory
    ICONSET_DIR="mcpproxy.iconset"
    mkdir -p "$ICONSET_DIR"
    
    # Copy available PNG files to iconset with proper naming
    if [ -f "$ICONS_DIR/icon-16.png" ]; then
        cp "$ICONS_DIR/icon-16.png" "$ICONSET_DIR/icon_16x16.png"
    fi
    
    if [ -f "$ICONS_DIR/icon-32.png" ]; then
        cp "$ICONS_DIR/icon-32.png" "$ICONSET_DIR/icon_16x16@2x.png"
        cp "$ICONS_DIR/icon-32.png" "$ICONSET_DIR/icon_32x32.png"
    fi
    
    if [ -f "$ICONS_DIR/icon-64.png" ]; then
        cp "$ICONS_DIR/icon-64.png" "$ICONSET_DIR/icon_32x32@2x.png"
    fi
    
    # Use proper 128px icon or fallback to 64px
    if [ -f "$ICONS_DIR/icon-128.png" ]; then
        cp "$ICONS_DIR/icon-128.png" "$ICONSET_DIR/icon_128x128.png"
    elif [ -f "$ICONS_DIR/icon-64.png" ]; then
        cp "$ICONS_DIR/icon-64.png" "$ICONSET_DIR/icon_128x128.png"
    fi
    
    # Add higher resolution icons for Retina displays
    if [ -f "$ICONS_DIR/icon-256.png" ]; then
        cp "$ICONS_DIR/icon-256.png" "$ICONSET_DIR/icon_128x128@2x.png"
        cp "$ICONS_DIR/icon-256.png" "$ICONSET_DIR/icon_256x256.png"
    fi
    
    if [ -f "$ICONS_DIR/icon-512.png" ]; then
        cp "$ICONS_DIR/icon-512.png" "$ICONSET_DIR/icon_256x256@2x.png"
        cp "$ICONS_DIR/icon-512.png" "$ICONSET_DIR/icon_512x512.png"
    fi
    
    # Create .icns file
    iconutil -c icns "$ICONSET_DIR" -o "$OUTPUT_ICNS"
    
    # Clean up
    rm -rf "$ICONSET_DIR"
    
    echo "Created $OUTPUT_ICNS successfully"
else
    echo "Skipping .icns creation (not on macOS)"
fi 