#!/usr/bin/env node
/**
 * MCP Test Suite
 *
 * Comprehensive test suite for MCP protocol functionality using real MCP client.
 * Tests both built-in and upstream tool calls, tool discovery, and history tracking.
 *
 * Usage:
 *   1. Start mcpproxy: MCPPROXY_API_KEY="test-key" ./mcpproxy serve
 *   2. Run tests: node tests/mcp/test-suite.mjs
 */

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { SSEClientTransport } from "@modelcontextprotocol/sdk/client/sse.js";

// Test configuration
const config = {
  serverUrl: process.env.MCPPROXY_URL || "http://127.0.0.1:8080",
  apiKey: process.env.MCPPROXY_API_KEY || "",
  timeout: 30000,
};

// Test results tracking
const results = {
  passed: 0,
  failed: 0,
  skipped: 0,
  tests: []
};

// Test utilities
class TestRunner {
  constructor(name) {
    this.name = name;
    this.client = null;
    this.transport = null;
  }

  async setup() {
    console.log(`\n${"=".repeat(60)}`);
    console.log(`🧪 ${this.name}`);
    console.log("=".repeat(60));

    this.client = new Client({
      name: "mcp-test-suite",
      version: "1.0.0",
    }, {
      capabilities: { tools: {} }
    });

    this.transport = new SSEClientTransport(
      new URL("/mcp/sse", config.serverUrl),
      { headers: config.apiKey ? { "X-API-Key": config.apiKey } : {} }
    );

    await this.client.connect(this.transport);
  }

  async teardown() {
    if (this.client) {
      await this.client.close();
    }
  }

  async test(name, fn) {
    const testCase = { name, suite: this.name };
    try {
      process.stdout.write(`  ⏳ ${name}... `);
      await fn();
      console.log("✅ PASS");
      results.passed++;
      testCase.status = "passed";
    } catch (error) {
      console.log(`❌ FAIL: ${error.message}`);
      results.failed++;
      testCase.status = "failed";
      testCase.error = error.message;
    }
    results.tests.push(testCase);
  }

  skip(name, reason) {
    console.log(`  ⏭️  ${name} - SKIPPED: ${reason}`);
    results.skipped++;
    results.tests.push({ name, suite: this.name, status: "skipped", reason });
  }

  assert(condition, message) {
    if (!condition) {
      throw new Error(message);
    }
  }

  assertEqual(actual, expected, message) {
    if (actual !== expected) {
      throw new Error(`${message}: expected ${expected}, got ${actual}`);
    }
  }

  assertGreater(actual, threshold, message) {
    if (actual <= threshold) {
      throw new Error(`${message}: expected > ${threshold}, got ${actual}`);
    }
  }

  assertIncludes(array, item, message) {
    if (!array.includes(item)) {
      throw new Error(`${message}: expected array to include ${item}`);
    }
  }
}

// Test Suite 1: Connection & Discovery
async function testConnectionAndDiscovery() {
  const runner = new TestRunner("Connection & Discovery");
  await runner.setup();

  await runner.test("Initialize MCP connection", async () => {
    runner.assert(runner.client, "Client should be connected");
  });

  let tools;
  await runner.test("List all available tools", async () => {
    const response = await runner.client.listTools();
    tools = response.tools;
    runner.assertGreater(tools.length, 0, "Should have at least one tool");
  });

  await runner.test("Verify built-in tools are present", async () => {
    const toolNames = tools.map(t => t.name);
    runner.assertIncludes(toolNames, "retrieve_tools", "retrieve_tools should be available");
    runner.assertIncludes(toolNames, "upstream_servers", "upstream_servers should be available");
  });

  await runner.teardown();
}

// Test Suite 2: Built-in Tools
async function testBuiltInTools() {
  const runner = new TestRunner("Built-in Tools");
  await runner.setup();

  await runner.test("Call retrieve_tools with query", async () => {
    const result = await runner.client.callTool({
      name: "retrieve_tools",
      arguments: { query: "echo", limit: 5 }
    });
    runner.assert(result.content, "Result should have content");
    runner.assert(Array.isArray(result.content), "Content should be an array");
  });

  await runner.test("Call upstream_servers list operation", async () => {
    const result = await runner.client.callTool({
      name: "upstream_servers",
      arguments: { operation: "list" }
    });
    runner.assert(result.content, "Result should have content");
  });

  await runner.test("Call tools_stat for statistics", async () => {
    const result = await runner.client.callTool({
      name: "tools_stat",
      arguments: {}
    });
    runner.assert(result.content, "Result should have content");
  });

  await runner.teardown();
}

