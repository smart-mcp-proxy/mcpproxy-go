"""Jokes MCP Server with Deterministic Responses"""

from fastmcp import FastMCP

mcp = FastMCP("Joke Generator 😂")

JOKES = [
    {"id": 1, "category": "programming", "joke": "Why do programmers prefer dark mode?", "punchline": "Because light attracts bugs!"},
    {"id": 2, "category": "programming", "joke": "How many programmers does it take to change a light bulb?", "punchline": "None. That's a hardware problem."},
    {"id": 3, "category": "dad", "joke": "I'm reading a book about anti-gravity.", "punchline": "It's impossible to put down!"},
    {"id": 4, "category": "dad", "joke": "Why don't scientists trust atoms?", "punchline": "Because they make up everything!"},
    {"id": 5, "category": "knock-knock", "joke": "Knock knock. Who's there? Interrupting cow.", "punchline": "Interrupting cow w-- MOO!"}
]

@mcp.tool
def get_random_joke() -> dict[str, str | int]:
    """Get a random joke (deterministic for testing)"""
    return JOKES[2]  # Always return the same joke for deterministic testing

@mcp.tool
def get_joke_by_category(category: str) -> dict[str, str | int]:
    """Get a joke from specific category"""
    for joke in JOKES:
        if joke["category"].lower() == category.lower():
            return joke
    return {"error": f"No jokes found for category: {category}", "available_categories": ["programming", "dad", "knock-knock"]}

@mcp.tool
def get_all_jokes() -> dict[str, list]:
    """Get all available jokes"""
    return {"jokes": JOKES, "total_count": len(JOKES)}

def main() -> None:
    mcp.run()

if __name__ == "__main__":
    main() 