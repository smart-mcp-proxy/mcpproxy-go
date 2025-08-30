#!/bin/bash

# Fix notarization for v0.6.6 release
VERSION="v0.6.6"
RELEASE_TAG="v0.6.6"
DMG_NAME="mcpproxy-0.6.6-darwin-arm64.dmg"

echo "=== Fixing notarization for ${VERSION} ==="

# Check for required environment variables
if [ -z "$APPLE_ID_USERNAME" ] || [ -z "$APPLE_ID_APP_PASSWORD" ] || [ -z "$APPLE_TEAM_ID" ]; then
    echo "❌ Missing required environment variables:"
    echo "   APPLE_ID_USERNAME"
    echo "   APPLE_ID_APP_PASSWORD" 
    echo "   APPLE_TEAM_ID"
    echo ""
    echo "Please set them and run again:"
    echo "export APPLE_ID_USERNAME='your-apple-id@example.com'"
    echo "export APPLE_ID_APP_PASSWORD='your-app-specific-password'"
    echo "export APPLE_TEAM_ID='your-team-id'"
    exit 1
fi

# Step 1: Download the DMG from the release
echo "1. Downloading DMG from release..."
mkdir -p temp
gh release download "${RELEASE_TAG}" --pattern "${DMG_NAME}" --dir temp/

if [ ! -f "temp/${DMG_NAME}" ]; then
    echo "❌ Failed to download ${DMG_NAME}"
    exit 1
fi

echo "✅ Downloaded ${DMG_NAME}"

# Step 2: Resubmit for notarization
echo "2. Resubmitting for notarization..."

# Submit with better error handling
SUBMISSION_OUTPUT=$(xcrun notarytool submit "temp/${DMG_NAME}" \
    --apple-id "${APPLE_ID_USERNAME}" \
    --password "${APPLE_ID_APP_PASSWORD}" \
    --team-id "${APPLE_TEAM_ID}" \
    --output-format json 2>&1)

if [ $? -eq 0 ]; then
    SUBMISSION_ID=$(echo "$SUBMISSION_OUTPUT" | jq -r '.id')
    
    if [ -n "$SUBMISSION_ID" ] && [ "$SUBMISSION_ID" != "null" ]; then
        echo "✅ Resubmission successful"
        echo "New Submission ID: ${SUBMISSION_ID}"
        
        # Step 3: Create corrected pending file
        echo "3. Creating corrected pending file..."
        cat > "${DMG_NAME}.pending" << EOF
{
  "submission_id": "$SUBMISSION_ID",
  "dmg_name": "$DMG_NAME",
  "version": "$VERSION",
  "submitted_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
        
        # Step 4: Upload corrected pending file to release
        echo "4. Uploading corrected pending file..."
        gh release upload "${RELEASE_TAG}" "${DMG_NAME}.pending" --clobber
        
        echo "✅ Fixed notarization for ${DMG_NAME}"
        echo "Submission ID: ${SUBMISSION_ID}"
        echo ""
        echo "The notarization check workflow will now be able to track this submission."
        
        # Clean up
        rm -rf temp/
        rm -f "${DMG_NAME}.pending"
        
    else
        echo "❌ Failed to extract valid submission ID"
        echo "Output: $SUBMISSION_OUTPUT"
        exit 1
    fi
else
    echo "❌ Notarization submission failed"
    echo "Output: $SUBMISSION_OUTPUT"
    exit 1
fi