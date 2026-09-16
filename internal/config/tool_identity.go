package config

import "strings"

// Tool identity (Spec 105 FR-009).
//
// A tool has exactly one identity: the (server, raw name) pair, where the raw
// name is the string the upstream server reported in tools/list — byte for
// byte, colons included. "ns:erase" on server "a" is a different tool from
// "erase" on server "a", and both are different from "a:erase" on server "a".
//
// Two spellings of that pair travel through the codebase:
//
//   - the RAW name ("ns:erase"): what is dispatched to the upstream, what the
//     approval record is keyed by, what the index docID is built from;
//   - the CANONICAL id ("a:ns:erase"): server + ":" + raw name, the id the MCP
//     surface hands to agents (#871) and the index reads back as
//     ToolMetadata.Name.
//
// Before FR-009 both producers (checkToolApprovals, the Bleve docID) and the
// differential index update derived the "bare" name by stripping everything up
// to the FIRST colon of whatever was in ToolMetadata.Name. Discovery stores the
// raw name there, so a raw "ns:erase" collapsed to "erase" — colliding with a
// real "erase" in the approval store and the index, and silently inheriting
// its approval. The helpers below make the raw name an explicit field instead
// of something guessed from the shape of the canonical string.

// RawToolName returns the exact upstream-reported name of a tool.
//
// It prefers the RawName stamp written at discovery
// (internal/upstream/core/client.go ListTools). Metadata built without the
// stamp — index reads, test fixtures, code paths that only carry the canonical
// id — falls back to trimming the tool's OWN server prefix once from Name,
// which is exact for both a raw name ("ns:erase" → "ns:erase") and a canonical
// id ("a:ns:erase" on server "a" → "ns:erase"). The one shape the fallback
// cannot disambiguate is a raw name that itself begins with the server's own
// prefix ("a:erase" on server "a"), which is why producers MUST stamp RawName
// rather than rely on the fallback.
func RawToolName(tool *ToolMetadata) string {
	if tool == nil {
		return ""
	}
	if tool.RawName != "" {
		return tool.RawName
	}
	if tool.ServerName == "" {
		return tool.Name
	}
	return strings.TrimPrefix(tool.Name, tool.ServerName+":")
}

// CanonicalToolName returns the canonical "<server>:<rawName>" id for an exact
// (server, raw name) pair. Unlike index.CanonicalToolName it never guards
// against a double prefix: the raw name is exact by contract, so a raw
// "a:erase" on server "a" is correctly rendered as "a:a:erase" — the spelling
// handleCallToolVariant splits back into the same pair.
func CanonicalToolName(serverName, rawName string) string {
	if serverName == "" {
		return rawName
	}
	return serverName + ":" + rawName
}
