package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/outputfile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/server/tokens"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
)

// CacheStore is the narrow interface forwardContentResult needs from a cache
// manager to persist truncated payloads so the read_cache MCP tool can later
// page through them. It mirrors cache.Manager.Store but is defined here to
// keep this package free of an internal/cache import.
type CacheStore interface {
	Store(key, toolName string, args map[string]interface{}, content, recordPath string, totalRecords int) error
}

// logCacheStoreFailure emits a structured warning when a CacheStore.Store call
// fails. The truncation banner shown to the agent will reference a cache key
// that no longer resolves, so the symptom (read_cache returning "cache key not
// found") needs to be debuggable from the server logs. A nil logger is a no-op
// so test callers can pass nil without ceremony.
func logCacheStoreFailure(logger *zap.Logger, err error, toolName, cacheKey string, totalRecords, payloadBytes int) {
	if logger == nil {
		return
	}
	logger.Warn("Failed to persist truncated payload — read_cache will return 'cache key not found' for this key",
		zap.String("tool", toolName),
		zap.String("cache_key", cacheKey),
		zap.Int("total_records", totalRecords),
		zap.Int("payload_bytes", payloadBytes),
		zap.Error(err),
	)
}

// proxyResultEnvelope builds the mcp.Result envelope mcpproxy puts on a
// forwarded tool result (Spec 058 FR-016b).
//
// Only _meta is relayed. Copying the upstream's envelope wholesale would also
// replay its ResultType — a 2026-07-28 field telling the CLIENT how to
// interpret the response — while mcp.CallToolResult embeds MultiRoundTripResult
// separately, so the inputRequests and requestState that make an
// "input_required" answerable were dropped on the same hop. The client was left
// told to supply input it had no way to supply.
//
// Leaving ResultType unset lets mcp-go stamp the value appropriate to the era
// mcpproxy is answering on, which is the only party that knows it.
//
// Both forwarding paths (forwardContentResult here, and the direct-surface
// handler in mcp_routing.go) call this, so the two cannot drift apart.
func proxyResultEnvelope(ctr *mcp.CallToolResult) mcp.Result {
	if ctr == nil {
		return mcp.Result{}
	}
	return mcp.Result{Meta: relaySafeResultMeta(ctr.Meta)}
}

// hopScopedResultMetaKeys are reserved _meta keys that describe the HOP a result
// travelled over rather than the payload it carries, so they must not survive a
// proxy hop.
//
// serverInfo is the one that bites. mcp-go stamps mcpproxy's own identity into
// an outgoing modern result only when the field is still absent
// (server/response.go decorateResult: `if meta.ServerInfo() == nil`). Relaying
// an upstream's serverInfo therefore does not merely add noise — it suppresses
// mcpproxy's identity and makes its response claim to BE the upstream server.
// protocolVersion is hop-scoped for the same reason: the era mcpproxy negotiated
// upstream is not the era it is answering on.
var hopScopedResultMetaKeys = []string{
	mcp.MetaKeyServerInfo,
	mcp.MetaKeyProtocolVersion,
}

// relaySafeResultMeta copies meta minus the hop-scoped keys, leaving trace
// context and any upstream-specific fields intact. It returns nil when nothing
// survives, so no empty _meta object is emitted.
func relaySafeResultMeta(meta *mcp.Meta) *mcp.Meta {
	if meta == nil {
		return nil
	}

	var fields map[string]any
	for key, value := range meta.AdditionalFields {
		if slices.Contains(hopScopedResultMetaKeys, key) {
			continue
		}
		if fields == nil {
			fields = make(map[string]any, len(meta.AdditionalFields))
		}
		fields[key] = value
	}

	if fields == nil && meta.ProgressToken == nil {
		return nil
	}
	return &mcp.Meta{ProgressToken: meta.ProgressToken, AdditionalFields: fields}
}

