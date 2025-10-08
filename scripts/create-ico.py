#!/usr/bin/env python3
"""
Generate a Windows .ico file from PNG icons for system tray.
This script creates icon-mono-44.ico from existing PNG assets.
"""

from PIL import Image
import os
import sys

def create_ico():
    """Create .ico file from PNG assets."""
    script_dir = os.path.dirname(os.path.abspath(__file__))
    project_root = os.path.dirname(script_dir)

    # Source PNG files (use multiple sizes for better quality)
    png_files = [
        os.path.join(project_root, 'assets/icons/icon-16.png'),
        os.path.join(project_root, 'assets/icons/icon-32.png'),
        os.path.join(project_root, 'cmd/mcpproxy-tray/icon-mono-44.png'),
    ]

    # Output ICO file
    ico_output = os.path.join(project_root, 'cmd/mcpproxy-tray/icon-mono-44.ico')

    # Check if all source files exist
    for png_file in png_files:
        if not os.path.exists(png_file):
            print(f"Error: Source file not found: {png_file}", file=sys.stderr)
            return False

    try:
        # Load all PNG images
        images = []
        for png_file in png_files:
            img = Image.open(png_file)
            # Convert to RGBA if needed
            if img.mode != 'RGBA':
                img = img.convert('RGBA')
            images.append(img)

        # Save as ICO with multiple sizes
        # Windows ICO format supports multiple resolutions in one file
        images[0].save(ico_output, format='ICO', sizes=[(16, 16), (32, 32), (44, 44)])

        print(f"✅ Successfully created {ico_output}")

        # Verify the file was created
        if os.path.exists(ico_output):
            size = os.path.getsize(ico_output)
            print(f"   File size: {size} bytes")
            return True
        else:
            print("Error: ICO file was not created", file=sys.stderr)
            return False

    except Exception as e:
        print(f"Error creating ICO file: {e}", file=sys.stderr)
        return False

if __name__ == '__main__':
    success = create_ico()
    sys.exit(0 if success else 1)
