/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.

 Create as many sidebars as you want.
 */

// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  docs: [
    'intro',
    {
      type: 'category',
      label: 'Getting Started',
      collapsed: false,
      items: [
        'getting-started/installation',
        'getting-started/quick-start',
      ],
    },
    {
      type: 'category',
      label: 'Configuration',
      items: [
        'configuration/config-file',
        'configuration/upstream-servers',
        'configuration/environment-variables',
        'configuration/sensitive-data-detection',
      ],
    },
    {
      type: 'category',
      label: 'CLI',
      items: [
        'cli/command-reference',
        'cli/management-commands',
        'cli/activity-commands',
        'cli/sensitive-data-commands',
        'cli/status-command',
      ],
    },
    {
      type: 'category',
      label: 'API',
      items: [
        'api/rest-api',
        'api/mcp-protocol',
      ],
    },
    {
      type: 'category',
      label: 'Web UI',
      items: [
        'web-ui/dashboard',
      ],
    },
    {
      type: 'category',
      label: 'Features',
      items: [
        'features/intent-declaration',
        'features/activity-log',
        'features/sensitive-data-detection',
        'features/docker-isolation',
        'features/oauth-authentication',
        'features/code-execution',
        'features/security-quarantine',
        'features/search-discovery',
        'features/version-updates',
      ],
    },
    {
      type: 'category',
      label: 'Operations',
      items: [
        'operations/shutdown-behavior',
      ],
    },
    {
      type: 'category',
      label: 'Errors',
      link: { type: 'doc', id: 'errors/README' },
      items: [
        {
          type: 'category',
          label: 'STDIO',
          items: [
            'errors/MCPX_STDIO_SPAWN_ENOENT',
            'errors/MCPX_STDIO_SPAWN_EACCES',
            'errors/MCPX_STDIO_EXIT_NONZERO',
            'errors/MCPX_STDIO_HANDSHAKE_TIMEOUT',
            'errors/MCPX_STDIO_HANDSHAKE_INVALID',
          ],
        },
        {
          type: 'category',
          label: 'OAuth',
          items: [
            'errors/MCPX_OAUTH_REFRESH_EXPIRED',
            'errors/MCPX_OAUTH_REFRESH_403',
            'errors/MCPX_OAUTH_DISCOVERY_FAILED',
            'errors/MCPX_OAUTH_CALLBACK_TIMEOUT',
            'errors/MCPX_OAUTH_CALLBACK_MISMATCH',
          ],
        },
        {
          type: 'category',
          label: 'HTTP',
          items: [
            'errors/MCPX_HTTP_DNS_FAILED',
            'errors/MCPX_HTTP_TLS_FAILED',
            'errors/MCPX_HTTP_401',
            'errors/MCPX_HTTP_403',
            'errors/MCPX_HTTP_404',
            'errors/MCPX_HTTP_5XX',
            'errors/MCPX_HTTP_CONN_REFUSED',
          ],
        },
        {
          type: 'category',
          label: 'Docker',
          items: [
            'errors/MCPX_DOCKER_DAEMON_DOWN',
            'errors/MCPX_DOCKER_IMAGE_PULL_FAILED',
            'errors/MCPX_DOCKER_NO_PERMISSION',
            'errors/MCPX_DOCKER_SNAP_APPARMOR',
          ],
        },
        {
          type: 'category',
          label: 'Config',
          items: [
            'errors/MCPX_CONFIG_DEPRECATED_FIELD',
            'errors/MCPX_CONFIG_PARSE_ERROR',
            'errors/MCPX_CONFIG_MISSING_SECRET',
          ],
        },
        {
          type: 'category',
          label: 'Quarantine',
          items: [
            'errors/MCPX_QUARANTINE_PENDING_APPROVAL',
            'errors/MCPX_QUARANTINE_TOOL_CHANGED',
          ],
        },
        {
          type: 'category',
          label: 'Network',
          items: [
            'errors/MCPX_NETWORK_PROXY_MISCONFIG',
            'errors/MCPX_NETWORK_OFFLINE',
          ],
        },
        'errors/MCPX_UNKNOWN_UNCLASSIFIED',
      ],
    },
    {
      type: 'category',
      label: 'Development',
      collapsed: true,
      items: [
        'development/architecture',
        'development/testing',
        'development/building',
      ],
    },
    'contributing',
  ],
};

module.exports = sidebars;
