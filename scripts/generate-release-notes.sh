#!/bin/bash
# Generate release notes using Claude API
# Usage: ./scripts/generate-release-notes.sh [version] [previous_tag]
# Environment: ANTHROPIC_API_KEY must be set
#
# This script can be used for:
# 1. Local testing before pushing tags
# 2. CI/CD integration via GitHub Actions
# 3. Manual release note generation

set -euo pipefail

# Configuration
CLAUDE_MODEL="${CLAUDE_MODEL:-claude-sonnet-4-5-20250929}"
MAX_TOKENS="${MAX_TOKENS:-1024}"
MAX_COMMITS="${MAX_COMMITS:-200}"
API_TIMEOUT="${API_TIMEOUT:-30}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check required tools
check_dependencies() {
    local missing=()

    if ! command -v curl &> /dev/null; then
        missing+=("curl")
    fi

    if ! command -v jq &> /dev/null; then
        missing+=("jq")
    fi

    if ! command -v git &> /dev/null; then
        missing+=("git")
    fi

    if [ ${#missing[@]} -gt 0 ]; then
        log_error "Missing required tools: ${missing[*]}"
        exit 1
    fi
}

# Check API key
check_api_key() {
    if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
        log_error "ANTHROPIC_API_KEY environment variable is not set"
        log_info "Get your API key from https://console.anthropic.com/"
        exit 1
    fi
}

# Get previous tag
get_previous_tag() {
    local current_tag="${1:-}"

    if [ -n "$current_tag" ]; then
        # Get the tag before the specified one
        git describe --tags --abbrev=0 "${current_tag}^" 2>/dev/null || echo ""
    else
        # Get the most recent tag
        git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo ""
    fi
}

# Collect commits between tags
collect_commits() {
    local prev_tag="${1:-}"
    local current_ref="${2:-HEAD}"
    local commits

    if [ -z "$prev_tag" ]; then
        # First release - get all commits
        log_info "No previous tag found, collecting all commits"
        commits=$(git log --pretty=format:"- %s" --no-merges "$current_ref" | head -n "$MAX_COMMITS")
    else
        # Get commits since previous tag
        log_info "Collecting commits from $prev_tag to $current_ref"
        commits=$(git log "${prev_tag}..${current_ref}" --pretty=format:"- %s" --no-merges | head -n "$MAX_COMMITS")
    fi

    # Check if commits were truncated
    local total_count
    if [ -z "$prev_tag" ]; then
        total_count=$(git rev-list --count --no-merges "$current_ref")
    else
        total_count=$(git rev-list --count --no-merges "${prev_tag}..${current_ref}")
    fi

    if [ "$total_count" -gt "$MAX_COMMITS" ]; then
        log_warn "Truncated to $MAX_COMMITS commits (total: $total_count)"
        commits="$commits

(Showing $MAX_COMMITS most recent of $total_count commits)"
    fi

    echo "$commits"
}

# Filter out noise commits
filter_commits() {
    local commits="$1"

    # Remove common noise patterns
    echo "$commits" | grep -v -E "^- (Merge branch|Merge pull request|Bump version|chore\(deps\)|chore\(release\))" || echo "$commits"
}

# Build the prompt for Claude
build_prompt() {
    local version="$1"
    local commits="$2"

    cat << EOF
Generate concise release notes for version ${version} of MCPProxy (Smart MCP Proxy).

MCPProxy is a desktop application that acts as a smart proxy for AI agents using the Model Context Protocol (MCP). It provides intelligent tool discovery, token savings, and security quarantine for MCP servers.

Commits since last release:
${commits}

Requirements:
- Maximum 400 words
- Use markdown format
- Start with a brief 1-2 sentence summary of this release
- Group changes into sections (use only sections that have content):
  - **New Features** - New functionality (feat: commits)
  - **Bug Fixes** - Fixed issues (fix: commits)
  - **Breaking Changes** - Changes requiring user action (BREAKING: or ! commits)
  - **Improvements** - Enhancements to existing features (improve:, perf:, refactor: commits)
- Skip internal changes (chore:, docs:, test:, ci: commits) unless significant
- Use bullet points for each change
- Be specific but brief - describe the user benefit, not implementation details
- If there are no meaningful changes, say "Minor internal improvements and maintenance updates."
EOF
}

# Call Claude API
call_claude_api() {
    local prompt="$1"
    local response
    local notes
    local error_msg

    log_info "Calling Claude API (model: $CLAUDE_MODEL)..."

    # Build JSON payload using jq to properly escape the prompt
    local payload
    payload=$(jq -n \
        --arg model "$CLAUDE_MODEL" \
        --argjson max_tokens "$MAX_TOKENS" \
        --arg prompt "$prompt" \
        '{
            model: $model,
            max_tokens: $max_tokens,
            messages: [{
                role: "user",
                content: $prompt
            }]
        }')

    # Make API call with timeout
    response=$(curl -s --max-time "$API_TIMEOUT" \
        https://api.anthropic.com/v1/messages \
        -H "x-api-key: $ANTHROPIC_API_KEY" \
        -H "content-type: application/json" \
        -H "anthropic-version: 2023-06-01" \
        -d "$payload" 2>&1) || {
        log_error "API request failed (timeout or network error)"
        return 1
    }

    # Check for error in response
    error_msg=$(echo "$response" | jq -r '.error.message // empty' 2>/dev/null || echo "")
    if [ -n "$error_msg" ]; then
        log_error "API error: $error_msg"
        return 1
    fi

    # Extract the text content
    notes=$(echo "$response" | jq -r '.content[0].text // empty' 2>/dev/null || echo "")

    if [ -z "$notes" ]; then
        log_error "Failed to extract release notes from API response"
        log_error "Response: $response"
        return 1
    fi

    echo "$notes"
}

# Generate fallback message
generate_fallback() {
    local version="$1"

    cat << EOF
## What's New in ${version}

Release notes could not be generated automatically. Please see the [commit history](../../commits/${version}) for detailed changes.

### Summary

This release includes various improvements and bug fixes. Check the commit log for specific changes.
EOF
}

# Main function
main() {
    local version="${1:-}"
    local prev_tag="${2:-}"

    check_dependencies
    check_api_key

    # Determine version
    if [ -z "$version" ]; then
        version=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
        log_info "Using detected version: $version"
    fi

    # Determine previous tag
    if [ -z "$prev_tag" ]; then
        prev_tag=$(get_previous_tag "$version")
        if [ -n "$prev_tag" ]; then
            log_info "Previous tag: $prev_tag"
        else
            log_info "No previous tag found (first release)"
        fi
    fi

    # Collect and filter commits
    local commits
    commits=$(collect_commits "$prev_tag" "$version")
    commits=$(filter_commits "$commits")

    if [ -z "$commits" ] || [ "$commits" = "-" ]; then
        log_warn "No commits found between tags"
        commits="- No changes since previous release"
    fi

    # Build prompt and call API
    local prompt
    prompt=$(build_prompt "$version" "$commits")

    local notes
    if notes=$(call_claude_api "$prompt"); then
        log_info "Release notes generated successfully"
        echo ""
        echo "$notes"
    else
        log_warn "Falling back to default message"
        generate_fallback "$version"
    fi
}

# Run main function
main "$@"
