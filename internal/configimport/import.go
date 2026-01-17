package configimport

import (
	"fmt"
	"time"
)

// Import parses configuration content and imports servers.
// It auto-detects the format, parses servers, maps them to MCPProxy format,
// and checks for duplicates.
func Import(content []byte, opts *ImportOptions) (*ImportResult, error) {
	if opts == nil {
		opts = &ImportOptions{}
	}

	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	// Step 1: Detect format
	var format ConfigFormat
	if opts.FormatHint != "" && opts.FormatHint != FormatUnknown {
		format = opts.FormatHint
	} else {
		detection, err := DetectFormat(content)
		if err != nil {
			return nil, err
		}
		format = detection.Format
	}

	// Step 2: Get parser and parse content
	parser := GetParser(format)
	if parser == nil {
		return nil, &ImportError{
			Type:    "unknown_format",
			Message: fmt.Sprintf("no parser available for format: %s", format),
		}
	}

	parsedServers, err := parser.Parse(content)
	if err != nil {
		return nil, err
	}

	// Step 3: Build result
	result := &ImportResult{
		Format:            format,
		FormatDisplayName: format.String(),
		Imported:          []*ImportedServer{},
		Skipped:           []SkippedServer{},
		Failed:            []FailedServer{},
		Warnings:          []string{},
	}

	// Build existing server name set for duplicate detection
	existingSet := make(map[string]bool)
	for _, name := range opts.ExistingServers {
		existingSet[name] = true
	}

	// Build filter set if server names are specified
	var filterSet map[string]bool
	if len(opts.ServerNames) > 0 {
		filterSet = make(map[string]bool)
		for _, name := range opts.ServerNames {
			filterSet[name] = true
		}
	}

	// Track servers found (for filter validation)
	foundServers := make(map[string]bool)

	// Step 4: Process each parsed server
	for _, parsed := range parsedServers {
		foundServers[parsed.Name] = true

		// Check filter
		if filterSet != nil && !filterSet[parsed.Name] {
			result.Skipped = append(result.Skipped, SkippedServer{
				Name:   parsed.Name,
				Reason: "filtered_out",
			})
			continue
		}

		// Validate server name
		if err := ValidServerName(parsed.Name); err != nil {
			// Try to sanitize
			sanitized, _ := SanitizeServerName(parsed.Name)
			if sanitized == "" {
				result.Failed = append(result.Failed, FailedServer{
					Name:    parsed.Name,
					Error:   "invalid_name",
					Details: err.Error(),
				})
				continue
			}
			// Use sanitized name with warning
			result.Warnings = append(result.Warnings, fmt.Sprintf("server '%s' renamed to '%s' due to invalid characters", parsed.Name, sanitized))
			parsed.Name = sanitized
		}

		// Check for duplicates
		if existingSet[parsed.Name] {
			result.Skipped = append(result.Skipped, SkippedServer{
				Name:   parsed.Name,
				Reason: "already_exists",
			})
			continue
		}

		// Map to ServerConfig
		serverConfig, skipped, warnings := MapToServerConfig(parsed, opts.Now)

		// Create imported server
		imported := &ImportedServer{
			Server:        serverConfig,
			SourceFormat:  parsed.SourceFormat,
			OriginalName:  parsed.Name,
			FieldsSkipped: skipped,
			Warnings:      warnings,
		}

		result.Imported = append(result.Imported, imported)

		// Add to existing set to detect duplicates within the same import
		existingSet[parsed.Name] = true
	}

	// Validate filter - check if requested servers were found
	for name := range filterSet {
		if !foundServers[name] {
			result.Warnings = append(result.Warnings, fmt.Sprintf("requested server '%s' not found in config", name))
		}
	}

	// Build summary
	result.Summary = ImportSummary{
		Total:    len(parsedServers),
		Imported: len(result.Imported),
		Skipped:  len(result.Skipped),
		Failed:   len(result.Failed),
	}

	return result, nil
}

// Preview is a convenience function that calls Import with Preview=true in options.
// It returns what would be imported without actually making changes.
func Preview(content []byte, opts *ImportOptions) (*ImportResult, error) {
	if opts == nil {
		opts = &ImportOptions{}
	}
	opts.Preview = true
	return Import(content, opts)
}

// GetAvailableServerNames returns the list of server names found in the config.
// This is useful for listing available servers when the requested server is not found.
func GetAvailableServerNames(content []byte, formatHint ConfigFormat) ([]string, error) {
	opts := &ImportOptions{
		FormatHint: formatHint,
	}

	result, err := Import(content, opts)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, result.Summary.Total)
	for _, s := range result.Imported {
		names = append(names, s.Server.Name)
	}
	for _, s := range result.Skipped {
		names = append(names, s.Name)
	}
	for _, s := range result.Failed {
		if s.Name != "" {
			names = append(names, s.Name)
		}
	}

	return names, nil
}
