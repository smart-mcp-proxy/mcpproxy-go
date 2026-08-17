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
        'cli/security-commands',
        'cli/credential-commands',
        'cli/status-command',
        'cli-client-mode',
        'cli-output-formatting',
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
        'web-ui/server-detail',
        'web-ui/activity-log',
        'features/settings-page',
      ],
    },
    {
      type: 'category',
      label: 'Features',
      items: [
        'features/routing-modes',
        'features/search-discovery',
        'features/tools-preflight',
        'features/profiles',
        'features/connect-clients',
        'features/config-import',
        'features/registry-add',
        'features/activity-log',
        'features/intent-declaration',
        'features/toon-output',
        'features/version-updates',
        'features/auto-update',
        'features/telemetry',
        {
          type: 'category',
          label: 'Security',
          items: [
            'features/security-quarantine',
            'features/tool-quarantine',
            'features/tool-scanner',
            'features/security-scanner-plugins',
            'features/sensitive-data-detection',
            'features/output-sanitisation',
            'features/output-schema-validation',
            'features/agent-tokens',
            'features/keyring-integration',
          ],
        },
        {
          type: 'category',
          label: 'Isolation',
          items: [
            'features/docker-isolation',
            'features/sandbox-isolation',
          ],
        },
        {
          type: 'category',
          label: 'Authentication',
          items: [
            'features/oauth-authentication',
            'features/auth-broker',
            'features/idp-token-storage',
          ],
        },
      ],
    },
    {
      type: 'category',
      label: 'Code Execution',
      link: { type: 'doc', id: 'features/code-execution' },
      items: [
        'code_execution/overview',
        'code_execution/api-reference',
        'code_execution/examples',
        'code_execution/cookbook',
        'code_execution/troubleshooting',
      ],
    },
    {
      type: 'category',
      label: 'Operations',
      items: [
        'operations/reverse-proxy',
        'operations/shutdown-behavior',
        'features/observability',
        'logging',
        'socket-communication',
        'registries',
        'features/linux-package-repos',
        'operations/linux-package-repos-infrastructure',
        'prerelease-builds',
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
            'errors/MCPX_STDIO_EXIT_BEFORE_INITIALIZE',
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
            'errors/MCPX_OAUTH_LOGIN_REQUIRED',
            'errors/MCPX_OAUTH_REAUTH_REQUIRED',
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
            'errors/MCPX_DOCKER_EXEC_NOT_FOUND',
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
        'development/web-ui-verification',
        'development/macos-tray',
        'development/release-gate',
        'development/server-edition-multiuser-auth',
        'features/quarantine-testing',
        'features/scanner-images',
      ],
    },
    'contributing',
  ],
};

module.exports = sidebars;