// forwardContentResult preserves non-text content blocks (ImageContent, AudioContent,
// EmbeddedResource) from an upstream CallToolResult while applying truncation only to
// TextContent blocks. This fixes issue #368 where all content types were being
// serialized to JSON and wrapped in a single TextContent, destroying the ability of
// vision-capable LLMs to process images efficiently.
//
// When truncation kicks in, the FULL pre-truncation content is persisted via
// cacheStore (if non-nil) under the same key embedded in the truncated text's
// "use read_cache" instruction. Without this step read_cache always returns
// "cache key not found", which was the long-standing pagination bug.
//
// Parameters:
//   - result: the upstream CallToolResult (passed as interface{} from Manager.CallTool)
//   - truncator: applies size limits to text blocks only
//   - cacheStore: optional; when non-nil, full pre-truncation payloads are stored here
//     under the cache key generated by the truncator so read_cache can paginate them
//   - logger: optional; receives a zap.Warn entry if a cacheStore.Store call fails.
//     A failed Store leaves the agent with a banner pointing at a key that won't
//     resolve, so this log is the only way the operator can diagnose the resulting
//     "cache key not found" symptom. Pass nil to silence.
//   - toolName, args: passed to truncator for cache-key derivation and to the cacheStore
//
// Returns:
//   - forwarded: the CallToolResult to return downstream (with original content types)
//   - textRepresentation: a text rendering of the full result for logging/metrics
//   - wasTruncated: whether any TextContent block was truncated
//
// If result is not a *mcp.CallToolResult, it falls back to JSON-serializing the whole
// thing into a TextContent block (legacy behavior).
func forwardContentResult(result interface{}, truncator *truncate.Truncator, cacheStore CacheStore, logger *zap.Logger, toolName string, args map[string]interface{}) (forwarded *mcp.CallToolResult, textRepresentation string, wasTruncated bool) {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil {
		// Fallback: not a CallToolResult (should not happen with current upstream chain,
		// but guard against future changes). Use legacy JSON-wrap behavior.
		jsonBytes, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize result: %v", err)), "", false
		}
		text := string(jsonBytes)
		if truncator != nil && truncator.ShouldTruncate(text) {
			tr := truncator.Truncate(text, toolName, args)
			if tr.CacheAvailable && cacheStore != nil {
				if storeErr := cacheStore.Store(tr.CacheKey, toolName, args, text, tr.RecordPath, tr.TotalRecords); storeErr != nil {
					logCacheStoreFailure(logger, storeErr, toolName, tr.CacheKey, tr.TotalRecords, len(text))
				}
			}
			text = tr.TruncatedContent
			wasTruncated = true
		}
		return mcp.NewToolResultText(text), text, wasTruncated
	}

	// Walk the Content slice, truncating only TextContent blocks.
	// Build a parallel text representation for logging/metrics.
	newContent := make([]mcp.Content, 0, len(ctr.Content))
	var textBuilder []string
	for _, c := range ctr.Content {
		switch tc := c.(type) {
		case mcp.TextContent:
			txt := tc.Text
			if truncator != nil && truncator.ShouldTruncate(txt) {
				tr := truncator.Truncate(txt, toolName, args)
				if tr.CacheAvailable && cacheStore != nil {
					if storeErr := cacheStore.Store(tr.CacheKey, toolName, args, txt, tr.RecordPath, tr.TotalRecords); storeErr != nil {
						logCacheStoreFailure(logger, storeErr, toolName, tr.CacheKey, tr.TotalRecords, len(txt))
					}
				}
				txt = tr.TruncatedContent
				wasTruncated = true
			}
			tc.Text = txt
			newContent = append(newContent, tc)
			textBuilder = append(textBuilder, txt)
		case mcp.ImageContent:
			// Preserve image blocks unchanged. For logging, emit a placeholder.
			newContent = append(newContent, tc)
			textBuilder = append(textBuilder, fmt.Sprintf("[image:%s len=%d]", tc.MIMEType, len(tc.Data)))
		case mcp.AudioContent:
			// Preserve audio blocks unchanged. For logging, emit a placeholder.
			newContent = append(newContent, tc)
			textBuilder = append(textBuilder, fmt.Sprintf("[audio:%s len=%d]", tc.MIMEType, len(tc.Data)))
		default:
			// Unknown content type (e.g., EmbeddedResource). Forward as-is and
			// best-effort JSON encode for logging.
			newContent = append(newContent, c)
			if b, err := json.Marshal(c); err == nil {
				textBuilder = append(textBuilder, string(b))
			}
		}
	}

	forwarded = &mcp.CallToolResult{
		Result:            proxyResultEnvelope(ctr),
		Content:           newContent,
		StructuredContent: ctr.StructuredContent,
		IsError:           ctr.IsError,
	}
	textRepresentation = joinTextParts(textBuilder)
	return forwarded, textRepresentation, wasTruncated
}

