# API Contract: Anthropic Messages API

**Version**: 2023-06-01
**Base URL**: https://api.anthropic.com/v1

## Endpoint: Create Message

**POST** `/messages`

### Request Headers

| Header | Required | Value |
|--------|----------|-------|
| `x-api-key` | Yes | `$ANTHROPIC_API_KEY` |
| `content-type` | Yes | `application/json` |
| `anthropic-version` | Yes | `2023-06-01` |

### Request Body

```json
{
  "model": "claude-sonnet-4-5-20250929",
  "max_tokens": 1024,
  "messages": [
    {
      "role": "user",
      "content": "Generate concise release notes for v1.2.0.\n\nCommits since v1.1.0:\n- feat: add OAuth token refresh\n- fix: handle expired tokens\n- chore: update dependencies\n\nRequirements:\n- Maximum 400 words\n- Group by: New Features, Bug Fixes, Breaking Changes\n- Skip chore/docs/test commits unless significant\n- Use bullet points\n- Be specific but brief"
    }
  ]
}
```

### Request Schema

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `model` | string | Yes | Model identifier |
| `max_tokens` | integer | Yes | Maximum tokens in response |
| `messages` | array | Yes | Conversation messages |
| `messages[].role` | string | Yes | "user" or "assistant" |
| `messages[].content` | string | Yes | Message content |

### Response (Success - 200 OK)

```json
{
  "id": "msg_01XFDUDYJgAACzvnptvVoYEL",
  "type": "message",
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "## What's New in v1.2.0\n\n### New Features\n\n- **OAuth Token Refresh**: Automatically refreshes expired OAuth tokens...\n\n### Bug Fixes\n\n- **Token Expiration Handling**: Fixed issue where expired tokens...\n"
    }
  ],
  "model": "claude-sonnet-4-5-20250929",
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {
    "input_tokens": 156,
    "output_tokens": 234
  }
}
```

### Response Schema

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique message identifier |
| `type` | string | Always "message" |
| `role` | string | Always "assistant" |
| `content` | array | Response content blocks |
| `content[].type` | string | Content type ("text") |
| `content[].text` | string | Generated text |
| `model` | string | Model used |
| `stop_reason` | string | Why generation stopped |
| `usage.input_tokens` | integer | Tokens in request |
| `usage.output_tokens` | integer | Tokens in response |

### Response (Error - 4xx/5xx)

```json
{
  "type": "error",
  "error": {
    "type": "authentication_error",
    "message": "Invalid API key provided"
  }
}
```

### Error Types

| HTTP Code | Error Type | Description |
|-----------|------------|-------------|
| 400 | `invalid_request_error` | Malformed request |
| 401 | `authentication_error` | Invalid or missing API key |
| 403 | `permission_error` | API key lacks permission |
| 429 | `rate_limit_error` | Too many requests |
| 500 | `api_error` | Internal server error |
| 529 | `overloaded_error` | API overloaded |

## Usage in Workflow

### curl Command

```bash
curl -s --max-time 30 https://api.anthropic.com/v1/messages \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "content-type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d @- << 'EOF'
{
  "model": "claude-sonnet-4-5-20250929",
  "max_tokens": 1024,
  "messages": [
    {
      "role": "user",
      "content": "..."
    }
  ]
}
EOF
```

### Response Parsing

```bash
# Extract text from response
NOTES=$(echo "$RESPONSE" | jq -r '.content[0].text // empty')

# Check for errors
ERROR=$(echo "$RESPONSE" | jq -r '.error.message // empty')
if [ -n "$ERROR" ]; then
  echo "::error::Claude API error: $ERROR"
fi
```

## Rate Limits

| Tier | Requests/min | Tokens/min |
|------|--------------|------------|
| Free | 5 | 20,000 |
| Build | 50 | 40,000 |
| Scale | 1,000 | 400,000 |

**Note**: Release workflows run infrequently (per release), so rate limits are not a concern.

## Security Considerations

1. **API Key Storage**: Must be stored in GitHub Secrets, never in code
2. **Key Rotation**: Rotate API keys periodically
3. **Audit Logging**: Anthropic logs all API calls for security review
4. **No PII**: Commit messages should not contain sensitive data
