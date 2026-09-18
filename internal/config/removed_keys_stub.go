//go:build !server

package config

// ValidateRemovedKeys is a no-op in the personal edition: the server-edition
// blocks are opaque JSON carriers there (Spec 107 FR-040) and nothing is
// normalised, validated or refused.
func ValidateRemovedKeys(_ map[string]any) []ValidationError {
	return nil
}

// normalizeLoadedDocument is a no-op in the personal edition: the file bytes
// are decoded exactly as read and no LoadDiagnostic is ever recorded.
func normalizeLoadedDocument(data []byte) ([]byte, []LoadDiagnostic, error) {
	return data, nil, nil
}
