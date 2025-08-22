#!/usr/bin/env python3
"""
Test script to verify ADK agent setup

This script tests that the agent can be imported and initialized properly.
Run with: python test_agent.py
"""

import os
import sys
from pathlib import Path

# Add the current directory to Python path for imports
sys.path.insert(0, str(Path(__file__).parent))

def test_agent_import():
    """Test that the agent can be imported successfully"""
    try:
        from agent import root_agent
        print("✅ Agent imported successfully!")
        print(f"Agent name: {root_agent.name}")
        print(f"Model: {root_agent.model}")
        print(f"Tools: {len(root_agent.tools)} tool(s) configured")
        return True
    except ImportError as e:
        print(f"❌ Failed to import agent: {e}")
        print("Make sure dependencies are installed: uv sync")
        return False
    except Exception as e:
        print(f"❌ Error initializing agent: {e}")
        return False

def test_environment():
    """Test that required environment variables are set"""
    mcpproxy_url = os.getenv("MCPPROXY_URL", "http://localhost:8080")
    google_api_key = os.getenv("GOOGLE_AI_API_KEY")
    
    print(f"MCPPROXY_URL: {mcpproxy_url}")
    
    if google_api_key:
        print("✅ GOOGLE_AI_API_KEY is set")
    else:
        print("⚠️  GOOGLE_AI_API_KEY not set (required for full functionality)")
        print("   Create .env file with your API key")
    
    return True

def main():
    """Main test function"""
    print("🧪 Testing ADK Agent Setup")
    print("=" * 40)
    
    print("\n📋 Environment Check:")
    test_environment()
    
    print("\n🤖 Agent Import Test:")
    success = test_agent_import()
    
    if success:
        print("\n🎉 Agent setup test passed!")
        print("Ready to use with 'adk web eval/adk_agents'")
    else:
        print("\n❌ Agent setup test failed!")
        print("Please check the installation and configuration")
    
    return 0 if success else 1

if __name__ == "__main__":
    sys.exit(main()) 