// maybeTruncateAndCacheText is a small helper for built-in MCP tools whose
// output bypasses forwardContentResult (e.g. handleReadCache). It applies the
// same truncate-and-cache contract: if the text exceeds the truncator's limit,
// the full text is stored in cacheStore under the truncator-generated key and
// the returned snippet carries the standard "use read_cache" banner that
// points at it.
//
// The paginableUnits parameter is the recursion guard for built-in tools that
// may themselves be paginating (notably read_cache). If paginableUnits <= 1
// the caller has nothing further to subdivide — caching a fresh key here would
// just hand the agent a new key resolving to the same oversize payload, an
// inescapable loop. That case still respects the limit, just without a
// pagination contract: the text is cut plainly and carries the
// "cache not available" notice instead of a read_cache banner.
//
// Termination is therefore agent-driven, not strictly algorithmic: a level
// whose records all fit individually but overflow collectively keeps minting
// a fresh key per call until the agent pages down to the single-record
// short-circuit. Each call's key is unique (timestamped at nanosecond
// granularity), so successive identical calls are NOT idempotent — they
// produce distinct cache entries. Unbounded growth is bounded only by the
// cache's TTL + background cleanup (see cache.DefaultTTL), which is acceptable
// because truncation is the exceptional path.
//
// Returns:
//   - the text to actually emit (possibly truncated)
//   - wasTruncated: true if the truncator's recorded-pagination path ran
//
// logger is optional; receives a zap.Warn if cacheStore.Store fails so the
// resulting "cache key not found" can be debugged. Pass nil to silence.
func maybeTruncateAndCacheText(text, toolName string, args map[string]interface{}, paginableUnits int, truncator *truncate.Truncator, cacheStore CacheStore, logger *zap.Logger) (out string, wasTruncated bool) {
	return maybeTruncateAndCacheTextPinned(text, toolName, args, paginableUnits, "", truncator, cacheStore, logger)
}

// maybeTruncateAndCacheTextPinned is maybeTruncateAndCacheText for callers that
// compose their own payload and therefore know which array the banner they emit
// promises to page. pinnedRecordPath forces that array to be the pagination
// record path instead of letting the truncator's heuristic pick whichever array
// happens to hold the most elements — for retrieve_tools that heuristic can land
// on the Spec 049 "disabled" list or on an array nested inside a tool's input
// schema, so read_cache would page non-tool records under a banner that
// advertises tools.
//
// The pin is also what makes paginableUnits meaningful: the guard counts units
// of the SAME array the key pages. If the pinned path is not an array in the
// payload the truncator emits no cache handle at all (plain truncation), so the
// banner can never advertise a contract that does not hold. An empty
// pinnedRecordPath keeps the inferred behaviour.
func maybeTruncateAndCacheTextPinned(text, toolName string, args map[string]interface{}, paginableUnits int, pinnedRecordPath string, truncator *truncate.Truncator, cacheStore CacheStore, logger *zap.Logger) (out string, wasTruncated bool) {
	if truncator == nil || !truncator.ShouldTruncate(text) {
		return text, false
	}
	if paginableUnits <= 1 {
		// Cannot subdivide further; recursive caching here would just hand the
		// agent a new key that resolves to the exact same payload. The response
		// limit still holds, so cut plainly: the standard truncation notice, no
		// read_cache banner and no cache write. Returning the oversize text
		// intact would have let a single fat record (one tool with a sprawling
		// schema) pass tool_response_limit unbounded.
		return truncator.SimpleTruncate(text), true
	}
	tr := truncator.TruncateWithRecordPath(text, toolName, args, pinnedRecordPath)
	if tr.CacheAvailable && cacheStore != nil {
		if storeErr := cacheStore.Store(tr.CacheKey, toolName, args, text, tr.RecordPath, tr.TotalRecords); storeErr != nil {
			logCacheStoreFailure(logger, storeErr, toolName, tr.CacheKey, tr.TotalRecords, len(text))
		}
	}
	return tr.TruncatedContent, true
}

// forwardedText recomputes the text representation of a (possibly sanitised)
// forwarded result so logging/metrics reflect what the agent actually receives
// after Spec 054 Track B mutation. It mirrors forwardContentResult's text
// rendering (text verbatim, placeholders for non-text blocks). If forwarded is
// nil or carries no renderable content, the supplied fallback is returned.
func forwardedText(forwarded *mcp.CallToolResult, fallback string) string {
	if forwarded == nil {
		return fallback
	}
	var parts []string
	for _, c := range forwarded.Content {
		switch tc := c.(type) {
		case mcp.TextContent:
			parts = append(parts, tc.Text)
		case mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[image:%s len=%d]", tc.MIMEType, len(tc.Data)))
		case mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio:%s len=%d]", tc.MIMEType, len(tc.Data)))
		default:
			if b, err := json.Marshal(c); err == nil {
				parts = append(parts, string(b))
			}
		}
	}
	if len(parts) == 0 {
		return fallback
	}
	return joinTextParts(parts)
}

