// Package contracts defines types for the version/update feature.
// This file is a design document - actual implementation will go in internal/updatecheck/types.go
package contracts

import "time"

// VersionInfo represents the current version and update availability.
// This is stored in-memory only and refreshed on startup + every 4 hours.
type VersionInfo struct {
	// CurrentVersion is the version of the running MCPProxy instance.
	// Format: semver with "v" prefix (e.g., "v1.2.3") or "development"
	CurrentVersion string `json:"current_version"`

	// LatestVersion is the latest version available on GitHub releases.
	// Nil if update check has not completed yet.
	LatestVersion *string `json:"latest_version,omitempty"`

	// UpdateAvailable is true if LatestVersion > CurrentVersion.
	// Computed via semver comparison.
	UpdateAvailable bool `json:"available"`

	// ReleaseURL is the URL to the GitHub release page for the latest version.
	ReleaseURL *string `json:"release_url,omitempty"`

	// CheckedAt is the timestamp of the last successful update check.
	CheckedAt *time.Time `json:"checked_at,omitempty"`

	// IsPrerelease indicates if the latest version is a prerelease.
	IsPrerelease bool `json:"is_prerelease,omitempty"`

	// CheckError contains the error message if the last check failed.
	// Empty string if no error.
	CheckError string `json:"check_error,omitempty"`
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

	// LatestVersion is the latest version (nil if not checked)
	LatestVersion *string `json:"latest_version"`

	// ReleaseURL is the GitHub release page URL
	ReleaseURL *string `json:"release_url"`

	// CheckedAt is when the last check occurred
	CheckedAt *time.Time `json:"checked_at"`

	// IsPrerelease indicates if latest is a prerelease
	IsPrerelease bool `json:"is_prerelease,omitempty"`

	// CheckError is set if the last check failed
	CheckError string `json:"check_error,omitempty"`
}

// ToAPIResponse converts VersionInfo to the API response format.
func (v *VersionInfo) ToAPIResponse() *InfoResponseUpdate {
	if v == nil {
		return nil
	}
	return &InfoResponseUpdate{
		Available:     v.UpdateAvailable,
		LatestVersion: v.LatestVersion,
		ReleaseURL:    v.ReleaseURL,
		CheckedAt:     v.CheckedAt,
		IsPrerelease:  v.IsPrerelease,
		CheckError:    v.CheckError,
	}
}
