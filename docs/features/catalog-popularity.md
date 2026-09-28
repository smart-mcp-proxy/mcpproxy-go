---
title: Catalog popularity
sidebar_label: Catalog popularity
description: How MCPProxy ranks catalog results using GitHub stars and Docker Hub pull counts.
---

# Catalog popularity

Catalog popularity uses real, source-native signals. It does not synthesize or
combine counts from different services:

- **GitHub stars** are fetched for catalog entries whose source-code URL points
  to a GitHub repository. The provider caches results for 24 hours and uses
  ETag revalidation. It allows at most four concurrent requests and applies a
  rolling request budget of 50 per hour without a token or 4,000 per hour with
  one.
- **Docker Hub installs** come from the `pull_count` field in the
  `docker-mcp-catalog` listing. Docker's `star_count` is ignored; it is not
  comparable to GitHub stars.

On a cold cache, a catalog search waits for at most 800 ms by default. Missing
GitHub values are fetched in the background and can appear in a later search.
GitHub is not a catalog source, so GitHub request failures do not add an entry
to the search response's `unavailable` list.

Set **`MCPPROXY_GITHUB_TOKEN`** to raise the GitHub request budget. The generic
`GITHUB_TOKEN` environment variable is not read for this purpose.

Set **`MCPPROXY_CATALOG_POPULARITY=false`** (or `0`/`off`) to disable outbound
GitHub requests, for example in an offline environment. Docker pull counts
remain available because they are part of the Docker catalog listing already
being fetched.
