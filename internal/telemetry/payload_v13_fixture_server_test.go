//go:build server

package telemetry

// newV13Fixture is the server-edition twin of the v13 fixture (Spec 107 US7):
// the block is enabled with a generic OIDC provider and a user counter that
// reports three users, so the build-tagged accessors must surface
// server_edition_enabled=true, idp_provider="oidc" and user_count_bucket="1-10".
func newV13Fixture() v13Fixture {
	return v13Fixture{
		cfg:         v13CanaryConfig(),
		userCounter: func() (int, error) { return 3, nil },
		wantEnabled: true,
		wantIdP:     "oidc",
		wantBucket:  "1-10",
	}
}
