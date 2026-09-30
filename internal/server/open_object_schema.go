package server

import "github.com/mark3labs/mcp-go/mcp"

// openObject marks an object-typed tool parameter as accepting arbitrary keys.
//
// mcp-go's WithObject always seeds {"type":"object","properties":{}}. Grammar-
// constrained clients (llama.cpp llama-server, xgrammar) compile an empty
// "properties" map without "additionalProperties" as "no keys allowed", so
// models can only emit {} (issue #1364). This emits
// {"type":"object","additionalProperties":true} instead: llama.cpp and
// xgrammar compile it to an any-keys object. (Older Gemini function
// declarations reject additionalProperties; clients targeting Gemini must
// already sanitize it out of upstream schemas.)
func openObject() mcp.PropertyOption {
	return func(schema map[string]any) {
		delete(schema, "properties")
		schema["additionalProperties"] = true
	}
}
