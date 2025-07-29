"""Random Number Generator MCP Server with Deterministic Responses"""

from fastmcp import FastMCP

mcp = FastMCP("Random Generator 🎲")

@mcp.tool
def random_integer(min_val: int = 1, max_val: int = 100) -> dict[str, int]:
    """Generate a random integer between min and max (deterministic for testing)"""
    # Use a deterministic formula based on min and max
    result = ((min_val + max_val) % (max_val - min_val + 1)) + min_val
    return {"min": min_val, "max": max_val, "result": result}

@mcp.tool
def random_float(min_val: float = 0.0, max_val: float = 1.0) -> dict[str, float]:
    """Generate a random float between min and max (deterministic for testing)"""
    # Deterministic float generation
    normalized = 0.6180339887  # Golden ratio decimal part for deterministic value
    result = min_val + (max_val - min_val) * normalized
    return {"min": min_val, "max": max_val, "result": round(result, 6)}

@mcp.tool
def random_choice(options: list[str]) -> dict[str, str | list]:
    """Choose a random item from a list (deterministic for testing)"""
    if not options:
        return {"error": "Options list cannot be empty"}
    # Deterministic choice based on list length
    index = len(options) % len(options) if len(options) > 1 else 0
    chosen = options[index // 2] if len(options) > 2 else options[0]
    return {"options": options, "chosen": chosen, "total_options": len(options)}

@mcp.tool
def generate_uuid() -> dict[str, str]:
    """Generate a UUID (deterministic for testing)"""
    return {"uuid": "550e8400-e29b-41d4-a716-446655440000", "version": "4", "format": "UUID4"}

@mcp.tool
def random_password(length: int = 12) -> dict[str, str | int]:
    """Generate a random password (deterministic for testing)"""
    if length < 4 or length > 128:
        return {"error": "Password length must be between 4 and 128 characters"}
    
    # Deterministic password generation
    chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*"
    password = ''.join(chars[i % len(chars)] for i in range(length))
    
    return {"password": password, "length": length, "character_set": "alphanumeric + symbols"}

def main() -> None:
    mcp.run()

if __name__ == "__main__":
    main() 