"""Restaurant MCP Server with Menu and Deterministic Responses"""

from typing import Literal
from fastmcp import FastMCP

mcp = FastMCP("Restaurant Manager 🍽️")

# Deterministic restaurant data
RESTAURANTS = {
    "mario's pizza": {
        "cuisine": "italian",
        "rating": 4.5,
        "price_range": "$$",
        "location": "123 Main St",
        "hours": "11:00 AM - 10:00 PM",
        "menu": {
            "appetizers": [
                {"name": "Garlic Bread", "price": 8.99, "description": "Fresh baked bread with garlic butter"},
                {"name": "Caesar Salad", "price": 12.99, "description": "Romaine lettuce, parmesan, croutons"},
                {"name": "Bruschetta", "price": 10.99, "description": "Toasted bread with tomatoes and basil"}
            ],
            "mains": [
                {"name": "Margherita Pizza", "price": 16.99, "description": "Tomato sauce, mozzarella, fresh basil"},
                {"name": "Pepperoni Pizza", "price": 18.99, "description": "Tomato sauce, mozzarella, pepperoni"},
                {"name": "Spaghetti Carbonara", "price": 22.99, "description": "Pasta with eggs, cheese, pancetta"},
                {"name": "Lasagna", "price": 24.99, "description": "Layered pasta with meat sauce and cheese"}
            ],
            "desserts": [
                {"name": "Tiramisu", "price": 8.99, "description": "Coffee-flavored Italian dessert"},
                {"name": "Gelato", "price": 6.99, "description": "Italian ice cream, various flavors"}
            ]
        }
    },
    "sushi zen": {
        "cuisine": "japanese",
        "rating": 4.8,
        "price_range": "$$$",
        "location": "456 Oak Ave",
        "hours": "5:00 PM - 11:00 PM",
        "menu": {
            "appetizers": [
                {"name": "Edamame", "price": 6.99, "description": "Steamed soybeans with sea salt"},
                {"name": "Gyoza", "price": 8.99, "description": "Pan-fried pork dumplings"},
                {"name": "Miso Soup", "price": 4.99, "description": "Traditional soybean paste soup"}
            ],
            "sushi_rolls": [
                {"name": "California Roll", "price": 12.99, "description": "Crab, avocado, cucumber"},
                {"name": "Spicy Tuna Roll", "price": 14.99, "description": "Spicy tuna, cucumber, avocado"},
                {"name": "Dragon Roll", "price": 18.99, "description": "Eel, cucumber, avocado on top"},
                {"name": "Rainbow Roll", "price": 16.99, "description": "California roll topped with assorted fish"}
            ],
            "sashimi": [
                {"name": "Salmon Sashimi", "price": 15.99, "description": "Fresh raw salmon slices"},
                {"name": "Tuna Sashimi", "price": 17.99, "description": "Fresh raw tuna slices"},
                {"name": "Mixed Sashimi", "price": 24.99, "description": "Assortment of fresh fish"}
            ]
        }
    },
    "burger barn": {
        "cuisine": "american",
        "rating": 4.2,
        "price_range": "$",
        "location": "789 Elm St",
        "hours": "11:00 AM - 9:00 PM",
        "menu": {
            "burgers": [
                {"name": "Classic Burger", "price": 12.99, "description": "Beef patty, lettuce, tomato, onion"},
                {"name": "Cheeseburger", "price": 14.99, "description": "Classic burger with cheddar cheese"},
                {"name": "BBQ Bacon Burger", "price": 16.99, "description": "Burger with BBQ sauce and bacon"},
                {"name": "Veggie Burger", "price": 13.99, "description": "Plant-based patty with vegetables"}
            ],
            "sides": [
                {"name": "French Fries", "price": 4.99, "description": "Crispy golden fries"},
                {"name": "Onion Rings", "price": 6.99, "description": "Beer-battered onion rings"},
                {"name": "Coleslaw", "price": 3.99, "description": "Fresh cabbage salad"}
            ],
            "shakes": [
                {"name": "Vanilla Shake", "price": 5.99, "description": "Creamy vanilla milkshake"},
                {"name": "Chocolate Shake", "price": 5.99, "description": "Rich chocolate milkshake"},
                {"name": "Strawberry Shake", "price": 5.99, "description": "Fresh strawberry milkshake"}
            ]
        }
    }
}

