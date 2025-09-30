package tokens

// ModelEncoding represents the mapping between model names and their tiktoken encodings
type ModelEncoding struct {
	Model    string
	Encoding string
}

// Common model encodings based on tiktoken documentation
var modelEncodings = map[string]string{
	// GPT-4o and GPT-4.5 series - o200k_base
	"gpt-4o":           "o200k_base",
	"gpt-4o-mini":      "o200k_base",
	"gpt-4.1":          "o200k_base",
	"gpt-4.5":          "o200k_base",
	"gpt-4o-2024-05-13": "o200k_base",
	"gpt-4o-2024-08-06": "o200k_base",

	// GPT-4 and GPT-3.5 series - cl100k_base
	"gpt-4":                  "cl100k_base",
	"gpt-4-turbo":            "cl100k_base",
	"gpt-4-turbo-preview":    "cl100k_base",
	"gpt-4-0125-preview":     "cl100k_base",
	"gpt-4-1106-preview":     "cl100k_base",
	"gpt-4-32k":              "cl100k_base",
	"gpt-3.5-turbo":          "cl100k_base",
	"gpt-3.5-turbo-16k":      "cl100k_base",
	"gpt-3.5-turbo-0125":     "cl100k_base",
	"gpt-3.5-turbo-1106":     "cl100k_base",
	"text-embedding-ada-002": "cl100k_base",
	"text-embedding-3-small": "cl100k_base",
	"text-embedding-3-large": "cl100k_base",

	// Codex series - p50k_base
	"code-davinci-002": "p50k_base",
	"code-davinci-001": "p50k_base",
	"code-cushman-002": "p50k_base",
	"code-cushman-001": "p50k_base",

	// Older GPT-3 series - r50k_base (gpt2)
	"text-davinci-003": "r50k_base",
	"text-davinci-002": "r50k_base",
	"text-davinci-001": "r50k_base",
	"text-curie-001":   "r50k_base",
	"text-babbage-001": "r50k_base",
	"text-ada-001":     "r50k_base",
	"davinci":          "r50k_base",
	"curie":            "r50k_base",
	"babbage":          "r50k_base",
	"ada":              "r50k_base",

	// Claude models - use cl100k_base as approximation (not official)
	// Note: These are approximations. For accurate counts, use Anthropic's count_tokens API
	"claude-3-5-sonnet": "cl100k_base",
	"claude-3-opus":     "cl100k_base",
	"claude-3-sonnet":   "cl100k_base",
	"claude-3-haiku":    "cl100k_base",
	"claude-2.1":        "cl100k_base",
	"claude-2.0":        "cl100k_base",
	"claude-instant":    "cl100k_base",
}

// DefaultEncoding is the fallback encoding when model is not recognized
const DefaultEncoding = "cl100k_base"

// GetEncodingForModel returns the appropriate encoding for a given model
func GetEncodingForModel(model string) string {
	if encoding, ok := modelEncodings[model]; ok {
		return encoding
	}
	return DefaultEncoding
}

// IsClaudeModel checks if a model is a Claude/Anthropic model
func IsClaudeModel(model string) bool {
	return len(model) >= 6 && model[:6] == "claude"
}

// SupportedModels returns a list of all supported model names
func SupportedModels() []string {
	models := make([]string, 0, len(modelEncodings))
	for model := range modelEncodings {
		models = append(models, model)
	}
	return models
}

// SupportedEncodings returns a list of all supported encodings
func SupportedEncodings() []string {
	return []string{
		"o200k_base",   // GPT-4o, GPT-4.5
		"cl100k_base",  // GPT-4, GPT-3.5
		"p50k_base",    // Codex
		"r50k_base",    // GPT-3
	}
}