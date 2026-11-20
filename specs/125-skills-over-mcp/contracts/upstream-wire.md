# Contract: what mcpproxy sends to and accepts from an upstream

Precondition for every request below: the server's `initialize` result has `capabilities.extensions["io.modelcontextprotocol/skills"]` **and** `capabilities.resources`; `enable_skills` is on; the server's `expose_skills` is not false. Otherwise mcpproxy sends none of them.

## `skills/list`

Request: `{"jsonrpc":"2.0","id":"mcpproxy-skills-<n>","method":"skills/list","params":{}}`; later pages add `"cursor": "<nextCursor>"`.

Accepted result: `{"skills":[Entry…], "nextCursor"?: string}`. Unknown fields are ignored. At most 50 pages and 200 entries are read, and a page larger than 4 MiB is refused (on the HTTP transports before it is buffered); when a bound stops the read, the listing is recorded as truncated and reported on the server.

`Entry`: `{"uri": string, "frontmatter": object, "resources": [{"uri": string, "digest": "sha256:<hex>", "size": number}] | "dynamic"}`.

A JSON-RPC error, or a result that does not decode, fails the refresh for that server only; stored records are left as they are.

## `skills/get`

Request params: `{"uri": "<SKILL.md URI>"}`. Accepted result: `{"skill": Entry}`.

## `resources/read`

Standard request with the file's URI. Accepted: exactly one content item whose `uri` equals the requested one. `text` → UTF-8 bytes of the string; `blob` → base64-decoded bytes, treated as binary. The bytes must have the listed size and SHA-256. A file is requested only when its listed size fits the skill, server and refresh budgets; on the HTTP transports a response larger than the listed size allows (base64 and envelope included) is cut off as an error, never truncated and used.

## Never sent

`resources/directory/read`, `resources/subscribe`, `resources/list`, `resources/templates/list`, and any request to a server that did not announce the extension.
