#!/bin/bash
# Test script for PR Artifacts Comment workflow
# Usage: ./scripts/test-pr-artifacts-workflow.sh <PR_NUMBER>

set -euo pipefail

PR_NUMBER="${1:-}"

if [ -z "$PR_NUMBER" ]; then
  echo "Usage: $0 <PR_NUMBER>"
  echo "Example: $0 163"
  exit 1
fi

echo "🔍 Testing PR Artifacts Comment workflow for PR #$PR_NUMBER"
echo ""

# 1. Check if PR Build workflow completed
echo "Step 1: Checking PR Build workflow status..."
BUILD_RUNS=$(gh run list --workflow="PR Build" --json number,status,conclusion,databaseId \
  --jq ".[] | select(.number == $PR_NUMBER)")

if [ -z "$BUILD_RUNS" ]; then
  echo "❌ No PR Build workflow runs found for PR #$PR_NUMBER"
  exit 1
fi

BUILD_STATUS=$(echo "$BUILD_RUNS" | jq -r '.status')
BUILD_CONCLUSION=$(echo "$BUILD_RUNS" | jq -r '.conclusion')
BUILD_ID=$(echo "$BUILD_RUNS" | jq -r '.databaseId')

echo "   Status: $BUILD_STATUS"
echo "   Conclusion: $BUILD_CONCLUSION"
echo "   Run ID: $BUILD_ID"

if [ "$BUILD_STATUS" != "completed" ]; then
  echo "⏳ PR Build workflow is still running. Wait for it to complete."
  exit 0
fi

if [ "$BUILD_CONCLUSION" != "success" ]; then
  echo "❌ PR Build workflow did not succeed. Artifacts Comment won't trigger."
  exit 1
fi

echo "✅ PR Build workflow completed successfully"
echo ""

# 2. Check if PR Artifacts Comment workflow triggered
echo "Step 2: Checking PR Artifacts Comment workflow..."
COMMENT_RUNS=$(gh run list --workflow="PR Artifacts Comment" --limit 10 \
  --json workflowDatabaseId,status,conclusion,createdAt,url)

if [ -z "$COMMENT_RUNS" ] || [ "$COMMENT_RUNS" = "[]" ]; then
  echo "⚠️  No PR Artifacts Comment workflow runs found yet."
  echo "   This is expected if the workflow file isn't in the main branch yet."
  echo "   The workflow needs to be merged to main to trigger on future PRs."
  exit 0
fi

echo "✅ Found PR Artifacts Comment workflow runs:"
echo "$COMMENT_RUNS" | jq -r '.[] | "   - \(.status) (\(.conclusion // "in progress")) - \(.url)"'
echo ""

# 3. Check for artifact comment on the PR
echo "Step 3: Checking for artifact comment on PR #$PR_NUMBER..."
COMMENT=$(gh pr view "$PR_NUMBER" --json comments \
  --jq '.comments[] | select(.author.login == "github-actions[bot]" and (.body | contains("mcpproxy-pr-artifacts"))) | .body')

if [ -z "$COMMENT" ]; then
  echo "❌ No artifact comment found on PR #$PR_NUMBER"
  echo "   Expected: Comment from github-actions[bot] with artifact links"
  exit 1
fi

echo "✅ Found artifact comment:"
echo "$COMMENT" | head -20
echo ""

# 4. Verify artifacts exist
echo "Step 4: Verifying artifacts were uploaded..."
ARTIFACTS=$(gh run view "$BUILD_ID" --json artifacts --jq '.artifacts | length')

if [ "$ARTIFACTS" -eq 0 ]; then
  echo "❌ No artifacts found for run $BUILD_ID"
  exit 1
fi

echo "✅ Found $ARTIFACTS artifacts"
echo ""

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🎉 All tests passed! The PR Artifacts Comment workflow is working correctly."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
