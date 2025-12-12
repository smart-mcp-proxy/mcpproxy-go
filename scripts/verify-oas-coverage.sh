#!/bin/bash
# scripts/verify-oas-coverage.sh
#
# Verifies that all REST API endpoints in internal/httpapi/server.go are documented
# in the OpenAPI specification (oas/swagger.yaml).
#
# Usage:
#   ./scripts/verify-oas-coverage.sh
#
# Exit codes:
#   0 - All endpoints documented (100% coverage)
#   1 - Missing endpoints detected

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Paths
SERVER_GO="internal/httpapi/server.go"
CODE_EXEC_GO="internal/httpapi/code_exec.go"
OAS_YAML="oas/swagger.yaml"

# Check required files exist
if [[ ! -f "$SERVER_GO" ]]; then
    echo -e "${RED}❌ ERROR: $SERVER_GO not found${NC}"
    exit 1
fi

if [[ ! -f "$OAS_YAML" ]]; then
    echo -e "${RED}❌ ERROR: $OAS_YAML not found${NC}"
    exit 1
fi

echo "🔍 Extracting implemented routes from Go handlers..."

# Extract routes from server.go
# Matches patterns like: r.Get("/config", s.handleGetConfig)
# Captures HTTP method and path
# Extract routes, excluding:
# - /ui (web UI routes)
# - /swagger (Swagger UI routes)
# - /mcp (MCP protocol endpoints, unprotected by design)
ROUTES=$(grep -E '\br\.(Get|Post|Put|Delete|Patch|Head)\(' "$SERVER_GO" "$CODE_EXEC_GO" 2>/dev/null | \
    sed -E 's/.*r\.(Get|Post|Put|Delete|Patch|Head)\("([^"]+)".*/\U\1 \2/' | \
    grep -v '/ui' | \
    grep -v '/swagger' | \
    grep -v '/mcp' | \
    sort -u)

# Extract documented paths from OAS
echo "📋 Extracting documented endpoints from OpenAPI spec..."
OAS_PATHS=$(awk '/^  \//{path=$1; gsub(/:$/, "", path)} /^    (get|post|put|delete|patch|head):$/{method=toupper($1); gsub(/:$/, "", method); print method " " path}' "$OAS_YAML" | sort -u)

# Compare and find missing endpoints
echo "🔬 Comparing implemented vs documented endpoints..."

MISSING=$(comm -23 <(echo "$ROUTES") <(echo "$OAS_PATHS"))

# Report results
if [[ -z "$MISSING" ]]; then
    echo -e "${GREEN}✅ All REST endpoints documented in OAS${NC}"

    # Count stats
    TOTAL_ROUTES=$(echo "$ROUTES" | wc -l | tr -d ' ')
    echo ""
    echo "📊 Coverage Statistics:"
    echo "  Total endpoints:     $TOTAL_ROUTES"
    echo "  Documented:          $TOTAL_ROUTES"
    echo "  Coverage:            100%"
    exit 0
else
    echo -e "${RED}❌ Missing OAS documentation for:${NC}"
    echo "$MISSING" | sed 's/^/  /'
    echo ""

    # Count stats
    TOTAL_ROUTES=$(echo "$ROUTES" | wc -l | tr -d ' ')
    DOCUMENTED=$(echo "$OAS_PATHS" | wc -l | tr -d ' ')
    MISSING_COUNT=$(echo "$MISSING" | wc -l | tr -d ' ')
    COVERAGE=$(awk "BEGIN {printf \"%.1f\", ($DOCUMENTED / $TOTAL_ROUTES) * 100}")

    echo "📊 Coverage Statistics:"
    echo "  Total endpoints:     $TOTAL_ROUTES"
    echo "  Documented:          $DOCUMENTED"
    echo "  Missing:             $MISSING_COUNT"
    echo "  Coverage:            ${COVERAGE}%"
    echo ""
    echo -e "${YELLOW}💡 To fix: Add swag annotations to the missing endpoint handlers${NC}"
    echo -e "${YELLOW}   See: specs/001-oas-endpoint-documentation/quickstart.md${NC}"
    exit 1
fi
