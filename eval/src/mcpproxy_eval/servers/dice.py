"""Dice Rolling MCP Server with Deterministic Responses"""

import random
from typing import Literal
from fastmcp import FastMCP

# Set seed for deterministic responses
random.seed(42)

mcp = FastMCP("Dice Roller 🎲")

@mcp.tool
def roll_dice(
    sides: int = 6,
    count: int = 1,
    modifier: int = 0
) -> dict[str, int | list[int]]:
    """Roll dice with specified number of sides
    
    Args:
        sides: Number of sides on each die (default: 6)
        count: Number of dice to roll (default: 1)
        modifier: Modifier to add to total (default: 0)
    
    Returns:
        Dictionary with individual rolls, total, and modified total
    """
    if sides < 2:
        raise ValueError("Dice must have at least 2 sides")
    if count < 1:
        raise ValueError("Must roll at least 1 die")
    if count > 20:
        raise ValueError("Cannot roll more than 20 dice at once")
    
    # Deterministic rolls based on input parameters
    rolls = []
    for i in range(count):
        # Use a deterministic formula based on sides, count, and position
        roll = ((sides * count * (i + 1)) % sides) + 1
        rolls.append(roll)
    
    total = sum(rolls)
    modified_total = total + modifier
    
    return {
        "rolls": rolls,
        "total": total,
        "modifier": modifier,
        "modified_total": modified_total,
        "dice_notation": f"{count}d{sides}{'+' + str(modifier) if modifier > 0 else str(modifier) if modifier < 0 else ''}"
    }

@mcp.tool
def roll_advantage() -> dict[str, int | list[int]]:
    """Roll with advantage (roll twice, take higher)
    
    Returns:
        Dictionary with both rolls and the advantage result
    """
    roll1 = 15  # Deterministic values
    roll2 = 12
    advantage = max(roll1, roll2)
    
    return {
        "roll1": roll1,
        "roll2": roll2,
        "advantage": advantage,
        "type": "advantage"
    }

@mcp.tool
def roll_disadvantage() -> dict[str, int | list[int]]:
    """Roll with disadvantage (roll twice, take lower)
    
    Returns:
        Dictionary with both rolls and the disadvantage result
    """
    roll1 = 8   # Deterministic values
    roll2 = 14
    disadvantage = min(roll1, roll2)
    
    return {
        "roll1": roll1,
        "roll2": roll2,
        "disadvantage": disadvantage,
        "type": "disadvantage"
    }

@mcp.tool
def dice_stats(sides: int = 6) -> dict[str, float]:
    """Get statistical information about a die
    
    Args:
        sides: Number of sides on the die
        
    Returns:
        Dictionary with statistical information
    """
    if sides < 2:
        raise ValueError("Dice must have at least 2 sides")
    
    return {
        "sides": sides,
        "min_value": 1,
        "max_value": sides,
        "average": (sides + 1) / 2,
        "total_combinations": sides
    }

def main() -> None:
    """Run the dice MCP server"""
    mcp.run()

if __name__ == "__main__":
    main() 