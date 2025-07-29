"""Morse Code MCP Server with Deterministic Responses"""

from fastmcp import FastMCP

mcp = FastMCP("Morse Code Translator 📡")

# Morse code mapping
MORSE_CODE = {
    'A': '.-', 'B': '-...', 'C': '-.-.', 'D': '-..', 'E': '.', 'F': '..-.',
    'G': '--.', 'H': '....', 'I': '..', 'J': '.---', 'K': '-.-', 'L': '.-..',
    'M': '--', 'N': '-.', 'O': '---', 'P': '.--.', 'Q': '--.-', 'R': '.-.',
    'S': '...', 'T': '-', 'U': '..-', 'V': '...-', 'W': '.--', 'X': '-..-',
    'Y': '-.--', 'Z': '--..', '1': '.----', '2': '..---', '3': '...--',
    '4': '....-', '5': '.....', '6': '-....', '7': '--...', '8': '---..',
    '9': '----.', '0': '-----', ' ': '/', '.': '.-.-.-', ',': '--..--',
    '?': '..--..', "'": '.----.', '!': '-.-.--', '/': '-..-.', '(': '-.--.',
    ')': '-.--.-', '&': '.-...', ':': '---...', ';': '-.-.-.', '=': '-...-',
    '+': '.-.-.', '-': '-....-', '_': '..--.-', '"': '.-..-.', '$': '...-..-',
    '@': '.--.-.'
}

# Reverse mapping for decoding
REVERSE_MORSE_CODE = {v: k for k, v in MORSE_CODE.items()}

@mcp.tool
def text_to_morse(text: str) -> dict[str, str]:
    """Convert text to Morse code
    
    Args:
        text: Text to convert to Morse code
        
    Returns:
        Dictionary with original text and Morse code
    """
    if not text:
        return {
            "original_text": "",
            "morse_code": "",
            "error": "Empty text provided"
        }
    
    morse_result = []
    for char in text.upper():
        if char in MORSE_CODE:
            morse_result.append(MORSE_CODE[char])
        elif char == ' ':
            morse_result.append('/')
        else:
            morse_result.append('?')  # Unknown character
    
    return {
        "original_text": text,
        "morse_code": ' '.join(morse_result),
        "character_count": len(text),
        "morse_length": len(morse_result)
    }

@mcp.tool
def morse_to_text(morse_code: str) -> dict[str, str]:
    """Convert Morse code to text
    
    Args:
        morse_code: Morse code to convert to text
        
    Returns:
        Dictionary with original Morse code and decoded text
    """
    if not morse_code:
        return {
            "original_morse": "",
            "decoded_text": "",
            "error": "Empty Morse code provided"
        }
    
    # Split by spaces and decode each Morse character
    morse_chars = morse_code.strip().split(' ')
    decoded_chars = []
    
    for morse_char in morse_chars:
        if morse_char == '/':
            decoded_chars.append(' ')
        elif morse_char in REVERSE_MORSE_CODE:
            decoded_chars.append(REVERSE_MORSE_CODE[morse_char])
        elif morse_char == '':
            continue  # Skip empty strings from multiple spaces
        else:
            decoded_chars.append('?')  # Unknown Morse code
    
    return {
        "original_morse": morse_code,
        "decoded_text": ''.join(decoded_chars),
        "morse_character_count": len(morse_chars),
        "decoded_length": len(decoded_chars)
    }

@mcp.tool
def morse_info() -> dict[str, str | dict]:
    """Get information about Morse code
    
    Returns:
        Dictionary with Morse code information and reference
    """
    return {
        "description": "Morse code is a method of transmitting text using dots and dashes",
        "inventor": "Samuel Morse",
        "year_invented": "1836",
        "dot_symbol": ".",
        "dash_symbol": "-",
        "space_between_letters": "space",
        "space_between_words": "/",
        "supported_characters": len(MORSE_CODE),
        "sample_mappings": {
            "SOS": "... --- ...",
            "HELLO": ".... . .-.. .-.. ---",
            "WORLD": ".-- --- .-. .-.. -.."
        }
    }

@mcp.tool
def validate_morse(morse_code: str) -> dict[str, str | bool | list]:
    """Validate if a string is valid Morse code
    
    Args:
        morse_code: String to validate as Morse code
        
    Returns:
        Dictionary with validation results
    """
    if not morse_code:
        return {
            "input": morse_code,
            "is_valid": False,
            "error": "Empty input"
        }
    
    # Split by spaces
    morse_chars = morse_code.strip().split(' ')
    invalid_chars = []
    valid_chars = []
    
    for morse_char in morse_chars:
        if morse_char == '/' or morse_char in REVERSE_MORSE_CODE or morse_char == '':
            valid_chars.append(morse_char)
        else:
            invalid_chars.append(morse_char)
    
    is_valid = len(invalid_chars) == 0
    
    return {
        "input": morse_code,
        "is_valid": is_valid,
        "valid_characters": len(valid_chars),
        "invalid_characters": invalid_chars if invalid_chars else None,
        "total_characters": len(morse_chars)
    }

@mcp.tool
def morse_audio_timing(morse_code: str, wpm: int = 20) -> dict[str, str | float]:
    """Calculate audio timing for Morse code transmission
    
    Args:
        morse_code: Morse code to calculate timing for
        wpm: Words per minute (default: 20)
        
    Returns:
        Dictionary with timing information
    """
    if wpm <= 0:
        return {
            "error": "Words per minute must be positive"
        }
    
    # Standard timing: 1 dot = 1 unit, 1 dash = 3 units
    # Space between symbols = 1 unit, between letters = 3 units, between words = 7 units
    
    dot_duration = 1.2 / wpm  # seconds
    dash_duration = dot_duration * 3
    symbol_space = dot_duration
    letter_space = dot_duration * 3
    word_space = dot_duration * 7
    
    total_duration = 0.0
    morse_chars = morse_code.split(' ')
    
    for i, morse_char in enumerate(morse_chars):
        if morse_char == '/':
            total_duration += word_space
        else:
            for symbol in morse_char:
                if symbol == '.':
                    total_duration += dot_duration
                elif symbol == '-':
                    total_duration += dash_duration
                if symbol != morse_char[-1]:  # Not the last symbol in character
                    total_duration += symbol_space
            
            if i < len(morse_chars) - 1 and morse_chars[i + 1] != '/':
                total_duration += letter_space
    
    return {
        "morse_code": morse_code,
        "wpm": wpm,
        "dot_duration_ms": round(dot_duration * 1000, 2),
        "dash_duration_ms": round(dash_duration * 1000, 2),
        "total_duration_seconds": round(total_duration, 2),
        "total_duration_minutes": round(total_duration / 60, 2)
    }

def main() -> None:
    """Run the Morse code MCP server"""
    mcp.run()

if __name__ == "__main__":
    main() 