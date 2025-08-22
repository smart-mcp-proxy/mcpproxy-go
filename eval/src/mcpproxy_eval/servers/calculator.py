"""Calculator MCP Server with Deterministic Responses"""

import math
from fastmcp import FastMCP

mcp = FastMCP("Calculator 🧮")

@mcp.tool
def add(a: float, b: float) -> dict[str, float]:
    """Add two numbers"""
    return {"operation": "addition", "a": a, "b": b, "result": a + b}

@mcp.tool
def subtract(a: float, b: float) -> dict[str, float]:
    """Subtract two numbers"""
    return {"operation": "subtraction", "a": a, "b": b, "result": a - b}

@mcp.tool
def multiply(a: float, b: float) -> dict[str, float]:
    """Multiply two numbers"""
    return {"operation": "multiplication", "a": a, "b": b, "result": a * b}

@mcp.tool
def divide(a: float, b: float) -> dict[str, float | str]:
    """Divide two numbers"""
    if b == 0:
        return {"operation": "division", "a": a, "b": b, "error": "Division by zero"}
    return {"operation": "division", "a": a, "b": b, "result": a / b}

@mcp.tool
def power(base: float, exponent: float) -> dict[str, float]:
    """Calculate base raised to the power of exponent"""
    return {"operation": "power", "base": base, "exponent": exponent, "result": base ** exponent}

@mcp.tool
def square_root(number: float) -> dict[str, float | str]:
    """Calculate square root of a number"""
    if number < 0:
        return {"operation": "square_root", "number": number, "error": "Cannot calculate square root of negative number"}
    return {"operation": "square_root", "number": number, "result": math.sqrt(number)}

@mcp.tool
def factorial(n: int) -> dict[str, int | str]:
    """Calculate factorial of a number"""
    if n < 0:
        return {"operation": "factorial", "number": n, "error": "Cannot calculate factorial of negative number"}
    if n > 20:
        return {"operation": "factorial", "number": n, "error": "Number too large for factorial calculation"}
    return {"operation": "factorial", "number": n, "result": math.factorial(n)}

def main() -> None:
    mcp.run()

if __name__ == "__main__":
    main() 