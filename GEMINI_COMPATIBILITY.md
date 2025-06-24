# Gemini 2.5 Pro Preview 06-05 Compatibility

## Issue
Gemini 2.5 Pro Preview 06-05 has known compatibility issues with complex MCP tool argument schemas that use nested objects with `AdditionalProperties(true)`.

## Solution  
The `upstream_servers` and `call_tool` schemas have been updated to use JSON string parameters instead of complex objects for better compatibility with modern AI models:

### Schema Changes

**Before (06-05 incompatible):**
```go
mcp.WithObject("env",
    mcp.Description("Environment variables for stdio servers"),
    mcp.AdditionalProperties(true),
),
mcp.WithObject("headers",
    mcp.Description("HTTP headers for authentication"),
    mcp.AdditionalProperties(true),
),
```

**After (06-05 compatible):**
```go
mcp.WithString("env_json",
    mcp.Description("Environment variables for stdio servers as JSON string (e.g., '{\"API_KEY\": \"value\"}')"),
),
mcp.WithString("headers_json",
    mcp.Description("HTTP headers for authentication as JSON string (e.g., '{\"Authorization\": \"Bearer token\"}')"),
),
```

**call_tool Changes:**
```go
// Before (Gemini 2.5/GPT-4.1 incompatible)
mcp.WithObject("args",
    mcp.Description("Arguments to pass to the tool"),
    mcp.AdditionalProperties(true),
),

// After (Gemini 2.5/GPT-4.1 compatible)  
mcp.WithString("args_json",
    mcp.Description("Arguments to pass to the tool as JSON string"),
),
```

### Usage Examples

**upstream_servers - New JSON string format:**
```json
{
  "operation": "add",
  "name": "test-server",
  "url": "http://localhost:3001", 
  "headers_json": "{\"Authorization\": \"Bearer token123\"}",
  "env_json": "{\"API_KEY\": \"my-key\", \"DEBUG\": \"true\"}"
}
```

**call_tool - New JSON string format:**
```json
{
  "name": "github:create_repository",
  "args_json": "{\"name\": \"my-repo\", \"private\": true, \"description\": \"Test repo\"}"
}
```

**Legacy object formats (still supported):**
```json
// upstream_servers legacy format
{
  "operation": "add",
  "name": "test-server", 
  "url": "http://localhost:3001",
  "headers": {"Authorization": "Bearer token123"},
  "env": {"API_KEY": "my-key", "DEBUG": "true"}
}

// call_tool legacy format  
{
  "name": "github:create_repository",
  "args": {"name": "my-repo", "private": true, "description": "Test repo"}
}
```

## Backward Compatibility
The implementation supports both the new JSON string format and the legacy object format for smooth migration.

## Known Issues with Gemini 2.5 Pro Preview 06-05
- Performance degradation compared to 05-06
- Schema parsing issues with complex nested objects
- Reduced thinking capabilities
- Community feedback led to delayed deprecation of 05-06

## Recommendations

### For Gemini Users
- **Gemini 2.5 Pro (Latest)**: Use the new JSON string schema for optimal compatibility - this addresses known parsing issues with complex objects
- **Gemini 2.5 Pro Preview 05-06**: Still supported if you prefer the more stable version over 06-05
- **Gemini 2.5 Pro Preview 06-05**: Should now work better with the simplified JSON string schema

### For GPT-4.1 Users  
- The new JSON string schema is fully compatible with GPT-4.1's improved parsing capabilities
- Both new and legacy formats are supported for smooth migration

### General Best Practices
- Use the new `args_json` and `*_json` parameters for better cross-model compatibility
- The implementation maintains backward compatibility with existing tools and scripts
- Test with your specific AI model to ensure optimal performance 