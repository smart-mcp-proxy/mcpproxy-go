package flow

import (
	"strings"
)

// Classifier classifies servers and tools as internal, external, hybrid, or unknown.
type Classifier struct {
	overrides map[string]string // server name → classification string
}

// NewClassifier creates a Classifier with optional server classification overrides.
func NewClassifier(overrides map[string]string) *Classifier {
	return &Classifier{overrides: overrides}
}

// Classify returns the classification for a server/tool combination.
// Priority: builtin tool → config override → server name heuristic → unknown.
func (c *Classifier) Classify(serverName, toolName string) ClassificationResult {
	// 1. Check for MCP tool namespacing: mcp__<server>__<tool>
	if strings.HasPrefix(toolName, "mcp__") {
		parts := strings.SplitN(toolName, "__", 3)
		if len(parts) == 3 {
			// Extract server name from namespace
			extractedServer := parts[1]
			extractedTool := parts[2]
			// Recurse with extracted server/tool (but skip MCP prefix check)
			return c.classifyResolved(extractedServer, extractedTool)
		}
	}

	return c.classifyResolved(serverName, toolName)
}

func (c *Classifier) classifyResolved(serverName, toolName string) ClassificationResult {
	// 1. Check builtin tool classifications (highest priority)
	if result, ok := builtinToolClassifications[toolName]; ok {
		return result
	}

	// 2. Check config overrides
	if c.overrides != nil {
		if classStr, ok := c.overrides[serverName]; ok {
			class := parseClassification(classStr)
			return ClassificationResult{
				Classification: class,
				Confidence:     1.0,
				Method:         "config",
				Reason:         "server classification configured as " + classStr,
				CanExfiltrate:  class == ClassExternal || class == ClassHybrid,
				CanReadData:    class == ClassInternal || class == ClassHybrid,
			}
		}
	}

	// 3. Server name heuristics
	if serverName != "" {
		if result, matched := classifyByName(serverName); matched {
			return result
		}
	}

	// 4. Unknown
	return ClassificationResult{
		Classification: ClassUnknown,
		Confidence:     0.0,
		Method:         "heuristic",
		Reason:         "no matching classification pattern",
		CanExfiltrate:  false,
		CanReadData:    false,
	}
}

// builtinToolClassifications maps agent-internal tool names to their classifications.
var builtinToolClassifications = map[string]ClassificationResult{
	"Read": {
		Classification: ClassInternal,
		Confidence:     0.9,
		Method:         "builtin",
		Reason:         "Read is a file system read tool (internal data source)",
		CanReadData:    true,
		CanExfiltrate:  false,
	},
	"Write": {
		Classification: ClassInternal,
		Confidence:     0.9,
		Method:         "builtin",
		Reason:         "Write is a file system write tool (internal data target)",
		CanReadData:    false,
		CanExfiltrate:  false,
	},
	"Edit": {
		Classification: ClassInternal,
		Confidence:     0.9,
		Method:         "builtin",
		Reason:         "Edit is a file system edit tool (internal data target)",
		CanReadData:    false,
		CanExfiltrate:  false,
	},
	"Glob": {
		Classification: ClassInternal,
		Confidence:     0.9,
		Method:         "builtin",
		Reason:         "Glob is a file system search tool (internal data source)",
		CanReadData:    true,
		CanExfiltrate:  false,
	},
	"Grep": {
		Classification: ClassInternal,
		Confidence:     0.9,
		Method:         "builtin",
		Reason:         "Grep is a file content search tool (internal data source)",
		CanReadData:    true,
		CanExfiltrate:  false,
	},
	"NotebookEdit": {
		Classification: ClassInternal,
		Confidence:     0.9,
		Method:         "builtin",
		Reason:         "NotebookEdit is a notebook modification tool (internal data target)",
		CanReadData:    false,
		CanExfiltrate:  false,
	},
	"WebFetch": {
		Classification: ClassExternal,
		Confidence:     0.9,
		Method:         "builtin",
		Reason:         "WebFetch sends HTTP requests to external URLs (external communication)",
		CanReadData:    false,
		CanExfiltrate:  true,
	},
	"WebSearch": {
		Classification: ClassExternal,
		Confidence:     0.9,
		Method:         "builtin",
		Reason:         "WebSearch queries external search engines (external communication)",
		CanReadData:    false,
		CanExfiltrate:  true,
	},
	"Bash": {
		Classification: ClassHybrid,
		Confidence:     0.8,
		Method:         "builtin",
		Reason:         "Bash can execute arbitrary commands (both data access and external communication)",
		CanReadData:    true,
		CanExfiltrate:  true,
	},
	"Task": {
		Classification: ClassInternal,
		Confidence:     0.9,
		Method:         "builtin",
		Reason:         "Task spawns sub-agents for internal operations",
		CanReadData:    true,
		CanExfiltrate:  false,
	},
}

// internalPatterns are substrings that indicate an internal (data source) server.
var internalPatterns = []string{
	"postgres", "mysql", "sqlite", "redis", "mongo", "database", "db",
	"filesystem", "file-system", "storage",
	"git", "github", "gitlab", "bitbucket",
	"vault", "secret",
	"ldap", "active-directory",
	"elastic", "opensearch", "solr",
	"kafka", "rabbitmq", "nats",
	"s3", "gcs", "blob",
	"jira", "confluence", "notion",
	"supabase", "firebase",
}

// externalPatterns are substrings that indicate an external (communication) server.
var externalPatterns = []string{
	"slack", "discord", "teams", "mattermost",
	"email", "smtp", "sendgrid", "mailgun", "ses",
	"webhook", "http-push", "http-post",
	"twilio", "sms", "telegram", "whatsapp", "signal",
	"twitter", "mastodon", "social",
	"zapier", "ifttt",
	"pagerduty", "opsgenie",
	"sns", "pubsub",
}

// hybridPatterns are substrings that indicate a hybrid (both read and communicate) server.
var hybridPatterns = []string{
	"aws", "azure", "gcloud", "gcp",
	"docker", "kubernetes", "k8s",
	"lambda", "function", "serverless",
	"api-gateway",
	"cloudflare",
}

func classifyByName(name string) (ClassificationResult, bool) {
	lower := strings.ToLower(name)

	for _, p := range internalPatterns {
		if strings.Contains(lower, p) {
			return ClassificationResult{
				Classification: ClassInternal,
				Confidence:     0.8,
				Method:         "heuristic",
				Reason:         "server name contains internal pattern: " + p,
				CanReadData:    true,
				CanExfiltrate:  false,
			}, true
		}
	}

	for _, p := range externalPatterns {
		if strings.Contains(lower, p) {
			return ClassificationResult{
				Classification: ClassExternal,
				Confidence:     0.8,
				Method:         "heuristic",
				Reason:         "server name contains external pattern: " + p,
				CanReadData:    false,
				CanExfiltrate:  true,
			}, true
		}
	}

	for _, p := range hybridPatterns {
		if strings.Contains(lower, p) {
			return ClassificationResult{
				Classification: ClassHybrid,
				Confidence:     0.8,
				Method:         "heuristic",
				Reason:         "server name contains hybrid pattern: " + p,
				CanReadData:    true,
				CanExfiltrate:  true,
			}, true
		}
	}

	return ClassificationResult{}, false
}

func parseClassification(s string) Classification {
	switch strings.ToLower(s) {
	case "internal":
		return ClassInternal
	case "external":
		return ClassExternal
	case "hybrid":
		return ClassHybrid
	default:
		return ClassUnknown
	}
}