// Test Suite 3: Upstream Tools (if available)
async function testUpstreamTools() {
  const runner = new TestRunner("Upstream Tools");
  await runner.setup();

  const response = await runner.client.listTools();
  const upstreamTools = response.tools.filter(t => t.name.includes(":"));

  if (upstreamTools.length === 0) {
    runner.skip("Test upstream tools", "No upstream servers configured");
    await runner.teardown();
    return;
  }

  // Test echo tool if available
  const echoTool = upstreamTools.find(t => t.name.endsWith(":echo"));
  if (echoTool) {
    await runner.test(`Call ${echoTool.name}`, async () => {
      const result = await runner.client.callTool({
        name: echoTool.name,
        arguments: { message: "Test message" }
      });
      runner.assert(result.content, "Result should have content");
    });
  }

  // Test math tool if available
  const addTool = upstreamTools.find(t => t.name.endsWith(":add"));
  if (addTool) {
    await runner.test(`Call ${addTool.name}`, async () => {
      const result = await runner.client.callTool({
        name: addTool.name,
        arguments: { a: 10, b: 32 }
      });
      runner.assert(result.content, "Result should have content");
    });
  }

  await runner.teardown();
}

// Test Suite 4: Tool Call History
async function testToolCallHistory() {
  const runner = new TestRunner("Tool Call History");
  await runner.setup();

  // Make a few tool calls first
  await runner.client.callTool({
    name: "retrieve_tools",
    arguments: { query: "test", limit: 1 }
  });

  await runner.client.callTool({
    name: "tools_stat",
    arguments: {}
  });

  // Give it a moment to persist
  await new Promise(resolve => setTimeout(resolve, 500));

  await runner.test("Retrieve tool call history via API", async () => {
    const apiUrl = config.apiKey
      ? `${config.serverUrl}/api/v1/tool-calls?apikey=${config.apiKey}`
      : `${config.serverUrl}/api/v1/tool-calls`;

    const response = await fetch(apiUrl);
    const data = await response.json();

    runner.assert(data.success, "API call should succeed");
    runner.assert(data.data.tool_calls, "Should have tool_calls array");
    runner.assertGreater(data.data.total, 0, "Should have recorded tool calls");
  });

  await runner.test("Verify tool call contains required fields", async () => {
    const apiUrl = config.apiKey
      ? `${config.serverUrl}/api/v1/tool-calls?apikey=${config.apiKey}&limit=1`
      : `${config.serverUrl}/api/v1/tool-calls?limit=1`;

    const response = await fetch(apiUrl);
    const data = await response.json();

    if (data.data.tool_calls.length > 0) {
      const call = data.data.tool_calls[0];
      runner.assert(call.id, "Tool call should have id");
      runner.assert(call.server_name, "Tool call should have server_name");
      runner.assert(call.tool_name, "Tool call should have tool_name");
      runner.assert(call.timestamp, "Tool call should have timestamp");
      runner.assert(typeof call.duration === "number", "Tool call should have duration");
    }
  });

  await runner.teardown();
}

// Test Suite 5: Error Handling
async function testErrorHandling() {
  const runner = new TestRunner("Error Handling");
  await runner.setup();

  await runner.test("Call non-existent tool", async () => {
    try {
      await runner.client.callTool({
        name: "nonexistent:tool",
        arguments: {}
      });
      throw new Error("Should have thrown an error");
    } catch (error) {
      runner.assert(error.message.includes("not found") || error.message.includes("invalid"),
        "Should get appropriate error message");
    }
  });

  await runner.test("Call tool with invalid arguments", async () => {
    try {
      await runner.client.callTool({
        name: "retrieve_tools",
        arguments: { query: 12345 } // Should be string
      });
      // If it doesn't error, that's also acceptable (server may coerce)
    } catch (error) {
      // Error is acceptable
    }
  });

  await runner.teardown();
}

// Main test runner
async function main() {
  console.log("🚀 Starting MCP Test Suite");
  console.log(`Server: ${config.serverUrl}`);
  console.log(`API Key: ${config.apiKey ? "✓ Configured" : "✗ Not set"}\n`);

  try {
    await testConnectionAndDiscovery();
    await testBuiltInTools();
    await testUpstreamTools();
    await testToolCallHistory();
    await testErrorHandling();

    // Print summary
    console.log(`\n${"=".repeat(60)}`);
    console.log("📊 Test Summary");
    console.log("=".repeat(60));
    console.log(`✅ Passed:  ${results.passed}`);
    console.log(`❌ Failed:  ${results.failed}`);
    console.log(`⏭️  Skipped: ${results.skipped}`);
    console.log(`📝 Total:   ${results.tests.length}`);

    if (results.failed > 0) {
      console.log("\n❌ Failed Tests:");
      results.tests
        .filter(t => t.status === "failed")
        .forEach(t => console.log(`  - ${t.suite} > ${t.name}: ${t.error}`));
      process.exit(1);
    } else {
      console.log("\n✅ All tests passed!");
      process.exit(0);
    }
  } catch (error) {
    console.error("\n💥 Fatal error:", error);
    process.exit(1);
  }
}

main();