"""Fake MCP Registry Server with Deterministic Responses"""

from datetime import datetime
from typing import Optional, List
from fastapi import FastAPI, Query
from pydantic import BaseModel

app = FastAPI(title="Fake MCP Registry", description="Test registry for MCP server evaluation")

class RepositoryInfo(BaseModel):
    url: str
    source: str
    id: str

class VersionDetail(BaseModel):
    version: str
    release_date: str
    is_latest: bool

class PackageArgument(BaseModel):
    description: str
    is_required: bool
    format: str
    value: str
    default: str
    type: str
    value_hint: str

class Package(BaseModel):
    registry_name: str
    name: str
    version: str
    package_arguments: List[PackageArgument]

class ServerEntry(BaseModel):
    id: str
    name: str
    url: Optional[str] = None
    description: str
    created_at: str
    updated_at: str
    repository: Optional[RepositoryInfo] = None
    version_detail: Optional[VersionDetail] = None
    packages: Optional[List[Package]] = None

class ServersResponse(BaseModel):
    servers: List[ServerEntry]
    metadata: dict

# Deterministic server data for testing
FAKE_SERVERS = [
    ServerEntry(
        id="01129bff-3d65-4e3d-8e82-6f2f269f818c",
        name="dice-roller",
        url="stdio://uvx mcp-dice",
        description="A dice rolling MCP server for gaming and probability calculations",
        created_at="2025-01-15T10:00:00.000Z",
        updated_at="2025-01-15T10:00:00.000Z",
        repository=RepositoryInfo(
            url="https://github.com/fake/dice-mcp-server",
            source="github",
            id="123456789"
        ),
        version_detail=VersionDetail(
            version="1.0.0",
            release_date="2025-01-15T10:00:00Z",
            is_latest=True
        )
    ),
    ServerEntry(
        id="02229bff-3d65-4e3d-8e82-6f2f269f818d",
        name="weather-service",
        url="stdio://uvx mcp-weather",
        description="Weather information MCP server with forecasts and current conditions",
        created_at="2025-01-15T10:05:00.000Z",
        updated_at="2025-01-15T10:05:00.000Z",
        repository=RepositoryInfo(
            url="https://github.com/fake/weather-mcp-server",
            source="github",
            id="123456790"
        ),
        version_detail=VersionDetail(
            version="2.1.0",
            release_date="2025-01-15T10:05:00Z",
            is_latest=True
        )
    ),
    ServerEntry(
        id="03329bff-3d65-4e3d-8e82-6f2f269f818e",
        name="restaurant-finder",
        url="stdio://uvx mcp-restaurant",
        description="Restaurant menu and search MCP server for food discovery",
        created_at="2025-01-15T10:10:00.000Z",
        updated_at="2025-01-15T10:10:00.000Z",
        repository=RepositoryInfo(
            url="https://github.com/fake/restaurant-mcp-server",
            source="github",
            id="123456791"
        ),
        version_detail=VersionDetail(
            version="1.5.0",
            release_date="2025-01-15T10:10:00Z",
            is_latest=True
        )
    ),
    ServerEntry(
        id="04429bff-3d65-4e3d-8e82-6f2f269f818f",
        name="morse-translator",
        url="stdio://uvx mcp-morse",
        description="Morse code translation MCP server for encoding and decoding text",
        created_at="2025-01-15T10:15:00.000Z",
        updated_at="2025-01-15T10:15:00.000Z",
        repository=RepositoryInfo(
            url="https://github.com/fake/morse-mcp-server",
            source="github",
            id="123456792"
        ),
        version_detail=VersionDetail(
            version="1.0.0",
            release_date="2025-01-15T10:15:00Z",
            is_latest=True
        )
    ),
    ServerEntry(
        id="05529bff-3d65-4e3d-8e82-6f2f269f8190",
        name="calculator",
        url="stdio://uvx mcp-calculator",
        description="Mathematical calculation MCP server with basic and advanced operations",
        created_at="2025-01-15T10:20:00.000Z",
        updated_at="2025-01-15T10:20:00.000Z",
        repository=RepositoryInfo(
            url="https://github.com/fake/calculator-mcp-server",
            source="github",
            id="123456793"
        ),
        version_detail=VersionDetail(
            version="3.0.0",
            release_date="2025-01-15T10:20:00Z",
            is_latest=True
        )
    ),
    ServerEntry(
        id="06629bff-3d65-4e3d-8e82-6f2f269f8191",
        name="translator",
        url="stdio://uvx mcp-translator",
        description="Multi-language translation MCP server supporting major languages",
        created_at="2025-01-15T10:25:00.000Z",
        updated_at="2025-01-15T10:25:00.000Z",
        repository=RepositoryInfo(
            url="https://github.com/fake/translator-mcp-server",
            source="github",
            id="123456794"
        ),
        version_detail=VersionDetail(
            version="2.0.0",
            release_date="2025-01-15T10:25:00Z",
            is_latest=True
        )
    ),
    ServerEntry(
        id="07729bff-3d65-4e3d-8e82-6f2f269f8192",
        name="time-service",
        url="stdio://uvx mcp-time",
        description="Time and timezone MCP server for temporal operations",
        created_at="2025-01-15T10:30:00.000Z",
        updated_at="2025-01-15T10:30:00.000Z",
        repository=RepositoryInfo(
            url="https://github.com/fake/time-mcp-server",
            source="github",
            id="123456795"
        ),
        version_detail=VersionDetail(
            version="1.2.0",
            release_date="2025-01-15T10:30:00Z",
            is_latest=True
        )
    ),
    ServerEntry(
        id="08829bff-3d65-4e3d-8e82-6f2f269f8193",
        name="joke-generator",
        url="stdio://uvx mcp-jokes",
        description="Joke and humor MCP server for entertainment and conversation",
        created_at="2025-01-15T10:35:00.000Z",
        updated_at="2025-01-15T10:35:00.000Z",
        repository=RepositoryInfo(
            url="https://github.com/fake/jokes-mcp-server",
            source="github",
            id="123456796"
        ),
        version_detail=VersionDetail(
            version="1.1.0",
            release_date="2025-01-15T10:35:00Z",
            is_latest=True
        )
    ),
    ServerEntry(
        id="09929bff-3d65-4e3d-8e82-6f2f269f8194",
        name="color-palette",
        url="stdio://uvx mcp-color",
        description="Color manipulation and palette MCP server for design tools",
        created_at="2025-01-15T10:40:00.000Z",
        updated_at="2025-01-15T10:40:00.000Z",
        repository=RepositoryInfo(
            url="https://github.com/fake/color-mcp-server",
            source="github",
            id="123456797"
        ),
        version_detail=VersionDetail(
            version="1.0.0",
            release_date="2025-01-15T10:40:00Z",
            is_latest=True
        )
    ),
    ServerEntry(
        id="10029bff-3d65-4e3d-8e82-6f2f269f8195",
        name="random-generator",
        url="stdio://uvx mcp-random",
        description="Random number and data generation MCP server for testing and simulation",
        created_at="2025-01-15T10:45:00.000Z",
        updated_at="2025-01-15T10:45:00.000Z",
        repository=RepositoryInfo(
            url="https://github.com/fake/random-mcp-server",
            source="github",
            id="123456798"
        ),
        version_detail=VersionDetail(
            version="2.0.0",
            release_date="2025-01-15T10:45:00Z",
            is_latest=True
        )
    )
]

