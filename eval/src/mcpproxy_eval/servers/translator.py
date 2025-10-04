"""Translator MCP Server with Deterministic Responses"""

from fastmcp import FastMCP

mcp = FastMCP("Translator 🌍")

# Deterministic translations
TRANSLATIONS = {
    "hello": {"spanish": "hola", "french": "bonjour", "german": "hallo", "italian": "ciao"},
    "goodbye": {"spanish": "adiós", "french": "au revoir", "german": "auf wiedersehen", "italian": "arrivederci"},
    "please": {"spanish": "por favor", "french": "s'il vous plaît", "german": "bitte", "italian": "per favore"},
    "thank you": {"spanish": "gracias", "french": "merci", "german": "danke", "italian": "grazie"},
    "yes": {"spanish": "sí", "french": "oui", "german": "ja", "italian": "sì"},
    "no": {"spanish": "no", "french": "non", "german": "nein", "italian": "no"},
    "water": {"spanish": "agua", "french": "eau", "german": "wasser", "italian": "acqua"},
    "food": {"spanish": "comida", "french": "nourriture", "german": "essen", "italian": "cibo"}
}

@mcp.tool
def translate_text(text: str, target_language: str) -> dict[str, str]:
    """Translate text to target language"""
    text_lower = text.lower().strip()
    target_lower = target_language.lower().strip()
    
    if text_lower in TRANSLATIONS and target_lower in TRANSLATIONS[text_lower]:
        return {
            "original_text": text,
            "translated_text": TRANSLATIONS[text_lower][target_lower],
            "source_language": "english",
            "target_language": target_language,
            "confidence": 1.0
        }
    
    return {
        "original_text": text,
        "error": f"Translation not available for '{text}' to {target_language}",
        "available_phrases": list(TRANSLATIONS.keys()),
        "supported_languages": ["spanish", "french", "german", "italian"]
    }

@mcp.tool
def get_supported_languages() -> dict[str, list[str]]:
    """Get list of supported languages"""
    return {"supported_languages": ["spanish", "french", "german", "italian"]}

def main() -> None:
    mcp.run()

if __name__ == "__main__":
    main() 