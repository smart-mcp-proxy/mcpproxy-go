package registries

// This file exposes a narrowly-scoped test seam so that packages OTHER than
// registries (notably internal/httpapi, for the FR-007 caller-scoping tests
// on GET /catalog/search) can drive SearchAll against a small, deterministic,
// hermetic set of sources instead of the real default registry list (which
// would otherwise make every catalog test a live network call).
//
// It is intentionally named *ForTest and does nothing a caller would want in
// production; keeping it in a non-_test.go file is the only way to make it
// reachable from a sibling package's test binary (Go's export_test.go trick
// is package-local). Do NOT call this outside tests.

// SetRegistriesForTest replaces the effective registry list wholesale (no
// merge with the shipped defaults, unlike SetRegistriesFromConfig) and
// returns a restore func that reinstalls the previous list.
func SetRegistriesForTest(regs []RegistryEntry) (restore func()) {
	prev := registryList
	registryList = regs
	return func() { registryList = prev }
}

// AllowPrivateRegistryFetchForTest relaxes the SSRF guard (internal/registries
// SetAllowPrivateRegistryFetch, MCP-1076) so a test's registry fixture can be
// an httptest.Server on loopback, and returns a restore func.
func AllowPrivateRegistryFetchForTest() (restore func()) {
	prevForce, prev := testForceAllowPrivate.Load(), registryAllowPrivateFetch.Load()
	testForceAllowPrivate.Store(true)
	registryAllowPrivateFetch.Store(true)
	return func() {
		testForceAllowPrivate.Store(prevForce)
		registryAllowPrivateFetch.Store(prev)
	}
}

// SetPopularityProviderForTest installs p as the process-wide popularity
// provider (Spec 110 FR-010) and returns a restore func reinstalling
// whatever was previously installed (typically nil).
func SetPopularityProviderForTest(p PopularityProvider) (restore func()) {
	prev := getPopularityProvider()
	SetPopularityProvider(p)
	return func() { SetPopularityProvider(prev) }
}

// SetGitHubAPIBaseForTest overrides the GitHub API base URL new
// githubStarsProvider instances read at construction (default
// githubAPIBaseURLDefault), so a test can point NewGitHubStarsProvider at an
// httptest.Server. Combine with AllowPrivateRegistryFetchForTest, since the
// SSRF guard otherwise blocks a loopback target. Returns a restore func.
func SetGitHubAPIBaseForTest(base string) (restore func()) {
	prev := currentGitHubAPIBase()
	b := base
	githubAPIBaseOverride.Store(&b)
	return func() {
		p := prev
		githubAPIBaseOverride.Store(&p)
	}
}
