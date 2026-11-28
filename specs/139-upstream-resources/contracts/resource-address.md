# Contract: Proxied resource address

```
address  = "mcpproxy://upstream/" server-segment "/" original-uri
server-segment = 1*( unreserved / pct-encoded )   ; RFC 3986; every byte of the
                                                   ; server name outside unreserved
                                                   ; is %XX, uppercase hex
original-uri   = 1*OCTET                           ; the upstream URI, byte for byte
template = "mcpproxy://upstream/" server-segment "/" upstream-uri-template
```

Rules:

1. Encoding is total and injective: `Decode(Encode(s, u)) == (s, u)` for every server name `s` and non-empty `u`.
2. Decoding accepts only the canonical form. Refused (answered as "resource not found", nothing sent upstream): missing prefix; no `/` after the segment; empty segment; `%` not followed by two hex digits; lowercase hex or an encoded unreserved byte (re-encoding differs); a segment that does not name a configured server; an empty original URI.
3. A template's expansion with any variable values equals `prefix + expansion of the upstream template with the same values`, because the prefix holds no `{` or `}`.
4. `/mcp/s/<server>` uses original URIs and patterns; no prefix.

Examples:

| Server | Original | Address |
|--------|----------|---------|
| `docs` | `file:///guide.md` | `mcpproxy://upstream/docs/file:///guide.md` |
| `my server` | `db://t/1` | `mcpproxy://upstream/my%20server/db://t/1` |
| `a/b` | `x:y` | `mcpproxy://upstream/a%2Fb/x:y` |
| `docs` | template `file:///{+path}` | `mcpproxy://upstream/docs/file:///{+path}` |

Errors on read (client-visible):

| Case | Code (2025-11-25 era) | Code (2026-07-28 era) | Message |
|------|------|------|---------|
| unknown, malformed, unreachable, quarantined, disabled, excluded, withheld | `-32002` | `-32602` | `handler not found for resource URI '<address>': resource not found` |
| response over cap | `-32603` | `-32603` | `resource response exceeds the 8 MiB limit` |
| blocked by output policy | `-32603` | `-32603` | `resource content blocked by output sanitisation policy` |
| upstream error | `-32603` | `-32603` | `upstream <server>: <error>` |