// joinTextParts concatenates text parts with a newline separator.
// Equivalent to strings.Join but avoids importing strings just for this.
func joinTextParts(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	total := len(parts) - 1 // separators
	for _, p := range parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for i, p := range parts {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, p...)
	}
	return string(out)
}

// --- save_to_file (Spec 076) ---
//
// Separates "get the data complete" from "read it into context": when the
// agent sets save_to_file on a call_tool_read/write/destructive call, the
// FULL upstream response is written to a config-whitelisted file instead of
// being (possibly truncated) forwarded inline.

// saveToFileParams carries the (optional) save_to_file request parameters
// parsed from a call_tool_* invocation. An empty Path means save_to_file was
// not requested for this call.
type saveToFileParams struct {
	Path      string
	Format    string // "text" (default) or "json"
	Overwrite bool
}

// saveToFileConfig carries the server-side whitelist configuration needed to
// resolve and write save_to_file targets (internal/config.Config's
// ToolOutputRoots / ToolOutputMaxBytes, threaded through rather than
// importing internal/config here to keep this file's dependency surface
// small and testable).
type saveToFileConfig struct {
	Roots    []string
	MaxBytes int64
}

// saveToFileEnvelope is the JSON body returned to the agent in place of the
// (possibly huge) upstream response when save_to_file succeeds. Field names
// are part of the save_to_file contract (Spec 076) — do not rename without
// updating the tool schema description in mcp.go.
type saveToFileEnvelope struct {
	SavedTo          string `json:"saved_to"`
	Bytes            int64  `json:"bytes"`
	SHA256           string `json:"sha256"`
	Format           string `json:"format"`
	ContentBlocks    int    `json:"content_blocks"`
	NonTextBlocks    int    `json:"non_text_blocks"`
	Preview          string `json:"preview"`
	TruncatedPreview bool   `json:"truncated_preview"`
}

// previewRuneLimit bounds the "preview" envelope field.
const previewRuneLimit = 1000

// maybeSaveToFile implements the save_to_file capability. It MUST be called
// before any ShouldTruncate/Truncate so the file it writes is always the
// complete, untruncated upstream response — never the possibly-truncated one
// forwardContentResult would otherwise produce.
//
// Returns handled=false when save_to_file was not requested (params.Path ==
// "") or when the upstream result is itself an error result — in both cases
// the caller must fall through to the normal forwardContentResult truncation
// path completely unchanged (agents must always see upstream errors inline;
// save_to_file never intercepts them). Returns handled=true in every other
// case (success or a save_to_file-specific failure), together with the
// *mcp.CallToolResult the caller should return to the agent AS-IS, skipping
// forwardContentResult entirely.
//
// On a resolve/write failure this returns a tool error result whose text is
// "save_to_file: <reason>" — errors never fall back to returning the
// (possibly huge) upstream body inline; the caller explicitly asked for a
// file, so a silent fallback to the old behavior would hide the failure.
// Never logs or echoes file CONTENT — only the resolved path, byte count,
// sha256 and a ≤1000-rune preview ever leave this function.
func maybeSaveToFile(result interface{}, params saveToFileParams, cfg saveToFileConfig) (forwarded *mcp.CallToolResult, handled bool) {
	if params.Path == "" {
		return nil, false
	}

	format := params.Format
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return saveToFileErrorResult(fmt.Errorf("invalid save_format %q (must be \"text\" or \"json\")", params.Format)), true
	}

	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil {
		// A legacy, non-*mcp.CallToolResult upstream result still honors an
		// explicit save_to_file request rather than silently ignoring it
		// (silently falling through to forwardContentResult would forward/
		// truncate the response inline as if save_to_file had never been
		// set). "text" has nothing well-defined to concatenate for an
		// arbitrary interface{}, so only "json" (the whole result,
		// JSON-marshaled) is supported on this path.
		if format == "text" {
			return saveToFileErrorResult(errors.New("unsupported upstream result type for text format; use save_format=\"json\"")), true
		}
		data, err := json.Marshal(result)
		if err != nil {
			return saveToFileErrorResult(fmt.Errorf("failed to encode response as json: %w", err)), true
		}
		return writeSaveToFile(params, cfg, format, data, 0, 0, "")
	}
	if ctr.IsError {
		// Agents must always see errors; never divert an error result to a
		// file. Let the existing (unchanged) error-forwarding path run.
		return nil, false
	}

	totalBlocks, nonTextBlocks := countContentBlocks(ctr)
	// The preview — and the format:"text" payload — is always the
	// concatenated TextContent blocks only (no placeholders for
	// image/audio/unknown blocks). This is what makes non_text_blocks > 0
	// with format:"text" meaningful: those blocks are genuinely absent from
	// the saved file, not just replaced with a placeholder string.
	textForm := concatSaveableTextBlocks(ctr)

	// format:"text" with content blocks present but none of them
	// non-empty text (e.g. an image-only response, or a lone empty-string
	// text block) must fail loudly instead of silently writing a 0-byte
	// file with a success envelope. A genuinely empty response (zero
	// content blocks at all) is unaffected — that is a real empty upstream
	// result, not text dropped by this filter.
	if format == "text" && textForm == "" && totalBlocks > 0 {
		return saveToFileErrorResult(errors.New("response has no non-empty text content; use save_format=\"json\"")), true
	}

	var data []byte
	if format == "json" {
		b, err := json.Marshal(ctr)
		if err != nil {
			return saveToFileErrorResult(fmt.Errorf("failed to encode response as json: %w", err)), true
		}
		data = b
	} else {
		data = []byte(textForm)
	}

	return writeSaveToFile(params, cfg, format, data, totalBlocks, nonTextBlocks, textForm)
}

