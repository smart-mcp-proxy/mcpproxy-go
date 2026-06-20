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
		originalName := parsed.Name

		// Resolve the effective (sanitized) name BEFORE filtering and dedup.
		// The Web UI's two-step flow surfaces the sanitized name in the preview,
		// so its server_names filter carries the sanitized name; the CLI's
		// --server flag carries the raw source name verbatim. We must match
		// either and track both, otherwise a name that needs sanitizing (e.g.
		// "Figma Desktop" -> "Figma_Desktop") gets silently dropped on whichever
		// path doesn't happen to match.
		effectiveName, _ := SanitizeServerName(originalName)
		nameUnsanitizable := effectiveName == ""
		if nameUnsanitizable {
			effectiveName = originalName
		}

		foundServers[originalName] = true
		foundServers[effectiveName] = true

		// Check filter against either the raw source name (CLI --server) or the
		// sanitized name the Web UI selected from the preview.
		if filterSet != nil && !filterSet[originalName] && !filterSet[effectiveName] {
			result.Skipped = append(result.Skipped, SkippedServer{
				Name:   effectiveName,
				Reason: "filtered_out",
			})
			continue
		}

		// Now that we know this server was selected, fail it if its name could
		// not be sanitized into anything valid.
		if nameUnsanitizable {
			result.Failed = append(result.Failed, FailedServer{
				Name:    originalName,
				Error:   "invalid_name",
				Details: ValidServerName(originalName).Error(),
			})
			continue
		}

		// Emit a rename warning when sanitization changed the name.
		if effectiveName != originalName {
			result.Warnings = append(result.Warnings, fmt.Sprintf("server '%s' renamed to '%s' due to invalid characters", originalName, effectiveName))
		}
		parsed.Name = effectiveName

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

		// Override quarantine if SkipQuarantine is set
		if opts.SkipQuarantine {
			serverConfig.Quarantined = false
		}

		// Create imported server
		imported := &ImportedServer{
			Server:        serverConfig,
			SourceFormat:  parsed.SourceFormat,
			OriginalName:  originalName,
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
