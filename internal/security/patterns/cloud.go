package patterns

// GetCloudPatterns returns all cloud credential detection patterns
func GetCloudPatterns() []*Pattern {
	return []*Pattern{
		awsAccessKeyPattern(),
		awsSecretKeyPattern(),
		gcpAPIKeyPattern(),
		gcpServiceAccountPattern(),
		azureClientSecretPattern(),
		azureConnectionStringPattern(),
	}
}

// AWS Access Key patterns
// Valid prefixes: AKIA, ABIA, ACCA, AGPA, AIDA, AIPA, ANPA, ANVA, APKA, AROA, ASCA, ASIA
func awsAccessKeyPattern() *Pattern {
	// Comprehensive regex covering all AWS access key prefixes:
	// AKIA - Access Key ID
	// ABIA, ACCA - Other access key types
	// AGPA - Group ID
	// AIDA - IAM User ID
	// AIPA, ANPA, ANVA, APKA - Various identifier types
	// AROA - Role ID
	// ASCA - Certificate ID
	// ASIA - Temporary credentials
	return NewPattern("aws_access_key").
		WithRegex(`(?:AKIA|ABIA|ACCA|AGPA|AIDA|AIPA|ANPA|ANVA|APKA|AROA|ASCA|ASIA)[A-Z0-9]{16}`).
		WithCategory(CategoryCloudCredentials).
		WithSeverity(SeverityCritical).
		WithDescription("AWS access key ID").
		WithKnownExamples(
			"AKIAIOSFODNN7EXAMPLE",    // AWS documentation example
			"AKIAI44QH8DHBEXAMPLE",    // AWS documentation example
			"AKIAIOSFODNN7EXAMPLEKEY", // Another AWS example
		).
		Build()
}

// AWS Secret Key pattern
// Base64-encoded 40-character string
func awsSecretKeyPattern() *Pattern {
	return NewPattern("aws_secret_key").
		WithRegex(`[A-Za-z0-9/+=]{40}`).
		WithCategory(CategoryCloudCredentials).
		WithSeverity(SeverityCritical).
		WithDescription("AWS secret access key").
		WithKnownExamples(
			"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", // AWS documentation example
		).
		WithValidator(func(match string) bool {
			// Must contain at least one lowercase, one uppercase, and one digit/special
			hasLower := false
			hasUpper := false
			hasOther := false
			for _, c := range match {
				switch {
				case c >= 'a' && c <= 'z':
					hasLower = true
				case c >= 'A' && c <= 'Z':
					hasUpper = true
				case (c >= '0' && c <= '9') || c == '/' || c == '+' || c == '=':
					hasOther = true
				}
			}
			return hasLower && hasUpper && hasOther
		}).
		Build()
}

// GCP API Key pattern
// Starts with AIza, followed by alphanumeric characters
func gcpAPIKeyPattern() *Pattern {
	return NewPattern("gcp_api_key").
		WithRegex(`AIza[0-9A-Za-z_-]{35}`).
		WithCategory(CategoryCloudCredentials).
		WithSeverity(SeverityHigh).
		WithDescription("Google Cloud Platform API key").
		Build()
}

// GCP Service Account pattern
// Detects "type": "service_account" in JSON
func gcpServiceAccountPattern() *Pattern {
	return NewPattern("gcp_service_account").
		WithRegex(`"type"\s*:\s*"service_account"`).
		WithCategory(CategoryCloudCredentials).
		WithSeverity(SeverityCritical).
		WithDescription("GCP service account key file").
		Build()
}

// Azure Client Secret pattern
// Typically 34+ characters with special characters
func azureClientSecretPattern() *Pattern {
	return NewPattern("azure_client_secret").
		WithRegex(`[a-zA-Z0-9~._-]{34,}`).
		WithCategory(CategoryCloudCredentials).
		WithSeverity(SeverityHigh).
		WithDescription("Azure client secret / app password").
		WithValidator(func(match string) bool {
			// Must contain at least one special character (~ . _ -)
			for _, c := range match {
				if c == '~' || c == '.' || c == '_' || c == '-' {
					return true
				}
			}
			return false
		}).
		Build()
}

// Azure Connection String pattern
// Contains AccountKey=
func azureConnectionStringPattern() *Pattern {
	return NewPattern("azure_connection_string").
		WithRegex(`AccountKey=[A-Za-z0-9+/=]{20,}`).
		WithCategory(CategoryCloudCredentials).
		WithSeverity(SeverityCritical).
		WithDescription("Azure storage/service connection string").
		Build()
}