// writeSaveToFile resolves params.Path against cfg's whitelist, writes data,
// and builds the success envelope. Shared by both the normal
// *mcp.CallToolResult path and the legacy non-*mcp.CallToolResult path
// above; the latter always passes totalBlocks=0, nonTextBlocks=0,
// textForm="" since those concepts don't apply to an arbitrary
// JSON-marshaled interface{}, so the envelope's preview is empty and
// content_blocks/non_text_blocks are 0.
func writeSaveToFile(params saveToFileParams, cfg saveToFileConfig, format string, data []byte, totalBlocks, nonTextBlocks int, textForm string) (*mcp.CallToolResult, bool) {
	target, err := outputfile.Resolve(cfg.Roots, params.Path, params.Overwrite)
	if err != nil {
		return saveToFileErrorResult(err), true
	}
	// Resolve opens (and identity-checks) target.Handle — an *os.Root — as
	// part of validating the path (see outputfile.Resolve); this call owns
	// that handle's lifecycle from here on and must close it on every
	// return path, success or failure.
	defer func() { _ = target.Close() }()

	info, err := outputfile.Write(target, data, cfg.MaxBytes, params.Overwrite)
	if err != nil {
		return saveToFileErrorResult(err), true
	}

	preview, truncatedPreview := previewRunes(textForm, previewRuneLimit)
	envJSON, err := json.Marshal(saveToFileEnvelope{
		SavedTo:          info.Path,
		Bytes:            info.Bytes,
		SHA256:           info.SHA256,
		Format:           format,
		ContentBlocks:    totalBlocks,
		NonTextBlocks:    nonTextBlocks,
		Preview:          preview,
		TruncatedPreview: truncatedPreview,
	})
	if err != nil {
		return saveToFileErrorResult(fmt.Errorf("failed to encode result envelope: %w", err)), true
	}

	return mcp.NewToolResultText(string(envJSON)), true
}

// currentSaveToFileConfig returns the save_to_file settings from the LIVE
// config snapshot when a runtime is attached, falling back to p.config.
// p.config is captured once in NewMCPProxyServer and never reassigned, so a
// hot-reloaded tool_output_roots/tool_output_max_bytes change would otherwise
// not take effect until a full restart.
func (p *MCPProxyServer) currentSaveToFileConfig() saveToFileConfig {
	cfg := saveToFileConfig{}
	if p.config != nil {
		cfg = saveToFileConfig{Roots: p.config.ToolOutputRoots, MaxBytes: p.config.ToolOutputMaxBytes}
	}
	if p.mainServer != nil && p.mainServer.runtime != nil {
		if live := p.mainServer.runtime.Config(); live != nil {
			cfg = saveToFileConfig{Roots: live.ToolOutputRoots, MaxBytes: live.ToolOutputMaxBytes}
		}
	}
	return cfg
}

