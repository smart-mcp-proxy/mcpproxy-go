package patterns

// GetTokenPatterns returns all API token detection patterns
func GetTokenPatterns() []*Pattern {
	return []*Pattern{
		// GitHub tokens
		githubPATPattern(),
		githubOAuthPattern(),
		githubAppPattern(),
		githubRefreshPattern(),
		// GitLab tokens
		gitlabPATPattern(),
		// Stripe tokens
		stripeKeyPattern(),
		// Slack tokens
		slackTokenPattern(),
		// SendGrid
		sendgridKeyPattern(),
		// LLM/AI API keys
		openaiKeyPattern(),
		anthropicKeyPattern(),
		googleAIKeyPattern(),
		xaiKeyPattern(),
		groqKeyPattern(),
		huggingFaceTokenPattern(),
		huggingFaceOrgTokenPattern(),
		replicateKeyPattern(),
		perplexityKeyPattern(),
		fireworksKeyPattern(),
		anyscaleKeyPattern(),
		mistralKeyPattern(),
		cohereKeyPattern(),
		deepseekKeyPattern(),
		togetherAIKeyPattern(),
		// Generic tokens
		jwtTokenPattern(),
		bearerTokenPattern(),
	}
}

// GitHub Personal Access Token (classic and fine-grained)
func githubPATPattern() *Pattern {
	// ghp_ = classic PAT, github_pat_ = fine-grained PAT
	// Fine-grained format: github_pat_<base62>_<base62> (variable lengths)
	return NewPattern("github_pat").
		WithRegex(`(?:ghp_[a-zA-Z0-9]{36}|github_pat_[a-zA-Z0-9]+_[a-zA-Z0-9]{30,})`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("GitHub Personal Access Token").
		Build()
}

// GitHub OAuth Token
func githubOAuthPattern() *Pattern {
	return NewPattern("github_oauth").
		WithRegex(`gho_[a-zA-Z0-9]{36}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("GitHub OAuth access token").
		Build()
}

// GitHub App Installation Token
func githubAppPattern() *Pattern {
	return NewPattern("github_app").
		WithRegex(`ghs_[a-zA-Z0-9]{36}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("GitHub App installation access token").
		Build()
}

// GitHub App Refresh Token
func githubRefreshPattern() *Pattern {
	return NewPattern("github_refresh").
		WithRegex(`ghr_[a-zA-Z0-9]{36}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("GitHub App refresh token").
		Build()
}

// GitLab Personal Access Token
func gitlabPATPattern() *Pattern {
	return NewPattern("gitlab_pat").
		WithRegex(`glpat-[a-zA-Z0-9_-]{20,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("GitLab Personal Access Token").
		Build()
}

// Stripe API Key (secret, publishable, restricted)
func stripeKeyPattern() *Pattern {
	// sk_ = secret, pk_ = publishable, rk_ = restricted
	return NewPattern("stripe_key").
		WithRegex(`(?:sk|pk|rk)_(?:live|test)_[a-zA-Z0-9]{24,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Stripe API key").
		Build()
}

// Slack Token (bot, user, app) and webhook
func slackTokenPattern() *Pattern {
	// xoxb = bot, xoxp = user, xapp = app, hooks.slack.com = webhook
	return NewPattern("slack_token").
		WithRegex(`(?:xox[bpas]-[0-9A-Za-z-]+|xapp-[0-9]-[A-Z0-9]+-[0-9]+-[a-zA-Z0-9]+|https://hooks\.slack\.com/services/[A-Z0-9]+/[A-Z0-9]+/[a-zA-Z0-9]+)`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("Slack token or webhook URL").
		Build()
}

// SendGrid API Key
func sendgridKeyPattern() *Pattern {
	return NewPattern("sendgrid_key").
		WithRegex(`SG\.[a-zA-Z0-9_-]{20,}\.[a-zA-Z0-9_-]{40,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("SendGrid API key").
		Build()
}

// OpenAI API Key
// Formats: sk-proj-, sk-svcacct-, sk-admin-, sk- (legacy)
// Contains T3BlbkFJ signature (base64 of "OpenAI") in newer keys
func openaiKeyPattern() *Pattern {
	return NewPattern("openai_key").
		WithRegex(`sk-(?:proj-|svcacct-|admin-)?[a-zA-Z0-9_-]{32,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("OpenAI API key").
		Build()
}

// Anthropic API Key
// Format: sk-ant-api03-{93 chars}AA or sk-ant-admin01-{93 chars}AA
func anthropicKeyPattern() *Pattern {
	return NewPattern("anthropic_key").
		WithRegex(`sk-ant-(?:api03|admin01)-[a-zA-Z0-9_-]{20,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Anthropic API key").
		Build()
}

// Google AI / Gemini / Vertex AI API Key
// Format: AIzaSy followed by 33 characters
func googleAIKeyPattern() *Pattern {
	return NewPattern("google_ai_key").
		WithRegex(`AIzaSy[0-9A-Za-z_-]{33}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Google AI/Gemini API key").
		Build()
}

// xAI / Grok API Key
// Format: xai- prefix followed by 48+ alphanumeric characters
func xaiKeyPattern() *Pattern {
	return NewPattern("xai_key").
		WithRegex(`xai-[a-zA-Z0-9]{48,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("xAI/Grok API key").
		Build()
}

// Groq API Key
// Format: gsk_ prefix followed by 48 alphanumeric characters
func groqKeyPattern() *Pattern {
	return NewPattern("groq_key").
		WithRegex(`gsk_[a-zA-Z0-9]{48}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Groq API key").
		Build()
}

// Hugging Face User Access Token
// Format: hf_ prefix followed by 34 alphanumeric characters
func huggingFaceTokenPattern() *Pattern {
	return NewPattern("huggingface_token").
		WithRegex(`hf_[a-zA-Z0-9]{34}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Hugging Face access token").
		Build()
}

// Hugging Face Organization API Token
// Format: api_org_ prefix followed by 34 alphanumeric characters
func huggingFaceOrgTokenPattern() *Pattern {
	return NewPattern("huggingface_org_token").
		WithRegex(`api_org_[a-zA-Z0-9]{34}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Hugging Face organization API token").
		Build()
}

// Replicate API Token
// Format: r8_ prefix followed by 37 alphanumeric characters (40 total)
func replicateKeyPattern() *Pattern {
	return NewPattern("replicate_key").
		WithRegex(`r8_[a-zA-Z0-9]{37}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Replicate API token").
		Build()
}

// Perplexity API Key
// Format: pplx- prefix followed by 48 alphanumeric characters
func perplexityKeyPattern() *Pattern {
	return NewPattern("perplexity_key").
		WithRegex(`pplx-[a-zA-Z0-9]{48}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Perplexity API key").
		Build()
}

// Fireworks AI API Key
// Format: fw_ prefix followed by 20+ alphanumeric characters
func fireworksKeyPattern() *Pattern {
	return NewPattern("fireworks_key").
		WithRegex(`fw_[a-zA-Z0-9]{20,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Fireworks AI API key").
		Build()
}

// Anyscale API Key
// Format: esecret_ prefix followed by 20+ alphanumeric characters
func anyscaleKeyPattern() *Pattern {
	return NewPattern("anyscale_key").
		WithRegex(`esecret_[a-zA-Z0-9]{20,}`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityCritical).
		WithDescription("Anyscale API key").
		Build()
}

// Mistral AI API Key
// No unique prefix - uses keyword context for detection
// 32 alphanumeric characters
func mistralKeyPattern() *Pattern {
	return NewPattern("mistral_key").
		WithRegex(`(?i)(?:mistral|MISTRAL_API_KEY)['":\s=]+([a-zA-Z0-9]{32})`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("Mistral AI API key").
		Build()
}

// Cohere API Key
// No unique prefix - uses keyword context for detection
// 40 alphanumeric characters
func cohereKeyPattern() *Pattern {
	return NewPattern("cohere_key").
		WithRegex(`(?i)(?:cohere|CO_API_KEY|COHERE_API_KEY)['":\s=]+([a-zA-Z0-9]{40})`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("Cohere API key").
		Build()
}

// DeepSeek API Key
// Uses sk- prefix (shared with OpenAI) - uses keyword context
func deepseekKeyPattern() *Pattern {
	return NewPattern("deepseek_key").
		WithRegex(`(?i)(?:deepseek|DEEPSEEK_API_KEY)['":\s=]+(sk-[a-z0-9]{32})`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("DeepSeek API key").
		Build()
}

// Together AI API Key
// No known unique prefix - uses keyword context
func togetherAIKeyPattern() *Pattern {
	return NewPattern("together_key").
		WithRegex(`(?i)(?:together|TOGETHER_API_KEY)['":\s=]+([a-zA-Z0-9]{40,})`).
		WithCategory(CategoryAPIToken).
		WithSeverity(SeverityHigh).
		WithDescription("Together AI API key").
		Build()
}

// JWT Token (JSON Web Token)
func jwtTokenPattern() *Pattern {
	// JWT has 3 base64url parts separated by dots
	// Header starts with eyJ (base64 of '{"')
	return NewPattern("jwt_token").
		WithRegex(`eyJ[a-zA-Z0-9_-]*\.eyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]+`).
		WithCategory(CategoryAuthToken).
		WithSeverity(SeverityHigh).
		WithDescription("JSON Web Token (JWT)").
		Build()
}

// Bearer Token (generic)
func bearerTokenPattern() *Pattern {
	return NewPattern("bearer_token").
		WithRegex(`(?i)(?:bearer\s+)([a-zA-Z0-9_-]{20,})`).
		WithCategory(CategoryAuthToken).
		WithSeverity(SeverityMedium).
		WithDescription("Bearer authentication token").
		Build()
}
