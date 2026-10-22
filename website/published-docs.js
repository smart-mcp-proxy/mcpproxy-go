// Shared publication boundary for HTML pages and both LLM exports.
// Keep internal research, plans and QA notes outside these patterns.
module.exports = {
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
  exclude: [
    'development/sandbox-snap-docker-harness.md',
    'development/sandbox-spike-mcp-34.md',
  ],
};