// preflightSaveToFile runs the request-side save_to_file checks BEFORE the
// upstream call is dispatched: feature disabled (no roots), invalid path,
// path outside every root, an existing file without save_overwrite, or an
// unavailable root. Without it, a (possibly destructive) upstream tool would
// execute only for its result to be discarded by a save that could never
// succeed — and an agent retrying the "failed" call would repeat the side
// effect. It returns nil when save_to_file was not requested or the target
// currently passes; the post-dispatch Resolve+Write in maybeSaveToFile still
// runs (the filesystem can change in between, and that path is the one that
// actually enforces the whitelist for the write).
func preflightSaveToFile(params saveToFileParams, cfg saveToFileConfig) *mcp.CallToolResult {
	if params.Path == "" {
		return nil
	}
	target, err := outputfile.Resolve(cfg.Roots, params.Path, params.Overwrite)
	if err != nil {
		return saveToFileErrorResult(err)
	}
	_ = target.Close()
	return nil
}

// saveToFileErrorResult wraps err as the standard "save_to_file: <reason>"
// tool error text mandated by the save_to_file contract (Spec 076).
func saveToFileErrorResult(err error) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf("save_to_file: %v", err))
}

// recountSaveOrTruncateTokenMetrics corrects tokenMetrics.OutputTokens/
// TotalTokens to reflect what the agent actually received, for the two
// cases where the raw upstream-body token count computed earlier is wrong:
// a truncated response (wasTruncated), a TOON re-encoded response (reencoded,
// Spec 084 FR-010) or a save_to_file-diverted response (savedToFile) — in
// each case the agent saw a different forwarded/response text than the raw
// upstream body. tokenMetrics.SavedToFile is always set
// unconditionally (independent of whether a recount is possible below) so a
// caller can tell "this call went through the save path" apart from "this
// call had a coincidentally small body", even when tokenizer is nil or
// errors.
//
// When neither flag is set, this is a no-op beyond setting SavedToFile.
// When a flag is set and tokenizer successfully recounts response, that
// recount wins. When tokenizer is nil, or CountTokensForModel errors, a
// save_to_file response STILL gets corrected to OutputTokens=0/
// TotalTokens=InputTokens (the agent never saw the un-recountable full
// body either way) — but a truncated-without-save response keeps its
// original (uncorrected, full-body) count, matching the pre-existing
// truncation/TOON behaviour.
//
// Returns true if tokenMetrics was mutated in a way the caller should
// propagate onto its own copy (toolCallRecord.Metrics = tokenMetrics).
func recountSaveOrTruncateTokenMetrics(tokenMetrics *storage.TokenMetrics, wasTruncated, reencoded, savedToFile bool, response string, tokenizer tokens.Tokenizer) bool {
	if tokenMetrics == nil {
		return false
	}
	tokenMetrics.SavedToFile = savedToFile
	if !wasTruncated && !reencoded && !savedToFile {
		return false
	}
	if tokenizer != nil {
		if recountedTokens, err := tokenizer.CountTokensForModel(response, tokenMetrics.Model); err == nil {
			tokenMetrics.WasTruncated = wasTruncated
			tokenMetrics.OutputTokens = recountedTokens
			tokenMetrics.TotalTokens = tokenMetrics.InputTokens + tokenMetrics.OutputTokens
			return true
		}
	}
	if savedToFile {
		tokenMetrics.WasTruncated = wasTruncated
		tokenMetrics.OutputTokens = 0
		tokenMetrics.TotalTokens = tokenMetrics.InputTokens
		return true
	}
	return false
}

