"""Color MCP Server with Deterministic Responses"""

from fastmcp import FastMCP

mcp = FastMCP("Color Palette 🎨")

COLORS = {
    "red": {"hex": "#FF0000", "rgb": [255, 0, 0], "hsl": [0, 100, 50]},
    "green": {"hex": "#00FF00", "rgb": [0, 255, 0], "hsl": [120, 100, 50]},
    "blue": {"hex": "#0000FF", "rgb": [0, 0, 255], "hsl": [240, 100, 50]},
    "yellow": {"hex": "#FFFF00", "rgb": [255, 255, 0], "hsl": [60, 100, 50]},
    "purple": {"hex": "#800080", "rgb": [128, 0, 128], "hsl": [300, 100, 25]},
    "orange": {"hex": "#FFA500", "rgb": [255, 165, 0], "hsl": [39, 100, 50]},
    "pink": {"hex": "#FFC0CB", "rgb": [255, 192, 203], "hsl": [350, 100, 88]},
    "black": {"hex": "#000000", "rgb": [0, 0, 0], "hsl": [0, 0, 0]},
    "white": {"hex": "#FFFFFF", "rgb": [255, 255, 255], "hsl": [0, 0, 100]}
}

@mcp.tool
def get_color_info(color_name: str) -> dict[str, str | list]:
    """Get color information by name"""
    color_key = color_name.lower().strip()
    if color_key in COLORS:
        return {"name": color_name, **COLORS[color_key]}
    return {"error": f"Color '{color_name}' not found", "available_colors": list(COLORS.keys())}

@mcp.tool
def hex_to_rgb(hex_color: str) -> dict[str, str | list]:
    """Convert hex color to RGB"""
    hex_color = hex_color.lstrip('#')
    if len(hex_color) != 6:
        return {"error": "Invalid hex color format. Use #RRGGBB"}
    try:
        rgb = [int(hex_color[i:i+2], 16) for i in (0, 2, 4)]
        return {"hex": f"#{hex_color.upper()}", "rgb": rgb}
    except ValueError:
        return {"error": "Invalid hex color format"}

@mcp.tool
def rgb_to_hex(r: int, g: int, b: int) -> dict[str, str | list]:
    """Convert RGB to hex color"""
    if not all(0 <= c <= 255 for c in [r, g, b]):
        return {"error": "RGB values must be between 0 and 255"}
    hex_color = f"#{r:02X}{g:02X}{b:02X}"
    return {"rgb": [r, g, b], "hex": hex_color}

def main() -> None:
    mcp.run()

if __name__ == "__main__":
    main() 