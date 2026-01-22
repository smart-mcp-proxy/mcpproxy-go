#!/usr/bin/env python3
"""
Mock Runlayer-style OAuth server for testing issue #271.

This server mimics Runlayer's OAuth behavior which requires the RFC 8707
`resource` parameter in the authorization URL.

Usage:
    pip install fastapi uvicorn
    # Or with uv:
    uv run --with fastapi --with uvicorn python oauth_runlayer_mock.py

    # With custom port:
    PORT=9000 python oauth_runlayer_mock.py

The server exposes:
    - /.well-known/oauth-protected-resource (RFC 9728 PRM)
    - /.well-known/oauth-authorization-server (RFC 8414)
    - /register (Dynamic Client Registration)
    - /authorize (requires `resource` param - returns 422 if missing)
    - /token (token exchange)
    - /mcp (protected MCP endpoint - returns 401 without auth)
"""

import os
import json
import secrets
from urllib.parse import urlencode
from typing import Optional

from fastapi import FastAPI, Request, Response, Query, HTTPException
from fastapi.responses import RedirectResponse

app = FastAPI(title="Runlayer OAuth Mock Server")

# Server state
PORT = int(os.environ.get("PORT", "8000"))
BASE_URL = os.environ.get("BASE_URL", f"http://localhost:{PORT}")

# Storage for registered clients and issued tokens
registered_clients: dict[str, dict] = {}
issued_tokens: dict[str, dict] = {}


@app.get("/.well-known/oauth-protected-resource")
async def protected_resource_metadata():
    """RFC 9728 Protected Resource Metadata."""
    return {
        "resource": f"{BASE_URL}/mcp",
        "authorization_servers": [BASE_URL],
        "scopes_supported": ["read", "write"],
        "bearer_methods_supported": ["header"],
    }


@app.get("/.well-known/oauth-authorization-server")
async def authorization_server_metadata():
    """RFC 8414 OAuth Authorization Server Metadata."""
    return {
        "issuer": BASE_URL,
        "authorization_endpoint": f"{BASE_URL}/authorize",
        "token_endpoint": f"{BASE_URL}/token",
        "registration_endpoint": f"{BASE_URL}/register",
        "response_types_supported": ["code"],
        "code_challenge_methods_supported": ["S256"],
        "grant_types_supported": ["authorization_code", "refresh_token"],
        "token_endpoint_auth_methods_supported": ["none", "client_secret_post"],
    }


@app.post("/register")
async def register_client(request: Request):
    """Dynamic Client Registration (RFC 7591)."""
    body = await request.json()
    client_name = body.get("client_name", "unknown")

    client_id = f"mock-client-{secrets.token_hex(8)}"
    client_secret = f"mock-secret-{secrets.token_hex(16)}"

    registered_clients[client_id] = {
        "client_id": client_id,
        "client_secret": client_secret,
        "client_name": client_name,
        "redirect_uris": body.get("redirect_uris", []),
    }

    return {
        "client_id": client_id,
        "client_secret": client_secret,
        "client_name": client_name,
        "redirect_uris": body.get("redirect_uris", []),
    }


@app.get("/authorize")
async def authorize(
    client_id: str = Query(...),
    redirect_uri: str = Query(...),
    state: str = Query(...),
    response_type: str = Query(default="code"),
    code_challenge: Optional[str] = Query(default=None),
    code_challenge_method: Optional[str] = Query(default=None),
    resource: str = Query(..., description="RFC 8707 resource parameter - REQUIRED"),
):
    """
    Authorization endpoint.

    IMPORTANT: The `resource` parameter is REQUIRED (Runlayer behavior).
    FastAPI/Pydantic will return a 422 validation error if missing.
    """
    # Generate authorization code
    auth_code = f"mock-auth-code-{secrets.token_hex(16)}"

    # Store code for token exchange
    issued_tokens[auth_code] = {
        "client_id": client_id,
        "redirect_uri": redirect_uri,
        "resource": resource,
        "code_challenge": code_challenge,
    }

    # Redirect back with code
    redirect_params = urlencode({"code": auth_code, "state": state})
    return RedirectResponse(
        url=f"{redirect_uri}?{redirect_params}",
        status_code=302,
    )


@app.post("/token")
async def token(request: Request):
    """Token endpoint."""
    form = await request.form()
    grant_type = form.get("grant_type")

    if grant_type == "authorization_code":
        code = form.get("code")
        if not code or code not in issued_tokens:
            raise HTTPException(status_code=400, detail="Invalid authorization code")

        # Clean up used code
        issued_tokens.pop(code)

        return {
            "access_token": f"mock-access-token-{secrets.token_hex(16)}",
            "token_type": "Bearer",
            "expires_in": 3600,
            "refresh_token": f"mock-refresh-token-{secrets.token_hex(16)}",
        }

    elif grant_type == "refresh_token":
        return {
            "access_token": f"mock-access-token-{secrets.token_hex(16)}",
            "token_type": "Bearer",
            "expires_in": 3600,
            "refresh_token": f"mock-refresh-token-{secrets.token_hex(16)}",
        }

    raise HTTPException(status_code=400, detail=f"Unsupported grant_type: {grant_type}")


@app.api_route("/mcp", methods=["GET", "POST"])
async def mcp_endpoint(request: Request):
    """
    Protected MCP endpoint.
    Returns 401 with WWW-Authenticate header if not authenticated.
    """
    auth_header = request.headers.get("Authorization", "")

    if not auth_header.startswith("Bearer "):
        return Response(
            content=json.dumps({
                "error": "unauthorized",
                "message": "Authentication required",
            }),
            status_code=401,
            headers={
                "WWW-Authenticate": f'Bearer error="invalid_token", resource_metadata="{BASE_URL}/.well-known/oauth-protected-resource"',
                "Content-Type": "application/json",
            },
        )

    # Authenticated - return MCP initialize response
    return {
        "jsonrpc": "2.0",
        "id": 1,
        "result": {
            "protocolVersion": "2024-11-05",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "runlayer-mock", "version": "1.0.0"},
        },
    }


@app.get("/")
async def root():
    """Server info."""
    return {
        "name": "Runlayer OAuth Mock Server",
        "description": "Mock server for testing issue #271 - RFC 8707 resource parameter",
        "base_url": BASE_URL,
        "endpoints": {
            "protected_resource_metadata": "/.well-known/oauth-protected-resource",
            "authorization_server_metadata": "/.well-known/oauth-authorization-server",
            "register": "/register",
            "authorize": "/authorize",
            "token": "/token",
            "mcp": "/mcp",
        },
    }


if __name__ == "__main__":
    import uvicorn

    print(f"Starting Runlayer OAuth Mock Server on {BASE_URL}")
    print(f"MCP endpoint: {BASE_URL}/mcp")
    print(f"Protected Resource Metadata: {BASE_URL}/.well-known/oauth-protected-resource")
    print()
    print("This server requires the 'resource' parameter in /authorize")
    print("Missing resource param will return Pydantic 422 validation error")
    print()

    uvicorn.run(app, host="0.0.0.0", port=PORT)
