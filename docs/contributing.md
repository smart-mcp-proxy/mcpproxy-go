---
id: contributing
title: Contributing
sidebar_label: Contributing
sidebar_position: 99
description: How to contribute to MCPProxy documentation
keywords: [contributing, docs, documentation, edit]
---

# Contributing to Documentation

Thank you for your interest in improving MCPProxy documentation!

## Quick Edits

Every documentation page has an "Edit this page" link at the bottom. Click it to:

1. Open the file on GitHub
2. Make your changes in the web editor
3. Submit a pull request

## Local Development

For larger changes, set up local development:

### Prerequisites

- Node.js 18+
- Git

### Setup

```bash
# Clone the repository
git clone https://github.com/smart-mcp-proxy/mcpproxy-go.git
cd mcpproxy-go

# Install documentation dependencies
make docs-setup

# Start local development server
make docs-dev
```

Open http://localhost:3000 to preview your changes with hot reload.

### Build

Before submitting a PR, verify the build succeeds:

```bash
make docs-build
```

## Documentation Structure

```
docs/
├── getting-started/     # Installation and quick start
├── configuration/       # Config file and env vars
├── cli/                 # CLI command reference
├── api/                 # REST API and MCP protocol
├── web-ui/              # Web dashboard docs
├── features/            # Feature documentation
├── images/              # Screenshots and diagrams
└── contributing.md      # This file
```

## Writing Guidelines

### Frontmatter

Every markdown file needs frontmatter:

```markdown
---
id: page-id
title: Page Title
sidebar_label: Short Label
sidebar_position: 1
description: Brief description for SEO
keywords: [keyword1, keyword2]
---
```

### Style

- Use clear, concise language
- Include code examples for technical content
- Add screenshots for UI features
- Link to related documentation

### Code Blocks

Specify the language for syntax highlighting:

````markdown
```bash
mcpproxy serve
```

```json
{
  "key": "value"
}
```
````

## Pull Request Process

1. Fork the repository
2. Create a feature branch: `git checkout -b docs/my-improvement`
3. Make your changes
4. Test locally: `make docs-build`
5. Commit: `git commit -m "docs: improve X documentation"`
6. Push and create a PR

### Commit Messages

Use conventional commits for documentation:

- `docs: add new section about X`
- `docs: fix typo in configuration guide`
- `docs: improve clarity of quick start`

## Getting Help

- Open an [issue](https://github.com/smart-mcp-proxy/mcpproxy-go/issues) for questions
- Join discussions on GitHub
- Check existing documentation for examples
