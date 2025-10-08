# Release Process

This document describes the release and hotfix process for MCPProxy.

## Branch Model

- **`main`** - Production-ready code, always deployable
- **`next`** - Integration branch for ongoing development and refactoring work
- **`hotfix/x.y.z`** - Emergency fixes for production issues

## Hotfix Process

When a critical bug is discovered in production:

### 1. Create Hotfix Branch
```bash
# Create hotfix branch from the latest production tag
git checkout tags/vX.Y.Z
git checkout -b hotfix/X.Y.Z+1
```

### 2. Apply Fix
- Make the minimal necessary changes to fix the issue
- Test thoroughly in isolation
- Update version numbers if needed

### 3. Create and Tag Release
```bash
# Commit your changes
git add .
git commit -m "hotfix: fix critical issue description"

# Tag the hotfix release
git tag -a vX.Y.Z+1 -m "Release vX.Y.Z+1: hotfix for critical issue"

# Push tag and branch
git push origin hotfix/X.Y.Z+1
git push origin vX.Y.Z+1
```

### 4. Merge Back to Main
```bash
# Switch to main and merge the hotfix
git checkout main
git merge hotfix/X.Y.Z+1
git push origin main
```

### 5. Backport to Next
**IMPORTANT**: All hotfixes must be backported to the `next` branch to ensure ongoing development includes the fix.

```bash
# Switch to next and merge the hotfix
git checkout next  
git merge hotfix/X.Y.Z+1
git push origin next
```

### 6. Clean Up
```bash
# Delete the hotfix branch (optional)
git branch -d hotfix/X.Y.Z+1
git push origin --delete hotfix/X.Y.Z+1
```

## Regular Release Process

### From Next to Main
When ready to release accumulated features from `next`:

1. Create a release PR from `next` → `main`
2. Run full test suite and integration tests
3. Update version numbers and changelog
4. Merge to `main` after approval
5. Tag the release on `main`
6. Deploy to production environment

### Development Workflow
- Feature branches should be created from and merged into `next`
- `main` should only receive hotfixes and vetted releases from `next`
- All hotfixes applied to `main` must be backported to `next`

## Environment Deployment

- **Production Environment**: Deploys from `main` branch tags
- **Staging Environment**: Deploys from `next` branch for testing

## Best Practices

1. Keep hotfixes minimal and focused
2. Always test hotfixes in staging first if possible
3. Document the issue and fix in the commit message
4. Never forget to backport hotfixes to `next`
5. Use semantic versioning for all releases
6. Maintain a changelog for all releases