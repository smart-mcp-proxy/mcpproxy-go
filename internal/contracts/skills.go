package contracts

import "time"

// Spec 125 (skills over MCP), PR-1: wire types of the administrator-only
// skills endpoints under /api/v1/servers/{id}/skills.

// SkillFileInfo is one file of a skill as listed by the upstream and verified
// by mcpproxy.
type SkillFileInfo struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
	Text   bool   `json:"text"`
}

// SkillScanFinding is one finding of the skill scanner (empty in PR-1).
type SkillScanFinding struct {
	File     string `json:"file"`
	Offset   int64  `json:"offset"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Evidence string `json:"evidence"`
}

// SkillScanInfo is the scan result recorded for a skill's current content.
type SkillScanInfo struct {
	Verdict  string `json:"verdict"`
	Complete bool   `json:"complete"`
	// Incomplete says why Complete is false: size_cap, coverage or
	// interrupted; empty when the scan is complete.
	Incomplete string `json:"incomplete"`
	// ScannedAt is omitted when no scan of the current content exists.
	ScannedAt *time.Time `json:"scanned_at,omitempty"`
	// FindingsTruncated is true when more findings existed than are listed.
	FindingsTruncated bool               `json:"findings_truncated"`
	Findings          []SkillScanFinding `json:"findings"`
}

// SkillEntry is one skill of a server.
type SkillEntry struct {
	ID             string `json:"id"`
	URI            string `json:"uri"`
	Path           string `json:"path"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Status         string `json:"status"`
	Blocked        bool   `json:"blocked"`
	Listed         bool   `json:"listed"`
	Available      bool   `json:"available"`
	UnusableReason string `json:"unusable_reason"`
	CurrentHash    string `json:"current_hash"`
	ApprovedHash   string `json:"approved_hash"`
	// ApprovedAt is omitted when the skill was never approved.
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	ApprovedBy string     `json:"approved_by"`
	// ApprovedFiles is the file set of the approved copy; omitted when the
	// skill has no approved copy. `skills show --diff` compares it with Files.
	ApprovedFiles []SkillFileInfo `json:"approved_files,omitempty"`
	Files         []SkillFileInfo `json:"files"`
	TotalSize     int64           `json:"total_size"`
	Scan          SkillScanInfo   `json:"scan"`
}

// SkillsListResponse is the response of GET /api/v1/servers/{id}/skills and
// POST /api/v1/servers/{id}/skills/refresh. With enable_skills off only
// Enabled (false) and an empty Skills are meaningful.
type SkillsListResponse struct {
	Enabled bool `json:"enabled"`
	// Incarnation is the server's approval incarnation: send it back as
	// expected_incarnation on approve, block and unblock.
	Incarnation  int64        `json:"incarnation"`
	Server       string       `json:"server,omitempty"`
	Supported    bool         `json:"supported"`
	ExposeSkills *bool        `json:"expose_skills"`
	LastRefresh  *time.Time   `json:"last_refresh"`
	Error        string       `json:"error"`
	Truncated    bool         `json:"truncated"`
	Skills       []SkillEntry `json:"skills"`
}

// SkillContentResponse is the response of GET /api/v1/servers/{id}/skills/content.
type SkillContentResponse struct {
	Text   string `json:"text"`
	Offset int64  `json:"offset"`
	// NextOffset is the byte offset of the next chunk; absent at the end.
	NextOffset *int64 `json:"next_offset,omitempty"`
	Size       int64  `json:"size"`
	Digest     string `json:"digest"`
}

// SkillsErrorResponse is an error body that carries a stable error_code.
type SkillsErrorResponse struct {
	Success   bool   `json:"success"`
	Error     string `json:"error"`
	ErrorCode string `json:"error_code"`
	RequestID string `json:"request_id,omitempty"`
}

// SkillsApproveRequest is the body of POST /api/v1/servers/{id}/skills/approve.
type SkillsApproveRequest struct {
	// Skills are skill paths (the part of the id after "<server>:"), 1 to 200.
	Skills []string `json:"skills"`
	// ExpectedHashes maps each skill path to the current_hash the reviewer
	// saw. A skill without an entry is refused as unreviewed.
	ExpectedHashes map[string]string `json:"expected_hashes"`
	// ExpectedIncarnation binds the approval to the server the reviewer saw;
	// 0 (or absent) binds to the incarnation current when the request starts.
	ExpectedIncarnation int64 `json:"expected_incarnation,omitempty"`
}

// SkillsApproveResponse is the 200 body of the approve endpoint.
type SkillsApproveResponse struct {
	// Approved counts the skills newly approved; a skill already approved for
	// its current content is not counted.
	Approved int `json:"approved"`
}

// SkillsBlockRequest is the body of POST /api/v1/servers/{id}/skills/block and
// .../unblock.
type SkillsBlockRequest struct {
	Skills              []string `json:"skills"`
	ExpectedIncarnation int64    `json:"expected_incarnation,omitempty"`
}

// SkillsBlockResponse is the 200 body of the block and unblock endpoints.
type SkillsBlockResponse struct {
	// Changed counts the skills whose blocked mark changed.
	Changed int `json:"changed"`
}

// SkillsStaleReviewResponse is the 409 review_out_of_date body of the approve
// endpoint. Nothing was approved.
type SkillsStaleReviewResponse struct {
	Success    bool     `json:"success"`
	Error      string   `json:"error"`
	ErrorCode  string   `json:"error_code"`
	Changed    []string `json:"changed"`
	Unreviewed []string `json:"unreviewed"`
	RequestID  string   `json:"request_id,omitempty"`
}

// SkillsUnusableResponse is the 422 skill_unusable body of the approve
// endpoint. Nothing was approved.
type SkillsUnusableResponse struct {
	Success   bool     `json:"success"`
	Error     string   `json:"error"`
	ErrorCode string   `json:"error_code"`
	Skills    []string `json:"skills"`
	RequestID string   `json:"request_id,omitempty"`
}
