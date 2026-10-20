// @ts-check
// Note: type annotations allow type checking and IDEs autocompletion

const fs = require('fs');
const path = require('path');
const {themes: prismThemes} = require('prism-react-renderer');

// Published documentation: the Docusaurus docs plugin serves ONLY these paths.
// Hoisted into one constant so the llms.txt generator below can be restricted to
// exactly the same set (Spec 121): the llms plugin walks the whole docs
// directory, so without this it advertised ~70 internal plans/audits/research
// pages whose URLs 404 on the live site.
const docsPublished = {
  include: [
    'intro.md',
    'getting-started/**/*.{md,mdx}',
    'configuration/**/*.{md,mdx}',
    'cli/**/*.{md,mdx}',
    'api/**/*.{md,mdx}',
    'web-ui/**/*.{md,mdx}',
    'features/**/*.{md,mdx}',
    'code_execution/**/*.{md,mdx}',
    'operations/**/*.{md,mdx}',
    'errors/**/*.{md,mdx}',
    'development/**/*.{md,mdx}',
    'contributing.md',
    // Standalone references with no structured counterpart
    'cli-client-mode.md',
    'cli-output-formatting.md',
    'logging.md',
    'prerelease-builds.md',
    'registries.md',
    'socket-communication.md',
  ],
    // Internal working documents that live in the repo but are not published.
  exclude: [
    'development/sandbox-snap-docker-harness.md',
    'development/sandbox-spike-mcp-34.md',
  ],
};

const llmsRootContent = [
  'MCPProxy is a local-first smart proxy for the Model Context Protocol: it indexes every upstream MCP server\'s tools for BM25 discovery, quarantines unreviewed servers and tool changes, and cuts tool-schema token cost.',
  '',
  'To install, start and connect it from an agent, follow https://docs.mcpproxy.app/getting-started/agent-install (a paste-ready prompt for Claude Code and Codex). Package managers: Homebrew (`brew install smart-mcp-proxy/mcpproxy/mcpproxy`), winget (`winget install smart-mcp-proxy.mcpproxy`), scoop, apt/dnf, Docker.',
].join('\n');

/**
 * ignoreFiles patterns for docusaurus-plugin-llms: every markdown file/dir in
 * the docs tree that the docs plugin does NOT publish. Derived from
 * docsPublished.include by top-level name, so adding a page to the allowlist also adds
 * it to the llms bundles with no second list to maintain.
 */
function unpublishedDocsPatterns() {
  const docsDir = path.join(__dirname, 'docs');
  if (!fs.existsSync(docsDir)) return [];
  const allowedFiles = new Set(docsPublished.include.filter((g) => !g.includes('/')));
  const allowedDirs = new Set(docsPublished.include.filter((g) => g.includes('/')).map((g) => g.split('/')[0]));
  const out = [];
  for (const entry of fs.readdirSync(docsDir, {withFileTypes: true})) {
    if (entry.isDirectory()) {
      if (!allowedDirs.has(entry.name)) out.push(`${entry.name}/**`);
    } else if (/\.mdx?$/.test(entry.name) && !allowedFiles.has(entry.name)) {
      out.push(entry.name);
    }
  }
  // Explicitly excluded pages inside a published directory.
  return out.concat(docsPublished.exclude);
}

/** @type {import('@docusaurus/types').Config} */
// Version shown in the navbar badge. Supplied by CI: docs.yml derives it from
// the latest stable release tag reachable from main, and release.yml's
// deploy-docs job passes the tag it is releasing. Empty locally, which hides
// the badge rather than rendering a placeholder.
//
// The value is interpolated into a RAW HTML navbar item and originates in a git
// tag, which may contain almost any byte, so it is validated rather than
// escaped: only a plain vN[.N...] string is accepted. That also rejects the old
// `__VERSION__` placeholder and any prerelease suffix, both of which hide the
// badge instead of shipping something wrong.
const rawVersion = (process.env.DOCS_VERSION || '').trim();
const normalizedVersion = rawVersion && !rawVersion.startsWith('v') ? `v${rawVersion}` : rawVersion;
const siteVersion = /^v[0-9]+(\.[0-9]+){0,3}$/.test(normalizedVersion) ? normalizedVersion : '';

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
    format: 'detect',
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
          include: docsPublished.include,
          // Internal working documents that live in the repo but are not published
          exclude: docsPublished.exclude,
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
        // Spec 121: bound llms-full.txt for agents. `development/**` holds
        // contributor plans, audits and research reports (about 2.2 MB of the
        // old 3 MB file) that no end-user agent task needs; the pages stay
        // published on the site, only the LLM bundles omit them. The agent
        // install page sorts first (see includeOrder) and
        // website/scripts/check-llms.mjs enforces both invariants after build.
        ignoreFiles: unpublishedDocsPatterns().concat(['intro.md']),
        // intro.md is served at "/" (slug: /) but the plugin links it as
        // /docs, which 404s, so it is left out of the index and replaced by
        // this short, agent-oriented summary that leads with how to install.
        rootContent: llmsRootContent,
        fullRootContent: llmsRootContent,
        includeOrder: [
          'getting-started/agent-install*',
          'getting-started/*',
          'configuration/*',
          'cli/*',
          'api/*',
          'web-ui/*',
          'features/*',
          'code_execution/*',
          'operations/*',
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
          href: '/',
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
          // Version badge. Rendered only when a real version is available.
          // This used to be a hard-coded item containing the literal
          // "v__VERSION__": the `sed` that substitutes that placeholder runs
          // only in release.yml's deploy-docs job, and docs.yml — which
          // deploys the same Cloudflare Pages project on every push to main —
          // never substituted anything, so main's deploy shipped the raw
          // placeholder to production. Both workflows now set DOCS_VERSION and
          // the placeholder is gone. Omitting the badge is better than showing
          // a placeholder, so an unset or malformed value renders nothing.
          ...(siteVersion
            ? [
                {
                  type: 'html',
                  position: 'right',
                  value: `<span class="badge badge--primary">${siteVersion}</span>`,
                },
              ]
            : []),
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