@app.get("/v0/servers", response_model=ServersResponse)
async def list_servers(
    limit: int = Query(default=30, le=100),
    cursor: Optional[str] = Query(default=None)
):
    """List MCP registry server entries with pagination support"""
    start_idx = 0
    if cursor:
        # Simple cursor implementation - find server by ID
        for i, server in enumerate(FAKE_SERVERS):
            if server.id == cursor:
                start_idx = i + 1
                break
    
    end_idx = min(start_idx + limit, len(FAKE_SERVERS))
    servers = FAKE_SERVERS[start_idx:end_idx]
    
    next_cursor = None
    if end_idx < len(FAKE_SERVERS):
        next_cursor = FAKE_SERVERS[end_idx - 1].id
    
    return ServersResponse(
        servers=servers,
        metadata={
            "next_cursor": next_cursor,
            "count": len(servers)
        }
    )

@app.get("/v0/servers/{server_id}", response_model=ServerEntry)
async def get_server_details(server_id: str):
    """Retrieve detailed information about a specific MCP server entry"""
    for server in FAKE_SERVERS:
        if server.id == server_id:
            # Add detailed package information for the response
            server.packages = [
                Package(
                    registry_name="uvx",
                    name=f"@mcpeval/{server.name}",
                    version=server.version_detail.version if server.version_detail else "1.0.0",
                    package_arguments=[
                        PackageArgument(
                            description="Server executable command",
                            is_required=True,
                            format="string",
                            value=f"mcp-{server.name.split('-')[0]}",
                            default=f"mcp-{server.name.split('-')[0]}",
                            type="positional",
                            value_hint=f"mcp-{server.name.split('-')[0]}"
                        )
                    ]
                )
            ]
            return server
    
    from fastapi import HTTPException
    raise HTTPException(status_code=404, detail="Server not found")

@app.get("/health")
async def health_check():
    """Health check endpoint"""
    return {"status": "healthy", "timestamp": datetime.utcnow().isoformat()}

def main() -> None:
    """Run the fake MCP registry server"""
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8001)

if __name__ == "__main__":
    main() 