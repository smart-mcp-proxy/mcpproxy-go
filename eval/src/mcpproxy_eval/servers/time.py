"""Time MCP Server with Deterministic Responses"""

from datetime import datetime, timezone
from fastmcp import FastMCP

mcp = FastMCP("Time Service ⏰")

# Fixed deterministic time for consistent testing
FIXED_TIME = datetime(2024, 6, 15, 14, 30, 0, tzinfo=timezone.utc)

@mcp.tool
def get_current_time(timezone_name: str = "UTC") -> dict[str, str]:
    """Get current time in specified timezone"""
    # Deterministic time offsets for testing
    timezone_offsets = {
        "UTC": 0, "EST": -5, "PST": -8, "CET": 1, "JST": 9
    }
    
    if timezone_name.upper() not in timezone_offsets:
        return {
            "error": f"Timezone {timezone_name} not supported",
            "supported_timezones": list(timezone_offsets.keys())
        }
    
    offset_hours = timezone_offsets[timezone_name.upper()]
    local_time = FIXED_TIME.replace(hour=FIXED_TIME.hour + offset_hours)
    
    return {
        "timezone": timezone_name.upper(),
        "current_time": local_time.strftime("%Y-%m-%d %H:%M:%S"),
        "iso_format": local_time.isoformat(),
        "unix_timestamp": int(local_time.timestamp())
    }

@mcp.tool
def format_time(timestamp: int, format_string: str = "%Y-%m-%d %H:%M:%S") -> dict[str, str]:
    """Format a Unix timestamp"""
    dt = datetime.fromtimestamp(timestamp, tz=timezone.utc)
    return {
        "timestamp": timestamp,
        "formatted_time": dt.strftime(format_string),
        "format_used": format_string
    }

def main() -> None:
    mcp.run()

if __name__ == "__main__":
    main() 