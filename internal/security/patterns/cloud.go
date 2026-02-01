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
// Requires keyword context to avoid false positives on random base64 strings
func awsSecretKeyPattern() *Pattern {
	// Pattern requires keyword context like: aws_secret_access_key=, AWS_SECRET_KEY:, "secretAccessKey":
	// Handles formats: key=value, key: value, "key": "value"
	return NewPattern("aws_secret_key").
		WithRegex(`(?i)(?:aws[_-]?secret[_-]?(?:access[_-]?)?key|secret[_-]?access[_-]?key|secretAccessKey)["']?\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})["']?`).
		WithCategory(CategoryCloudCredentials).
		WithSeverity(SeverityCritical).
		WithDescription("AWS secret access key").
		WithKnownExamples(
			"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", // AWS documentation example
		).
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
// Requires keyword context to avoid false positives
func azureClientSecretPattern() *Pattern {
	// Pattern requires keyword context like: AZURE_CLIENT_SECRET=, client_secret:, "clientSecret":
	// Handles formats: key=value, key: value, "key": "value"
	return NewPattern("azure_client_secret").
		WithRegex(`(?i)(?:azure[_-]?client[_-]?secret|client[_-]?secret|clientSecret|AZURE_SECRET)["']?\s*[:=]\s*["']?([a-zA-Z0-9~._-]{34,})["']?`).
		WithCategory(CategoryCloudCredentials).
		WithSeverity(SeverityHigh).
		WithDescription("Azure client secret / app password").
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
