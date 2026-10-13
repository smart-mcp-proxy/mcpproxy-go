package tray

// ProfileInfo is a tray-facing summary of a configured profile (Profiles v2 T5).
// Name is the profile slug; an empty active profile means "all servers". It lives
// in a build-constraint-free file so the cross-platform tray API adapter can
// reference it even in stub (headless/linux) builds of this package.
type ProfileInfo struct {
	Name      string
	ToolCount int
}
