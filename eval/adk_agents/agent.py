"""ADK Agent for MCP Proxy Evaluation

This agent connects to mcpproxy via HTTP to access MCP tools for evaluation.
The flow is: MCP servers → MCP registry → mcpproxy → StreamableHTTPConnectionParams → ADK agent
"""

import os
from google.adk.agents import LlmAgent
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, StreamableHTTPConnectionParams

# Load environment variables
MCPPROXY_URL = os.getenv("MCPPROXY_URL", "http://localhost:8080")

root_agent = LlmAgent(
    model='gemini-2.0-flash',
    name='assistant_agent',
    instruction='''You are a helpful AI assistant. You have access to various tools that can help you complete tasks and answer questions.

When a user asks you to do something:
1. Look at the available tools to see what capabilities you have
2. Use the appropriate tools to help the user
3. If you need additional capabilities that aren't available, let the user know
4. Be thorough and helpful in your responses

Always explain what you're doing and why, so the user understands your process.''',
    tools=[
        MCPToolset(
            connection_params=StreamableHTTPConnectionParams(
                url=MCPPROXY_URL,
                # Optional: Add headers if needed for authentication
                # headers={"Authorization": "Bearer token"}
            ),
            # Optional: Filter which tools are exposed to the agent
            # tool_filter=['upstream_servers', 'search_servers', 'quarantine_security']
        )
    ],
) 