@mcp.tool
def search_restaurants(cuisine: str = "any", price_range: str = "any") -> dict[str, str | list[dict]]:
    """Search for restaurants by cuisine or price range
    
    Args:
        cuisine: Type of cuisine (italian, japanese, american, any)
        price_range: Price range ($, $$, $$$, any)
        
    Returns:
        Dictionary with matching restaurants
    """
    results = []
    
    for name, restaurant in RESTAURANTS.items():
        cuisine_match = cuisine == "any" or restaurant["cuisine"].lower() == cuisine.lower()
        price_match = price_range == "any" or restaurant["price_range"] == price_range
        
        if cuisine_match and price_match:
            results.append({
                "name": name,
                "cuisine": restaurant["cuisine"],
                "rating": restaurant["rating"],
                "price_range": restaurant["price_range"],
                "location": restaurant["location"],
                "hours": restaurant["hours"]
            })
    
    return {
        "query": {"cuisine": cuisine, "price_range": price_range},
        "results": results,
        "count": len(results)
    }

@mcp.tool
def get_menu(restaurant_name: str) -> dict[str, str | dict]:
    """Get the full menu for a restaurant
    
    Args:
        restaurant_name: Name of the restaurant
        
    Returns:
        Dictionary with the restaurant's menu
    """
    restaurant_key = restaurant_name.lower().strip()
    
    if restaurant_key not in RESTAURANTS:
        return {
            "restaurant": restaurant_name,
            "error": "Restaurant not found",
            "available_restaurants": list(RESTAURANTS.keys())
        }
    
    restaurant = RESTAURANTS[restaurant_key]
    return {
        "restaurant": restaurant_name,
        "cuisine": restaurant["cuisine"],
        "menu": restaurant["menu"],
        "location": restaurant["location"],
        "hours": restaurant["hours"]
    }

@mcp.tool
def get_menu_section(restaurant_name: str, section: str) -> dict[str, str | list[dict]]:
    """Get a specific section of a restaurant's menu
    
    Args:
        restaurant_name: Name of the restaurant
        section: Menu section (appetizers, mains, desserts, etc.)
        
    Returns:
        Dictionary with the requested menu section
    """
    restaurant_key = restaurant_name.lower().strip()
    
    if restaurant_key not in RESTAURANTS:
        return {
            "restaurant": restaurant_name,
            "error": "Restaurant not found"
        }
    
    menu = RESTAURANTS[restaurant_key]["menu"]
    section_key = section.lower().strip()
    
    if section_key not in menu:
        return {
            "restaurant": restaurant_name,
            "section": section,
            "error": "Menu section not found",
            "available_sections": list(menu.keys())
        }
    
    return {
        "restaurant": restaurant_name,
        "section": section,
        "items": menu[section_key]
    }

@mcp.tool
def calculate_order_total(restaurant_name: str, items: list[str]) -> dict[str, str | float | list[dict]]:
    """Calculate the total cost of an order
    
    Args:
        restaurant_name: Name of the restaurant
        items: List of item names to order
        
    Returns:
        Dictionary with order details and total cost
    """
    restaurant_key = restaurant_name.lower().strip()
    
    if restaurant_key not in RESTAURANTS:
        return {
            "restaurant": restaurant_name,
            "error": "Restaurant not found"
        }
    
    menu = RESTAURANTS[restaurant_key]["menu"]
    order_items = []
    subtotal = 0.0
    
    # Flatten menu to find items
    all_items = {}
    for section_items in menu.values():
        for item in section_items:
            all_items[item["name"].lower()] = item
    
    for item_name in items:
        item_key = item_name.lower().strip()
        if item_key in all_items:
            item = all_items[item_key]
            order_items.append(item)
            subtotal += item["price"]
        else:
            order_items.append({
                "name": item_name,
                "error": "Item not found",
                "price": 0.0
            })
    
    tax = subtotal * 0.08  # 8% tax
    tip = subtotal * 0.18  # 18% suggested tip
    total = subtotal + tax + tip
    
    return {
        "restaurant": restaurant_name,
        "order_items": order_items,
        "subtotal": round(subtotal, 2),
        "tax": round(tax, 2),
        "suggested_tip": round(tip, 2),
        "total": round(total, 2)
    }

@mcp.tool
def get_restaurant_info(restaurant_name: str) -> dict[str, str | float]:
    """Get basic information about a restaurant
    
    Args:
        restaurant_name: Name of the restaurant
        
    Returns:
        Dictionary with restaurant information
    """
    restaurant_key = restaurant_name.lower().strip()
    
    if restaurant_key not in RESTAURANTS:
        return {
            "restaurant": restaurant_name,
            "error": "Restaurant not found",
            "available_restaurants": list(RESTAURANTS.keys())
        }
    
    restaurant = RESTAURANTS[restaurant_key]
    return {
        "name": restaurant_name,
        "cuisine": restaurant["cuisine"],
        "rating": restaurant["rating"],
        "price_range": restaurant["price_range"],
        "location": restaurant["location"],
        "hours": restaurant["hours"]
    }

def main() -> None:
    """Run the restaurant MCP server"""
    mcp.run()

if __name__ == "__main__":
    main() 