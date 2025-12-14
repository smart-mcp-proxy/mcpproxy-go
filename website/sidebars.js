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
      ],
    },
    {
      type: 'category',
      label: 'CLI',
      items: [
        'cli/command-reference',
        'cli/management-commands',
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
        'features/docker-isolation',
        'features/oauth-authentication',
        'features/code-execution',
        'features/security-quarantine',
        'features/search-discovery',
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
