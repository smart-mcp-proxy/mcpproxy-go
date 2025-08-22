"""Weather MCP Server with Deterministic Responses"""

from typing import Literal
from fastmcp import FastMCP

mcp = FastMCP("Weather Service ⛅")

# Deterministic weather data
WEATHER_DATA = {
    "new york": {
        "temperature": 72,
        "condition": "partly cloudy",
        "humidity": 65,
        "wind_speed": 8,
        "wind_direction": "NW",
        "pressure": 1013.25,
        "visibility": 10,
        "uv_index": 6
    },
    "london": {
        "temperature": 59,
        "condition": "light rain",
        "humidity": 82,
        "wind_speed": 12,
        "wind_direction": "SW",
        "pressure": 1008.5,
        "visibility": 8,
        "uv_index": 2
    },
    "tokyo": {
        "temperature": 77,
        "condition": "sunny",
        "humidity": 58,
        "wind_speed": 5,
        "wind_direction": "E",
        "pressure": 1018.7,
        "visibility": 15,
        "uv_index": 8
    },
    "paris": {
        "temperature": 64,
        "condition": "overcast",
        "humidity": 73,
        "wind_speed": 7,
        "wind_direction": "W",
        "pressure": 1011.3,
        "visibility": 12,
        "uv_index": 3
    },
    "sydney": {
        "temperature": 68,
        "condition": "clear",
        "humidity": 45,
        "wind_speed": 15,
        "wind_direction": "SE",
        "pressure": 1020.1,
        "visibility": 20,
        "uv_index": 9
    }
}

FORECAST_DATA = {
    "new york": [
        {"day": "today", "high": 75, "low": 62, "condition": "partly cloudy"},
        {"day": "tomorrow", "high": 78, "low": 65, "condition": "sunny"},
        {"day": "day_after", "high": 73, "low": 60, "condition": "thunderstorms"}
    ],
    "london": [
        {"day": "today", "high": 61, "low": 48, "condition": "light rain"},
        {"day": "tomorrow", "high": 58, "low": 45, "condition": "heavy rain"},
        {"day": "day_after", "high": 63, "low": 50, "condition": "cloudy"}
    ]
}

@mcp.tool
def get_current_weather(city: str) -> dict[str, str | int | float]:
    """Get current weather for a city
    
    Args:
        city: Name of the city
        
    Returns:
        Dictionary with current weather information
    """
    city_key = city.lower().strip()
    
    if city_key not in WEATHER_DATA:
        # Return a default response for unknown cities
        return {
            "city": city,
            "temperature": 70,
            "condition": "unknown",
            "humidity": 50,
            "wind_speed": 10,
            "wind_direction": "N",
            "pressure": 1013.25,
            "visibility": 10,
            "uv_index": 5,
            "note": "Weather data not available for this location"
        }
    
    weather = WEATHER_DATA[city_key].copy()
    weather["city"] = city
    weather["units"] = "imperial"
    
    return weather

@mcp.tool
def get_forecast(city: str, days: int = 3) -> dict[str, str | list[dict]]:
    """Get weather forecast for a city
    
    Args:
        city: Name of the city
        days: Number of days to forecast (1-7)
        
    Returns:
        Dictionary with forecast information
    """
    if days < 1 or days > 7:
        raise ValueError("Forecast days must be between 1 and 7")
    
    city_key = city.lower().strip()
    
    if city_key not in FORECAST_DATA:
        # Generate deterministic forecast for unknown cities
        forecast = []
        for i in range(days):
            forecast.append({
                "day": f"day_{i+1}",
                "high": 70 + (i * 2),
                "low": 55 + (i * 1),
                "condition": ["sunny", "cloudy", "partly cloudy"][i % 3]
            })
    else:
        forecast = FORECAST_DATA[city_key][:days]
    
    return {
        "city": city,
        "forecast": forecast,
        "days": len(forecast),
        "units": "imperial"
    }

@mcp.tool
def get_weather_alerts(city: str) -> dict[str, str | list[dict]]:
    """Get weather alerts for a city
    
    Args:
        city: Name of the city
        
    Returns:
        Dictionary with weather alerts
    """
    # Deterministic alerts based on city
    alerts_map = {
        "miami": [
            {
                "type": "hurricane_watch",
                "severity": "moderate",
                "message": "Hurricane watch in effect until 6 PM EST",
                "expires": "2024-01-15T18:00:00Z"
            }
        ],
        "phoenix": [
            {
                "type": "excessive_heat",
                "severity": "high",
                "message": "Excessive heat warning until midnight",
                "expires": "2024-01-15T07:00:00Z"
            }
        ]
    }
    
    city_key = city.lower().strip()
    alerts = alerts_map.get(city_key, [])
    
    return {
        "city": city,
        "alerts": alerts,
        "alert_count": len(alerts)
    }

@mcp.tool
def compare_weather(city1: str, city2: str) -> dict[str, str | dict]:
    """Compare weather between two cities
    
    Args:
        city1: First city to compare
        city2: Second city to compare
        
    Returns:
        Dictionary with weather comparison
    """
    weather1 = get_current_weather(city1)
    weather2 = get_current_weather(city2)
    
    temp_diff = weather1["temperature"] - weather2["temperature"]
    humidity_diff = weather1["humidity"] - weather2["humidity"]
    
    return {
        "city1": weather1,
        "city2": weather2,
        "comparison": {
            "temperature_difference": temp_diff,
            "humidity_difference": humidity_diff,
            "warmer_city": city1 if temp_diff > 0 else city2,
            "more_humid_city": city1 if humidity_diff > 0 else city2
        }
    }

def main() -> None:
    """Run the weather MCP server"""
    mcp.run()

if __name__ == "__main__":
    main() 