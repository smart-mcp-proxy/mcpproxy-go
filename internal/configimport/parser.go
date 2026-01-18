package configimport

// Parser is the interface that all format-specific parsers implement.
type Parser interface {
	// Parse parses the configuration content and returns parsed servers.
	Parse(content []byte) ([]*ParsedServer, error)

	// Format returns the configuration format this parser handles.
	Format() ConfigFormat
}

// GetParser returns the appropriate parser for the given format.
func GetParser(format ConfigFormat) Parser {
	switch format {
	case FormatClaudeDesktop:
		return &ClaudeDesktopParser{}
	case FormatClaudeCode:
		return &ClaudeCodeParser{}
	case FormatCursor:
		return &CursorParser{}
	case FormatCodex:
		return &CodexParser{}
	case FormatGemini:
		return &GeminiParser{}
	default:
		return nil
	}
}
