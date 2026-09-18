//go:build !server

package telemetry

// newV13Fixture is the personal-edition twin of the v13 fixture (Spec 107
// US7): the same config bytes are loaded — the server_edition block is an
// opaque carrier here (FR-040) — but the stub accessors must report
// server_edition_enabled=false and idp_provider="none", and with no user
// counter installed user_count_bucket is "0".
func newV13Fixture() v13Fixture {
	return v13Fixture{
		cfg:         v13CanaryConfig(),
		userCounter: nil,
		wantEnabled: false,
		wantIdP:     "none",
		wantBucket:  "0",
	}
}
