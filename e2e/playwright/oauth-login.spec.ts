import { test, expect } from '@playwright/test';

/**
 * OAuth E2E Browser Tests
 *
 * These tests verify the OAuth login flow through a browser.
 * The test server URL is passed via environment variable OAUTH_SERVER_URL.
 *
 * To run these tests:
 * 1. Start the OAuth test server (done by the Go test harness)
 * 2. Set OAUTH_SERVER_URL environment variable
 * 3. Run: npx playwright test
 */

// Get OAuth server URL from environment
const getServerUrl = () => {
  const url = process.env.OAUTH_SERVER_URL;
  if (!url) {
    throw new Error('OAUTH_SERVER_URL environment variable is required');
  }
  return url;
};

// Build authorization URL with PKCE
const buildAuthUrl = (serverUrl: string, params: {
  clientId: string;
  redirectUri: string;
  codeChallenge: string;
  state?: string;
  scope?: string;
}) => {
  const url = new URL('/authorize', serverUrl);
  url.searchParams.set('response_type', 'code');
  url.searchParams.set('client_id', params.clientId);
  url.searchParams.set('redirect_uri', params.redirectUri);
  url.searchParams.set('code_challenge', params.codeChallenge);
  url.searchParams.set('code_challenge_method', 'S256');
  if (params.state) url.searchParams.set('state', params.state);
  if (params.scope) url.searchParams.set('scope', params.scope);
  return url.toString();
};

