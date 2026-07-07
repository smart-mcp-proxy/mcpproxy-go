// Package updatecheck provides centralized version checking against GitHub releases.
package updatecheck

import "time"

// VersionInfo represents the current version and update availability.
// This is stored in-memory only and refreshed on startup + every 4 hours.
type VersionInfo struct {
	// CurrentVersion is the version of the running MCPProxy instance.
	// Format: semver with "v" prefix (e.g., "v1.2.3") or "development"
	CurrentVersion string `json:"current_version"`

	// LatestVersion is the latest version available on GitHub releases.
	// Empty if update check has not completed yet.
	LatestVersion string `json:"latest_version,omitempty"`

	// UpdateAvailable is true if LatestVersion > CurrentVersion.
	// Computed via semver comparison.
	UpdateAvailable bool `json:"available"`

	// ReleaseURL is the URL to the GitHub release page for the latest version.
	ReleaseURL string `json:"release_url,omitempty"`

	// CheckedAt is the timestamp of the last successful update check.
	CheckedAt *time.Time `json:"checked_at,omitempty"`

	// IsPrerelease indicates if the latest version is a prerelease.
	IsPrerelease bool `json:"is_prerelease,omitempty"`

	// CheckError contains the error message if the last check failed.
	// Empty string if no error.
	CheckError string `json:"check_error,omitempty"`

	// InstallChannel identifies the distribution channel of the running
	// binary (homebrew, dmg, deb, rpm, docker, go-install,
	// windows-installer, tarball, or unknown). Detected once at startup and
	// always populated, even when no update is available (Spec 079 FR-008,
	// additive per FR-021).
	InstallChannel string `json:"install_channel,omitempty"`

	// UpdateCommand is the exact one-line update command for InstallChannel.
	// Only set when UpdateAvailable is true AND the channel has a safe
	// command (homebrew, deb, rpm, go-install); empty otherwise (Spec 079
	// FR-009, additive per FR-021). When the offered version is a
	// prerelease, only go-install gets a command (version-pinned) — the
	// package-manager channels serve stable artifacts only.
	UpdateCommand string `json:"update_command,omitempty"`
}

// GitHubRelease represents a release from the GitHub Releases API.
// This matches the structure returned by:
// - GET /repos/{owner}/{repo}/releases/latest
// - GET /repos/{owner}/{repo}/releases
type GitHubRelease struct {
	// TagName is the git tag for this release (e.g., "v1.2.3")
	TagName string `json:"tag_name"`

	// Name is the release title
	Name string `json:"name"`

	// Body is the release notes in markdown format
	Body string `json:"body"`

	// Prerelease indicates if this is a prerelease
	Prerelease bool `json:"prerelease"`

	// HTMLURL is the URL to view the release on GitHub
	HTMLURL string `json:"html_url"`

	// PublishedAt is the publication timestamp
	PublishedAt string `json:"published_at"`

	// Assets is the list of downloadable files
	Assets []Asset `json:"assets"`
}

// Asset represents a downloadable file attached to a release.
type Asset struct {
	// Name is the asset filename (e.g., "mcpproxy-v1.2.3-darwin-arm64.tar.gz")
	Name string `json:"name"`

	// BrowserDownloadURL is the direct download URL
	BrowserDownloadURL string `json:"browser_download_url"`

	// ContentType is the MIME type of the asset
	ContentType string `json:"content_type"`

	// Size is the file size in bytes
	Size int64 `json:"size"`
}

// InfoResponseUpdate is the update field added to the /api/v1/info response.
// This structure is serialized to JSON for API responses.
type InfoResponseUpdate struct {
	// Available indicates if an update is available
	Available bool `json:"available"`

	// LatestVersion is the latest version (empty if not checked)
	LatestVersion string `json:"latest_version,omitempty"`

	// ReleaseURL is the GitHub release page URL
	ReleaseURL string `json:"release_url,omitempty"`

	// CheckedAt is when the last check occurred
	CheckedAt *time.Time `json:"checked_at,omitempty"`

	// IsPrerelease indicates if latest is a prerelease
	IsPrerelease bool `json:"is_prerelease,omitempty"`

	// CheckError is set if the last check failed
	CheckError string `json:"check_error,omitempty"`

	// InstallChannel is the detected distribution channel (Spec 079 FR-008)
	InstallChannel string `json:"install_channel,omitempty"`

	// UpdateCommand is the channel's one-line update command, only present
	// when an update is available and the channel has one (Spec 079 FR-009)
	UpdateCommand string `json:"update_command,omitempty"`
}

// ToAPIResponse converts VersionInfo to the API response format.
func (v *VersionInfo) ToAPIResponse() *InfoResponseUpdate {
	if v == nil {
		return nil
	}
	return &InfoResponseUpdate{
		Available:      v.UpdateAvailable,
		LatestVersion:  v.LatestVersion,
		ReleaseURL:     v.ReleaseURL,
		CheckedAt:      v.CheckedAt,
		IsPrerelease:   v.IsPrerelease,
		CheckError:     v.CheckError,
		InstallChannel: v.InstallChannel,
		UpdateCommand:  v.UpdateCommand,
	}
}
