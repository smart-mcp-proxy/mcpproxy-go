package skills

import "fmt"

// ExtensionKey is the capabilities.extensions key an upstream announces to say
// it serves skills (SEP-2640).
const ExtensionKey = "io.modelcontextprotocol/skills"

// Bounds that keep one upstream from making mcpproxy read, store or scan
// without limit. They are constants, not config, so every layer agrees.
const (
	MaxFilesPerSkill   = 512
	MaxSkillBytes      = 16 << 20
	MaxSkillsPerServer = 200
	// MaxUnlistedSkillsPerServer bounds the records kept for skills a server
	// no longer lists, so a server that keeps renaming its skills cannot grow
	// the store. Blocked records are kept beyond it.
	MaxUnlistedSkillsPerServer = 200
	MaxListPages               = 50
	MaxListResponseBytes       = 4 << 20
	// MaxListBytes bounds all pages of one listing together.
	MaxListBytes           = 16 << 20
	MaxStoredTextPerServer = 64 << 20
	MaxReadPerRefresh      = 256 << 20
	MaxFrontmatterBytes    = 64 << 10
	MaxScanBytesPerSkill   = 1 << 20
	MaxURIBytes            = 2048
)

// The bound that cut a listing short.
const (
	BoundPages         = "pages"
	BoundEntries       = "entries"
	BoundResponseBytes = "response_bytes"
)

// TruncatedError reports that a listing stopped at a bound. It travels next to
// the partial result so the caller can record the listing as truncated rather
// than complete.
type TruncatedError struct {
	Bound string
	Limit int64
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("skills listing truncated: %s limit %d reached", e.Bound, e.Limit)
}
