package httpapi

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"

// Named aliases of the runtime view types, so the swag annotations of the
// profile routes can name them without the import alias (swag resolves a local
// type name; it does not see the `internalRuntime` alias).

// ProfileListData is the data of GET /api/v1/profiles.
type ProfileListData = runtime.ProfileList

// ProfileViewData is the data of GET /api/v1/profiles/{name}.
type ProfileViewData = runtime.ProfileView

// ProfileWriteData is the data of POST /api/v1/profiles and PUT /api/v1/profiles/{name}.
type ProfileWriteData = runtime.WriteResult

// ProfileRenameData is the data of POST /api/v1/profiles/{name}/rename.
type ProfileRenameData = runtime.RenameResult

// ProfileDeleteData is the data of DELETE /api/v1/profiles/{name}.
type ProfileDeleteData = runtime.DeleteResult

// ProfileTryData is the data of POST /api/v1/profiles/try.
type ProfileTryData = runtime.TryResult

// EffectiveToolsData is the data of GET /api/v1/profiles/{name}/effective-tools.
type EffectiveToolsData = runtime.EffectiveToolsResult

// AccessExplanationData is the data of GET /api/v1/access/explain.
type AccessExplanationData = runtime.AccessExplanation

// ClientWarning is one Clients-surface warning (code, severity, action).
type ClientWarning = runtime.Warning

// BulkAssignSkipped is a client a bulk assign did not move.
type BulkAssignSkipped = runtime.Skipped

// ProfileUsedByData is what points at a profile (administrators only).
type ProfileUsedByData = runtime.UsedBy
