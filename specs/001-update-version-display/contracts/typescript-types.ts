// TypeScript types for version/update feature
// To be added to frontend/src/types/contracts.ts (or auto-generated)

/**
 * Update information from the background GitHub release checker.
 * Null if update check has not completed yet.
 */
export interface UpdateInfo {
  /** True if a newer version is available */
  available: boolean;

  /** Latest version available (e.g., "v0.12.0"), null if check failed */
  latest_version: string | null;

  /** URL to the GitHub release page */
  release_url: string | null;

  /** ISO 8601 timestamp of last successful check */
  checked_at: string | null;

  /** True if the latest version is a prerelease */
  is_prerelease?: boolean;

  /** Error message if last check failed */
  check_error?: string;
}

/**
 * Extended response from GET /api/v1/info
 * Includes update information for tray/WebUI integration.
 */
export interface InfoResponse {
  /** Current MCPProxy version (e.g., "v0.11.0" or "development") */
  version: string;

  /** URL to access the Web Control Panel */
  web_ui_url: string;

  /** HTTP listen address */
  listen_addr: string;

  /** Endpoint addresses */
  endpoints: {
    /** HTTP endpoint */
    http: string;
    /** Unix socket path (empty if disabled) */
    socket: string;
  };

  /** Update information, null if check not completed */
  update: UpdateInfo | null;
}

/**
 * API response wrapper for InfoResponse
 */
export interface InfoAPIResponse {
  success: boolean;
  data?: InfoResponse;
  error?: string;
}

// Example usage in Vue component:
//
// const { data } = await api.get<InfoAPIResponse>('/api/v1/info');
// if (data.success && data.data?.update?.available) {
//   showUpdateBanner(data.data.update.latest_version, data.data.update.release_url);
// }