test.describe('OAuth Login Flow', () => {
  // Skip tests if OAUTH_SERVER_URL is not set
  test.skip(() => !process.env.OAUTH_SERVER_URL, 'OAUTH_SERVER_URL not set');

  test('happy path - successful login and consent', async ({ page }) => {
    const serverUrl = getServerUrl();
    const clientId = process.env.OAUTH_CLIENT_ID || 'test-client';
    // Use the OAuth server's callback endpoint (same port)
    const redirectUri = `${serverUrl}/callback`;

    // Generate a simple code challenge for testing
    const codeChallenge = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';
    const state = 'test-state-' + Date.now();

    const authUrl = buildAuthUrl(serverUrl, {
      clientId,
      redirectUri,
      codeChallenge,
      state,
      scope: 'read write',
    });

    // Navigate to authorization page
    await page.goto(authUrl);

    // Verify login page loaded
    await expect(page.locator('h1')).toContainText('OAuth Test Server');

    // Fill in credentials
    await page.fill('#username', 'testuser');
    await page.fill('#password', 'testpass');

    // Verify consent checkbox is checked by default
    const consentCheckbox = page.locator('#consent');
    await expect(consentCheckbox).toBeChecked();

    // Click approve button
    await page.click('button[value="approve"]');

    // Wait for redirect to callback
    await page.waitForURL(/\/callback\?/);

    // Verify we're on the callback page with correct parameters
    await expect(page.locator('h1')).toContainText('OAuth Callback Received');

    // Verify the code was received
    const codeSpan = page.locator('#code');
    await expect(codeSpan).not.toBeEmpty();

    // Verify state matches
    const stateSpan = page.locator('#state');
    await expect(stateSpan).toHaveText(state);

    // Verify no error
    const errorSpan = page.locator('#error');
    await expect(errorSpan).toBeEmpty();
  });

  test('invalid password - shows error message', async ({ page }) => {
    const serverUrl = getServerUrl();
    const clientId = process.env.OAUTH_CLIENT_ID || 'test-client';
    const redirectUri = `${serverUrl}/callback`;
    const codeChallenge = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';

    const authUrl = buildAuthUrl(serverUrl, {
      clientId,
      redirectUri,
      codeChallenge,
    });

    await page.goto(authUrl);

    // Fill in wrong password
    await page.fill('#username', 'testuser');
    await page.fill('#password', 'wrongpassword');
    await page.click('button[value="approve"]');

    // Should stay on login page with error
    await expect(page.locator('.error-message')).toContainText('Invalid username or password');

    // Should still be on the authorize page
    expect(page.url()).toContain('/authorize');
  });

  test('consent denied - redirects with error', async ({ page }) => {
    const serverUrl = getServerUrl();
    const clientId = process.env.OAUTH_CLIENT_ID || 'test-client';
    const redirectUri = `${serverUrl}/callback`;
    const codeChallenge = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';
    const state = 'deny-test-' + Date.now();

    const authUrl = buildAuthUrl(serverUrl, {
      clientId,
      redirectUri,
      codeChallenge,
      state,
    });

    await page.goto(authUrl);

    // Fill in credentials
    await page.fill('#username', 'testuser');
    await page.fill('#password', 'testpass');

    // Click deny button
    await page.click('button[value="deny"]');

    // Wait for redirect to callback
    await page.waitForURL(/\/callback\?/);

    // Verify error response
    await expect(page.locator('h1')).toContainText('OAuth Callback Received');
    await expect(page.locator('#error')).toHaveText('access_denied');
    await expect(page.locator('#code')).toBeEmpty();
    await expect(page.locator('#state')).toHaveText(state);
  });

  test('uncheck consent - redirects with access_denied', async ({ page }) => {
    const serverUrl = getServerUrl();
    const clientId = process.env.OAUTH_CLIENT_ID || 'test-client';
    const redirectUri = `${serverUrl}/callback`;
    const codeChallenge = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';

    const authUrl = buildAuthUrl(serverUrl, {
      clientId,
      redirectUri,
      codeChallenge,
    });

    await page.goto(authUrl);

    // Fill in credentials
    await page.fill('#username', 'testuser');
    await page.fill('#password', 'testpass');

    // Uncheck consent
    await page.uncheck('#consent');

    // Click approve (but consent unchecked)
    await page.click('button[value="approve"]');

    // Wait for redirect to callback
    await page.waitForURL(/\/callback\?/);

    // Verify error response
    await expect(page.locator('#error')).toHaveText('access_denied');
  });

  test('displays requested scopes', async ({ page }) => {
    const serverUrl = getServerUrl();
    const clientId = process.env.OAUTH_CLIENT_ID || 'test-client';
    const redirectUri = `${serverUrl}/callback`;
    const codeChallenge = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';

    const authUrl = buildAuthUrl(serverUrl, {
      clientId,
      redirectUri,
      codeChallenge,
      scope: 'read write admin',
    });

    await page.goto(authUrl);

    // Verify scopes are displayed
    const consentSection = page.locator('.consent-section');
    await expect(consentSection).toContainText('read');
    await expect(consentSection).toContainText('write');
    await expect(consentSection).toContainText('admin');
  });

  test('displays client information', async ({ page }) => {
    const serverUrl = getServerUrl();
    const clientId = process.env.OAUTH_CLIENT_ID || 'test-client';
    const redirectUri = `${serverUrl}/callback`;
    const codeChallenge = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';

    const authUrl = buildAuthUrl(serverUrl, {
      clientId,
      redirectUri,
      codeChallenge,
    });

    await page.goto(authUrl);

    // Verify client info is displayed
    const clientInfo = page.locator('.client-info');
    await expect(clientInfo).toContainText('Client ID');
  });
});

test.describe('OAuth Discovery', () => {
  test.skip(() => !process.env.OAUTH_SERVER_URL, 'OAUTH_SERVER_URL not set');

  test('well-known endpoint returns valid metadata', async ({ request }) => {
    const serverUrl = getServerUrl();

    const response = await request.get(`${serverUrl}/.well-known/oauth-authorization-server`);
    expect(response.ok()).toBeTruthy();

    const metadata = await response.json();
    expect(metadata.issuer).toBe(serverUrl);
    expect(metadata.authorization_endpoint).toContain('/authorize');
    expect(metadata.token_endpoint).toContain('/token');
    expect(metadata.jwks_uri).toContain('/jwks.json');
    expect(metadata.code_challenge_methods_supported).toContain('S256');
  });

  test('JWKS endpoint returns valid keys', async ({ request }) => {
    const serverUrl = getServerUrl();

    const response = await request.get(`${serverUrl}/jwks.json`);
    expect(response.ok()).toBeTruthy();

    const jwks = await response.json();
    expect(jwks.keys).toHaveLength(1);
    expect(jwks.keys[0].kty).toBe('RSA');
    expect(jwks.keys[0].alg).toBe('RS256');
    expect(jwks.keys[0].use).toBe('sig');
  });
});
