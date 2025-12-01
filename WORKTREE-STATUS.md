# Worktree Status: Zero-Config OAuth

**Created**: 2025-11-27
**Branch**: `zero-config-oauth`
**Based On**: `origin/oauth-diagnostics-phase0`
**Location**: `/Users/josh.nichols/workspace/mcpproxy-go/.worktrees/zero-config-oauth`

## ✅ Setup Complete

The worktree has been successfully created and verified:

- ✅ Branch `zero-config-oauth` tracking `origin/oauth-diagnostics-phase0`
- ✅ Phase 0 commits present (OAuth diagnostics infrastructure)
- ✅ Build successful (`make build` completed)
- ✅ Binary created: `./mcpproxy`
- ✅ Ready for implementation

## 📋 Phase 0 Features (Inherited)

This worktree includes all Phase 0 OAuth diagnostics work:

### Infrastructure Already Present ✅

1. **OAuth Error Parsing** (`internal/upstream/core/connection.go`)
   - `OAuthParameterError` type
   - `parseOAuthError()` function
   - Detects missing `resource` parameter

2. **Config Serialization** (`internal/contracts/types.go`)
   - `OAuthConfig.ExtraParams` field
   - API serialization support

3. **Enhanced Diagnostics**
   - `cmd/mcpproxy/auth_cmd.go` - OAuth error display
   - `internal/management/diagnostics.go` - OAuth issue detection
   - `cmd/mcpproxy/doctor_cmd.go` - OAuth diagnostics output

4. **Test Coverage** (`internal/upstream/core/oauth_error_test.go`)
   - Error parsing tests
   - FastAPI validation error tests

### Commits Included

```
bbf125e feat: enhance OAuth diagnostics in auth status and doctor commands (Phase 0 Tasks 4-5)
e8a184c feat: add OAuth config serialization and error parsing (Phase 0 Tasks 1-3)
281f290 docs: Add OAuth extra parameters investigation and implementation plan
```

## 🎯 Implementation Plan

See: `docs/plans/2025-11-27-zero-config-oauth.md`

### Phase 1: Resource Parameter Extraction (Next Steps)

**Focus**: Extract resource parameter from Protected Resource Metadata

**Files to Modify**:
1. [ ] `internal/oauth/discovery.go` - Add `DiscoverProtectedResourceMetadata()`
2. [ ] `internal/oauth/config.go` - Extract resource in `CreateOAuthConfig()`
3. [ ] `internal/config/config.go` - Add `ExtraParams` to config schema
4. [ ] Tests for new functionality

**Goal**: Return `(OAuthConfig, extraParams)` from `CreateOAuthConfig()`

### Phase 2: OAuth Wrapper

**Focus**: Inject resource parameter into OAuth URLs

**Files to Create**:
1. [ ] `internal/oauth/wrapper.go` - NEW FILE
2. [ ] `internal/oauth/wrapper_test.go` - NEW FILE

**Files to Modify**:
1. [ ] `internal/upstream/core/connection.go` - Use wrapper in `tryOAuthAuth()`
2. [ ] `internal/transport/http.go` - Support wrapped clients

### Phase 3: Capability Detection

**Focus**: Detect OAuth without explicit config

**Files to Modify**:
1. [ ] `internal/oauth/config.go` - Add `IsOAuthCapable()`
2. [ ] `cmd/mcpproxy/auth_cmd.go` - Use new function
3. [ ] `internal/management/diagnostics.go` - Use new function

## 🚀 Quick Start

```bash
# Navigate to worktree
cd /Users/josh.nichols/workspace/mcpproxy-go/.worktrees/zero-config-oauth

# Build
make build

# Run tests
go test ./internal/oauth/... -v

# Test auth status (with daemon running)
./mcpproxy auth status

# Test doctor command
./mcpproxy doctor
```

## 📚 Documentation

**Implementation Plan**: `docs/plans/2025-11-27-zero-config-oauth.md`
**Branch Strategy**: `docs/plans/branch-strategy-zero-config-oauth.md`
**Auto-Detection Research**: `docs/oauth-auto-detection-analysis.md`
**Zero-Config Analysis**: `docs/zero-config-oauth-analysis.md`
**Summary**: `docs/oauth-implementation-summary.md`

## 🔄 Git Workflow

```bash
# View status
git status

# Create feature branch for Phase 1
git checkout -b feat/resource-parameter-extraction

# Make changes, commit
git add internal/oauth/discovery.go internal/oauth/config.go
git commit -m "feat: extract resource parameter from Protected Resource Metadata"

# Push when ready
git push -u origin zero-config-oauth

# Create PR (when all phases complete)
gh pr create --base main \
  --title "feat: zero-config OAuth with automatic resource parameter detection" \
  --body "Implements zero-config OAuth with RFC 8707 resource parameter auto-detection. See docs/plans/2025-11-27-zero-config-oauth.md"
```

## 📊 Progress Tracking

### Phase 1: Resource Extraction
- [ ] `DiscoverProtectedResourceMetadata()` function
- [ ] Extract resource in `CreateOAuthConfig()`
- [ ] Resource fallback logic
- [ ] Return extra params tuple
- [ ] Unit tests

### Phase 2: OAuth Wrapper
- [ ] Create wrapper file
- [ ] URL interception
- [ ] Integration in `tryOAuthAuth()`
- [ ] Integration tests

### Phase 3: Capability Detection
- [ ] `IsOAuthCapable()` function
- [ ] Update callers
- [ ] Documentation

### Phase 4: Testing
- [ ] Unit tests complete
- [ ] Integration tests complete
- [ ] E2E test with Runlayer

### Phase 5: Documentation
- [ ] User guide updated
- [ ] API docs updated
- [ ] Examples added

## 🎉 Success Criteria

- ✅ Zero-config OAuth works (no `"oauth": {}` needed)
- ✅ Resource parameter auto-detected from metadata
- ✅ Runlayer Slack MCP server authenticates successfully
- ✅ Backward compatible with existing configs
- ✅ MCP spec 2025-06-18 compliant

## 📝 Notes

- This worktree is based on `oauth-diagnostics-phase0` for 45% code reuse
- Phase 0 features (error parsing, diagnostics) already implemented
- Focus on net-new features: resource extraction + wrapper
- Estimated timeline: 2-3 weeks for full implementation

---

**Ready to start Phase 1!** 🚀

Start with: `docs/plans/2025-11-27-zero-config-oauth.md`
