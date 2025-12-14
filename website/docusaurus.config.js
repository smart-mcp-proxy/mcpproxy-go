// @ts-check
// Note: type annotations allow type checking and IDEs autocompletion

const {themes: prismThemes} = require('prism-react-renderer');

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'MCPProxy Documentation',
  tagline: 'Smart MCP Proxy for AI Agents',
  favicon: 'img/favicon.ico',

  // Set the production url of your site here
  url: 'https://docs.mcpproxy.app',
  // Set the /<baseUrl>/ pathname under which your site is served
  baseUrl: '/',

  // GitHub pages deployment config.
  organizationName: 'smart-mcp-proxy',
  projectName: 'mcpproxy-go',

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',

  markdown: {
    format: 'md',
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          routeBasePath: '/', // docs at root
          sidebarPath: './sidebars.js',
          editUrl: 'https://github.com/smart-mcp-proxy/mcpproxy-go/edit/main/',
          // Only include structured documentation pages
          include: [
            'getting-started/**/*.{md,mdx}',
            'configuration/**/*.{md,mdx}',
            'cli/**/*.{md,mdx}',
            'api/**/*.{md,mdx}',
            'web-ui/**/*.{md,mdx}',
            'features/**/*.{md,mdx}',
            'development/**/*.{md,mdx}',
            'contributing.md',
          ],
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      }),
    ],
  ],

  plugins: [
    [
      'docusaurus-plugin-llms',
      {
        generateLLMsTxt: true,
        generateLLMsFullTxt: true,
        excludeImports: true,
        removeDuplicateHeadings: true,
        includeOrder: [
          'getting-started/*',
          'configuration/*',
          'cli/*',
          'api/*',
          'web-ui/*',
          'features/*',
        ],
      },
    ],
  ],

  themes: [
    [
      '@easyops-cn/docusaurus-search-local',
      /** @type {import("@easyops-cn/docusaurus-search-local").PluginOptions} */
      ({
        hashed: true,
        language: ['en'],
        indexDocs: true,
        indexBlog: false,
        docsRouteBasePath: '/',
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      // Replace with your project's social card
      image: 'img/social-card.png',
      navbar: {
        title: 'MCPProxy Docs',
        logo: {
          alt: 'MCPProxy Logo',
          src: 'img/logo.svg',
          href: '/getting-started/installation',
        },
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'docs',
            position: 'left',
            label: 'Documentation',
          },
          {
            href: 'https://mcpproxy.app',
            label: 'Main Site',
            position: 'right',
          },
          {
            href: 'https://github.com/smart-mcp-proxy/mcpproxy-go',
            label: 'GitHub',
            position: 'right',
          },
          {
            type: 'html',
            position: 'right',
            value: '<span class="badge badge--primary">v__VERSION__</span>',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Documentation',
            items: [
              {
                label: 'Getting Started',
                to: '/getting-started/installation',
              },
              {
                label: 'Configuration',
                to: '/configuration/config-file',
              },
              {
                label: 'CLI Reference',
                to: '/cli/command-reference',
              },
            ],
          },
          {
            title: 'Features',
            items: [
              {
                label: 'Docker Isolation',
                to: '/features/docker-isolation',
              },
              {
                label: 'OAuth Authentication',
                to: '/features/oauth-authentication',
              },
              {
                label: 'Code Execution',
                to: '/features/code-execution',
              },
            ],
          },
          {
            title: 'Community',
            items: [
              {
                label: 'GitHub',
                href: 'https://github.com/smart-mcp-proxy/mcpproxy-go',
              },
              {
                label: 'Issues',
                href: 'https://github.com/smart-mcp-proxy/mcpproxy-go/issues',
              },
              {
                label: 'Main Site',
                href: 'https://mcpproxy.app',
              },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} MCPProxy. Built with Docusaurus.`,
      },
      prism: {
        theme: prismThemes.github,
        darkTheme: prismThemes.dracula,
        additionalLanguages: ['bash', 'json', 'go', 'yaml', 'javascript', 'typescript'],
      },
    }),
};

module.exports = config;
