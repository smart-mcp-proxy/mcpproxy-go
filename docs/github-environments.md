# GitHub Environments Setup

This document provides step-by-step instructions for setting up GitHub Environments for production and staging deployments.

## Overview

GitHub Environments allow you to:
- Separate secrets between production and staging
- Add protection rules and approval requirements
- Control deployment access and timing
- Monitor deployment history

## Setup Instructions

### 1. Access Repository Settings

1. Go to your GitHub repository
2. Click on **Settings** tab
3. In the left sidebar, click **Environments**

### 2. Create Production Environment

1. Click **New environment**
2. Name it `production`
3. Click **Configure environment**

#### Protection Rules for Production:
- **Required reviewers**: Add team members who must approve production deployments
- **Wait timer**: Consider adding a 5-10 minute wait timer for production deployments
- **Deployment branches**: Restrict to `main` branch only
  - Select "Selected branches"
  - Add rule: `main`

#### Environment Secrets for Production:
Add production-specific secrets:
- `DOCKER_REGISTRY_TOKEN` - Production registry access
- `DEPLOY_KEY` - Production deployment key  
- `API_KEYS` - Production API keys
- Any other production-specific configuration

### 3. Create Staging Environment

1. Click **New environment** 
2. Name it `staging`
3. Click **Configure environment**

#### Protection Rules for Staging:
- **Deployment branches**: Restrict to `next` branch
  - Select "Selected branches" 
  - Add rule: `next`
- No required reviewers needed for staging
- No wait timer needed

#### Environment Secrets for Staging:
Add staging-specific secrets:
- `DOCKER_REGISTRY_TOKEN` - Staging registry access  
- `DEPLOY_KEY` - Staging deployment key
- `API_KEYS` - Staging/test API keys
- Any other staging-specific configuration

### 4. Update GitHub Actions Workflows

Modify your deployment workflows to use environments:

```yaml
# Example: .github/workflows/deploy-production.yml
name: Deploy to Production
on:
  push:
    branches: [main]
    tags: ['v*']

jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: production  # <- This connects to the environment
    steps:
      - uses: actions/checkout@v4
      - name: Deploy
        run: |
          echo "Deploying to production..."
          # Use secrets like: ${{ secrets.DEPLOY_KEY }}
```

```yaml
# Example: .github/workflows/deploy-staging.yml  
name: Deploy to Staging
on:
  push:
    branches: [next]

jobs:
  deploy:
    runs-on: ubuntu-latest
    environment: staging  # <- This connects to the environment
    steps:
      - uses: actions/checkout@v4
      - name: Deploy
        run: |
          echo "Deploying to staging..."
          # Use secrets like: ${{ secrets.DEPLOY_KEY }}
```

### 5. Verification

After setup, verify:

1. **Environments appear in Settings > Environments**
2. **Branch protection rules are active**
   - Try deploying from wrong branch - should fail
3. **Secrets are properly scoped**
   - Production secrets only available in production environment
   - Staging secrets only available in staging environment
4. **Approval requirements work (if configured)**
   - Production deployments wait for approval
   - Staging deployments proceed automatically

### 6. Best Practices

1. **Separate Secrets**: Never share secrets between environments
2. **Branch Restrictions**: Always restrict deployment branches
3. **Approval Gates**: Require human approval for production
4. **Monitoring**: Set up deployment notifications
5. **Documentation**: Keep environment configuration documented
6. **Regular Review**: Audit environment access and secrets quarterly

## Environment Variables in Workflows

Reference environment-specific secrets in your workflows:

```yaml
- name: Deploy Application
  env:
    DEPLOY_KEY: ${{ secrets.DEPLOY_KEY }}
    API_KEY: ${{ secrets.API_KEY }}
    DATABASE_URL: ${{ secrets.DATABASE_URL }}
  run: |
    ./deploy.sh
```

## Troubleshooting

**Environment not found**: Ensure the workflow references the exact environment name (case-sensitive)

**Secret not available**: Verify the secret exists in the specific environment, not at repository level

**Branch restriction failed**: Check that the deployment branch matches the environment's branch protection rules

**Approval hanging**: Ensure required reviewers have repository access and notification settings enabled