// validateSaveToFileArgTypes checks the RAW, un-coerced argument map for the
// save_to_file/save_format/save_overwrite keys before they are parsed with
// mcp-go's lenient request.GetString/GetBool (which silently fall back to
// the zero value on a type mismatch instead of erroring). Without this, a
// caller that accidentally sends save_to_file as a JSON number, or
// save_overwrite as a string, would have the request silently treated as
// "save_to_file not requested" / "overwrite not requested" — the request
// then proceeds and returns the (possibly truncated) inline response, with
// no indication the parameter was ignored. Also rejects an invalid
// save_format enum value here (pre-dispatch), not just a type mismatch — the
// same check maybeSaveToFile makes post-dispatch runs again there as
// defence-in-depth, but catching it here avoids running the (possibly
// destructive) upstream call only to discard its result for a save_format
// typo. Also rejects two shapes that would otherwise be silently
// misinterpreted: a present-but-empty save_to_file string (which downstream
// code treats identically to save_to_file being absent), and save_format or
// save_overwrite supplied without a (non-empty) save_to_file alongside them.
// Returns a non-nil tool-error result if any present argument has the wrong
// type, save_to_file is present but empty, save_format/save_overwrite are
// present without save_to_file, or save_format is present but not "",
// "text", or "json"; nil if every present argument (if any) is well-typed
// and valid.
func validateSaveToFileArgTypes(request mcp.CallToolRequest) *mcp.CallToolResult {
	args := request.GetArguments()
	saveToFileRequested := false
	if v, ok := args["save_to_file"]; ok {
		s, isStr := v.(string)
		if !isStr {
			return saveToFileErrorResult(fmt.Errorf("parameter save_to_file must be a string"))
		}
		// A present-but-empty save_to_file is exactly the silent-ignore
		// failure mode this function exists to prevent: downstream code
		// treats "" the same as "key absent" (params.Path == ""), so a
		// caller who meant to request a save would instead get the normal
		// (possibly truncated) inline response with no indication their
		// save_to_file was ignored.
		if s == "" {
			return saveToFileErrorResult(errors.New("must be a non-empty absolute path"))
		}
		saveToFileRequested = true
	}
	if v, ok := args["save_format"]; ok {
		s, isStr := v.(string)
		if !isStr {
			return saveToFileErrorResult(fmt.Errorf("parameter save_format must be a string"))
		}
		if !saveToFileRequested {
			return saveToFileErrorResult(errors.New("save_format/save_overwrite require save_to_file"))
		}
		if s != "" && s != "text" && s != "json" {
			return saveToFileErrorResult(fmt.Errorf("invalid save_format %q (must be \"text\" or \"json\")", s))
		}
	}
	if v, ok := args["save_overwrite"]; ok {
		if _, isBool := v.(bool); !isBool {
			return saveToFileErrorResult(fmt.Errorf("parameter save_overwrite must be a bool"))
		}
		if !saveToFileRequested {
			return saveToFileErrorResult(errors.New("save_format/save_overwrite require save_to_file"))
		}
	}
	return nil
}

// saveToFileTextContent returns a content block's text and true if the
// block is a text block, accepting both the value form (mcp.TextContent, as
// produced by mcp.NewTextContent) and the pointer form (*mcp.TextContent) —
// a bare `c.(mcp.TextContent)` type assertion would mis-classify the
// pointer form as a non-text block and silently drop it from both the
// block count and the saved text, even though it carries real text.
func saveToFileTextContent(c mcp.Content) (string, bool) {
	switch tc := c.(type) {
	case mcp.TextContent:
		return tc.Text, true
	case *mcp.TextContent:
		if tc == nil {
			return "", false
		}
		return tc.Text, true
	default:
		return "", false
	}
}

// countContentBlocks reports the total number of content blocks in ctr and
// how many of those are NOT text (image, audio, embedded resource, or any
// other unknown type) — see saveToFileTextContent for what counts as text.
func countContentBlocks(ctr *mcp.CallToolResult) (total, nonText int) {
	total = len(ctr.Content)
	for _, c := range ctr.Content {
		if _, ok := saveToFileTextContent(c); !ok {
			nonText++
		}
	}
	return total, nonText
}

// concatSaveableTextBlocks concatenates every text block in ctr.Content
// (see saveToFileTextContent), newline-separated. This is the save_to_file
// path's own text extraction — kept separate from the shared
// concatTextBlocks in output_sanitisation.go (which only recognizes the
// value form) so this fix does not change behavior for other callers of
// that function.
func concatSaveableTextBlocks(ctr *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range ctr.Content {
		if txt, ok := saveToFileTextContent(c); ok {
			// Intentional: gating on b.Len() > 0 drops a leading empty text
			// block's separator (["","A","","B"] -> "A\n\nB"); interior/trailing empty blocks still keep theirs.
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(txt)
		}
	}
	return b.String()
}

// previewRunes returns the first maxRunes runes of s and whether s had more
// than that.
func previewRunes(s string, maxRunes int) (preview string, truncated bool) {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s, false
	}
	return string(runes[:maxRunes]), true
}
