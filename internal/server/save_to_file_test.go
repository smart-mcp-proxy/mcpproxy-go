package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
)

// --- (a) regression pin: no save_to_file -> unchanged truncation path ---
//
// maybeSaveToFile must be a pure no-op (handled=false) when save_to_file was
// not requested, so the existing forwardContentResult truncation behavior —
// already pinned by TestForwardContentResult_TruncatesOnlyText et al. above —
// is completely untouched by this feature.
func TestMaybeSaveToFile_NotRequestedFallsThrough(t *testing.T) {
	bigText := strings.Repeat("x", 2000)
	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(bigText)},
	}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{}, saveToFileConfig{})
	assert.False(t, handled)
	assert.Nil(t, forwarded)

	// The unmodified path still truncates exactly as before.
	truncator := truncate.NewTruncator(500)
	result, _, truncated := forwardContentResult(upstream, truncator, nil, nil, "test:tool", nil)
	require.NotNil(t, result)
	assert.True(t, truncated)
}

// (b) save_to_file under a whitelisted root: file bytes == full upstream
// text, envelope JSON fields correct, preview <= 1000 runes.
func TestMaybeSaveToFile_TextFormat_WritesFullContent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")

	bigText := strings.Repeat("y", 50000) // well over any tool_response_limit
	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(bigText)},
	}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target}, saveToFileConfig{
		Roots:    []string{root},
		MaxBytes: 0,
	})
	require.True(t, handled)
	require.NotNil(t, forwarded)
	require.False(t, forwarded.IsError)

	// File on disk must be byte-identical to the full upstream text.
	onDisk, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, bigText, string(onDisk))

	env := decodeEnvelope(t, forwarded)
	assert.Equal(t, int64(len(bigText)), env.Bytes)
	assert.Equal(t, "text", env.Format)
	assert.Equal(t, 1, env.ContentBlocks)
	assert.Equal(t, 0, env.NonTextBlocks)
	assert.LessOrEqual(t, len([]rune(env.Preview)), 1000)
	assert.True(t, env.TruncatedPreview, "50000-rune text must produce a truncated preview")
	assert.NotEmpty(t, env.SHA256)
	// Never echo file content in the envelope beyond the bounded preview.
	assert.Less(t, len(env.Preview), len(bigText))
}

// (c) save_format: json -> file parses back to a CallToolResult with all
// blocks (including a non-text block) — for TextContent and ImageContent,
// which round-trip to their original concrete types via mcp-go's
// CallToolResult.UnmarshalJSON/UnmarshalContent dispatch.
//
// Renamed from the original TestMaybeSaveToFile_JSONFormat_RoundTrips: "round
// trips" is only true for content types mcp-go's UnmarshalContent switch maps
// to a concrete struct by their "type" field (text/image/audio/resource_link)
// — it is NOT true for EmbeddedResource, see
// TestMaybeSaveToFile_JSONFormat_EmbeddedResourceDoesNotUnmarshalBackIntoMcpGoTypes
// below.
func TestMaybeSaveToFile_JSONFormat_RoundTripsTextAndImage(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.json")

	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent("hello"),
			mcp.NewImageContent("aGVsbG8=", "image/png"),
		},
	}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target, Format: "json"}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	require.NotNil(t, forwarded)
	require.False(t, forwarded.IsError)

	raw, err := os.ReadFile(target)
	require.NoError(t, err)

	var restored mcp.CallToolResult
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.Len(t, restored.Content, 2)
	assert.IsType(t, mcp.TextContent{}, restored.Content[0])
	assert.IsType(t, mcp.ImageContent{}, restored.Content[1])

	env := decodeEnvelope(t, forwarded)
	assert.Equal(t, "json", env.Format)
	assert.Equal(t, 2, env.ContentBlocks)
	assert.Equal(t, 1, env.NonTextBlocks)
	// preview is always the text-only form, regardless of save_format.
	assert.Equal(t, "hello", env.Preview)
	assert.False(t, env.TruncatedPreview)
}

// (c, EmbeddedResource) save_format:json with an EmbeddedResource block: the FILE on disk
// is a faithful json.Marshal of the original CallToolResult (bytes-correct —
// this is what an agent, or any JSON-aware tool, re-reading the file sees).
// But unmarshaling it back into a fresh mcp.CallToolResult with encoding/json
// (the way the sibling TestMaybeSaveToFile_JSONFormat_RoundTripsTextAndImage
// test does for text/image) does NOT work for EmbeddedResource in mcp-go
// v0.55.0: EmbeddedResource.Resource is the ResourceContents INTERFACE, and
// neither EmbeddedResource nor TextResourceContents/BlobResourceContents
// implement a custom UnmarshalJSON — so when UnmarshalContent's
// "type":"resource" case does a plain `json.Unmarshal(data, &EmbeddedResource{})`,
// encoding/json has no concrete type to target for the interface-typed
// "resource" field and returns
// "json: cannot unmarshal object into Go struct field EmbeddedResource.resource
// of type mcp.ResourceContents" — which fails the ENTIRE CallToolResult
// unmarshal (Content ends up nil), not just that one block. This is the real
// "non-round-tripping" behavior the brief warned about: save_to_file's json
// format still writes complete, correct bytes (verified below), but Go code
// that reads an EmbeddedResource-containing file back into mcp-go's own
// types via encoding/json cannot do so directly — it must decode into
// map[string]interface{} (or a caller-defined shape) instead.
func TestMaybeSaveToFile_JSONFormat_EmbeddedResourceDoesNotUnmarshalBackIntoMcpGoTypes(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.json")

	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewEmbeddedResource(mcp.TextResourceContents{
				URI:      "file:///example.txt",
				MIMEType: "text/plain",
				Text:     "embedded body",
			}),
		},
	}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target, Format: "json"}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	require.False(t, forwarded.IsError)

	raw, err := os.ReadFile(target)
	require.NoError(t, err)
	// The bytes on disk are a complete, correct JSON encoding of the original
	// resource — nothing is lost at write time.
	assert.Contains(t, string(raw), "embedded body")
	assert.Contains(t, string(raw), "file:///example.txt")

	// Unmarshaling those same bytes back into mcp.CallToolResult, however,
	// fails outright — this is mcp-go v0.55.0 behavior, not a save_to_file bug.
	var restored mcp.CallToolResult
	unmarshalErr := json.Unmarshal(raw, &restored)
	require.Error(t, unmarshalErr, "mcp-go v0.55.0 cannot unmarshal an EmbeddedResource's interface-typed Resource field back into a concrete type")
	assert.Contains(t, unmarshalErr.Error(), "ResourceContents")

	// A generic map decode, by contrast, recovers everything losslessly —
	// this is the fallback callers need for EmbeddedResource-bearing files.
	var generic map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &generic))
	content, _ := generic["content"].([]interface{})
	require.Len(t, content, 1)
	block, _ := content[0].(map[string]interface{})
	resource, _ := block["resource"].(map[string]interface{})
	assert.Equal(t, "embedded body", resource["text"])
}

// (d) path outside configured roots -> tool error, no file written.
func TestMaybeSaveToFile_OutsideRoots_ToolErrorNoFile(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "out.txt")

	upstream := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("x")}}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	require.NotNil(t, forwarded)
	assert.True(t, forwarded.IsError)
	assert.Contains(t, toolErrorText(t, forwarded), "save_to_file:")

	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "no file should have been written outside the whitelist")
}

// (e) roots empty -> ErrDisabled message surfaced verbatim.
func TestMaybeSaveToFile_RootsEmpty_DisabledMessage(t *testing.T) {
	upstream := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("x")}}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: "/tmp/whatever/out.txt"}, saveToFileConfig{})
	require.True(t, handled)
	require.NotNil(t, forwarded)
	assert.True(t, forwarded.IsError)
	assert.Contains(t, toolErrorText(t, forwarded), "save_to_file is disabled: configure tool_output_roots")
}

// (f) upstream isError -> no file, error result forwarded unchanged (handled
// must be false so the caller's existing error path runs untouched).
func TestMaybeSaveToFile_UpstreamIsError_SkipsSave(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")

	upstream := mcp.NewToolResultError("upstream failure")

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target}, saveToFileConfig{
		Roots: []string{root},
	})
	assert.False(t, handled)
	assert.Nil(t, forwarded)

	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "no file should be written for an upstream error result")
}

// Invalid save_format is rejected with a save_to_file: error, no file written.
func TestMaybeSaveToFile_InvalidFormat_ToolError(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")
	upstream := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("x")}}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target, Format: "xml"}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	assert.True(t, forwarded.IsError)
	assert.Contains(t, toolErrorText(t, forwarded), "save_format")

	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr))
}

// Existing file without overwrite -> ErrExists surfaced as a tool error.
func TestMaybeSaveToFile_ExistsNoOverwrite_ToolError(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o644))

	upstream := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("new")}}
	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	assert.True(t, forwarded.IsError)

	onDisk, _ := os.ReadFile(target)
	assert.Equal(t, "old", string(onDisk), "existing file must be untouched on ErrExists")
}

// Overwrite=true replaces the existing file's content.
func TestMaybeSaveToFile_Overwrite_ReplacesContent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o644))

	upstream := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("new-content")}}
	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target, Overwrite: true}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	require.False(t, forwarded.IsError)

	onDisk, _ := os.ReadFile(target)
	assert.Equal(t, "new-content", string(onDisk))
}

// tool_output_max_bytes is enforced.
func TestMaybeSaveToFile_TooLarge_ToolError(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")
	upstream := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("0123456789")}}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target}, saveToFileConfig{
		Roots:    []string{root},
		MaxBytes: 5,
	})
	require.True(t, handled)
	assert.True(t, forwarded.IsError)

	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr))
}

// format:"text" with content blocks present but none of them text (an
// image-only response) must fail with a save_to_file error and write nothing
// — not silently produce a 0-byte file with a success envelope.
func TestMaybeSaveToFile_TextFormat_NoTextBlocks_ToolError(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")

	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewImageContent("aGVsbG8=", "image/png")},
	}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	require.NotNil(t, forwarded)
	assert.True(t, forwarded.IsError)
	assert.Contains(t, toolErrorText(t, forwarded), "no non-empty text content")

	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "no file should be written when format:text has nothing to save")
}

// A genuinely empty response (zero content blocks at all) is NOT the same
// case as "blocks present but none are text" — format:text still writes (an
// empty file), since there is nothing being silently dropped here.
func TestMaybeSaveToFile_TextFormat_ZeroBlocks_WritesEmptyFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")

	upstream := &mcp.CallToolResult{Content: []mcp.Content{}}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	require.False(t, forwarded.IsError)

	onDisk, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "", string(onDisk))
}

// A *mcp.TextContent (pointer) block must be recognized as text — both
// counted correctly and included in the saved text — not mis-classified as a
// non-text block and silently dropped.
func TestMaybeSaveToFile_TextFormat_PointerTextContentIncluded(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")

	upstream := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Type: "text", Text: "pointer-block-text"}},
	}

	forwarded, handled := maybeSaveToFile(upstream, saveToFileParams{Path: target}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	require.False(t, forwarded.IsError)

	onDisk, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "pointer-block-text", string(onDisk))

	env := decodeEnvelope(t, forwarded)
	assert.Equal(t, 1, env.ContentBlocks)
	assert.Equal(t, 0, env.NonTextBlocks, "*mcp.TextContent must be counted as text, not non-text")
}

// A legacy, non-*mcp.CallToolResult upstream result with save_format:json
// still honors save_to_file (json.Marshal(result) is written) instead of
// silently ignoring the request and falling through to the old
// forward/truncate behavior.
func TestMaybeSaveToFile_LegacyResultType_JSONFormat_Writes(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.json")

	// Anything that is not *mcp.CallToolResult exercises the legacy branch.
	legacyResult := map[string]interface{}{"status": "ok", "count": 3}

	forwarded, handled := maybeSaveToFile(legacyResult, saveToFileParams{Path: target, Format: "json"}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	require.NotNil(t, forwarded)
	require.False(t, forwarded.IsError)

	raw, err := os.ReadFile(target)
	require.NoError(t, err)

	var restored map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &restored))
	assert.Equal(t, "ok", restored["status"])
	assert.Equal(t, float64(3), restored["count"])
}

// A legacy, non-*mcp.CallToolResult upstream result with save_format:text (or
// the default) is rejected with an explicit error pointing at
// save_format="json" — "text" has nothing well-defined to concatenate for an
// arbitrary interface{}.
func TestMaybeSaveToFile_LegacyResultType_TextFormat_ToolError(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "out.txt")

	legacyResult := map[string]interface{}{"status": "ok"}

	forwarded, handled := maybeSaveToFile(legacyResult, saveToFileParams{Path: target}, saveToFileConfig{
		Roots: []string{root},
	})
	require.True(t, handled)
	require.NotNil(t, forwarded)
	assert.True(t, forwarded.IsError)
	assert.Contains(t, toolErrorText(t, forwarded), "unsupported upstream result type")

	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr))
}

// (g) all three call_tool_* variants accept save_to_file/save_format/save_overwrite.
func TestBuildCallToolVariantTool_AdvertisesSaveToFileParams(t *testing.T) {
	variants := []string{
		contracts.ToolVariantRead,
		contracts.ToolVariantWrite,
		contracts.ToolVariantDestructive,
	}
	for _, variant := range variants {
		t.Run(variant, func(t *testing.T) {
			tool := buildCallToolVariantTool(variant)

			saveToFileProp, ok := tool.InputSchema.Properties["save_to_file"].(map[string]any)
			require.True(t, ok, "schema must advertise 'save_to_file'")
			assert.Equal(t, "string", saveToFileProp["type"])

			saveFormatProp, ok := tool.InputSchema.Properties["save_format"].(map[string]any)
			require.True(t, ok, "schema must advertise 'save_format'")
			assert.Equal(t, "string", saveFormatProp["type"])
			assert.ElementsMatch(t, []any{"text", "json"}, saveFormatProp["enum"])

			overwriteProp, ok := tool.InputSchema.Properties["save_overwrite"].(map[string]any)
			require.True(t, ok, "schema must advertise 'save_overwrite'")
			assert.Equal(t, "boolean", overwriteProp["type"])

			// None of the new params are required — save_to_file is opt-in.
			for _, req := range tool.InputSchema.Required {
				assert.NotEqual(t, "save_to_file", req)
				assert.NotEqual(t, "save_format", req)
				assert.NotEqual(t, "save_overwrite", req)
			}
		})
	}
}

// --- (h) argument type validation: request.GetString/GetBool are lenient
// and silently default a wrong-typed argument, which would make save_to_file
// silently not happen instead of surfacing the caller's mistake ---

func TestValidateSaveToFileArgTypes_AllAbsent(t *testing.T) {
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"name": "server:tool"}
	assert.Nil(t, validateSaveToFileArgTypes(request))
}

func TestValidateSaveToFileArgTypes_AllWellTyped(t *testing.T) {
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"save_to_file":   "/tmp/out.txt",
		"save_format":    "json",
		"save_overwrite": true,
	}
	assert.Nil(t, validateSaveToFileArgTypes(request))
}

func TestValidateSaveToFileArgTypes_SaveToFileWrongType(t *testing.T) {
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"save_to_file": 42}
	result := validateSaveToFileArgTypes(request)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, toolErrorText(t, result), "save_to_file must be a string")
}

func TestValidateSaveToFileArgTypes_SaveFormatWrongType(t *testing.T) {
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"save_format": true}
	result := validateSaveToFileArgTypes(request)
	require.NotNil(t, result)
	assert.Contains(t, toolErrorText(t, result), "save_format must be a string")
}

func TestValidateSaveToFileArgTypes_SaveOverwriteWrongType(t *testing.T) {
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"save_overwrite": "true"}
	result := validateSaveToFileArgTypes(request)
	require.NotNil(t, result)
	assert.Contains(t, toolErrorText(t, result), "save_overwrite must be a bool")
}

// TestValidateSaveToFileArgTypes_InvalidSaveFormatEnum_RejectedPreDispatch
// pins the fix for the pre-dispatch/post-dispatch gap: previously the
// save_format enum ("text"|"json") was only checked inside maybeSaveToFile,
// which runs AFTER the upstream tool call, so
// call_tool_destructive(..., save_format:"xml") would run the (possibly
// destructive) upstream tool and only then discard the body with an
// "invalid save_format" error. validateSaveToFileArgTypes now rejects the
// bad enum value itself, before dispatch.
func TestValidateSaveToFileArgTypes_InvalidSaveFormatEnum_RejectedPreDispatch(t *testing.T) {
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"save_to_file": "/tmp/out.txt",
		"save_format":  "xml",
	}
	result := validateSaveToFileArgTypes(request)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	text := toolErrorText(t, result)
	assert.Contains(t, text, `invalid save_format "xml"`)
	assert.Contains(t, text, `must be "text" or "json"`)
}

// TestValidateSaveToFileArgTypes_SaveFormatValidEnums accepts every legal
// save_format value ("text", "json", and "" meaning "unset/default"), each
// alongside a non-empty save_to_file.
func TestValidateSaveToFileArgTypes_SaveFormatValidEnums(t *testing.T) {
	for _, format := range []string{"text", "json", ""} {
		t.Run(format, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{
				"save_to_file": "/tmp/out.txt",
				"save_format":  format,
			}
			assert.Nil(t, validateSaveToFileArgTypes(request))
		})
	}
}

// TestValidateSaveToFileArgTypes_SaveToFileEmptyString_Rejected pins the fix
// for the other pre-existing gap: a present-but-EMPTY save_to_file was
// silently ignored downstream (params.Path == "" is indistinguishable from
// "key absent"), which is exactly the failure mode this function's doc
// comment says it exists to prevent for other argument shapes.
func TestValidateSaveToFileArgTypes_SaveToFileEmptyString_Rejected(t *testing.T) {
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"save_to_file": ""}
	result := validateSaveToFileArgTypes(request)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Equal(t, "save_to_file: must be a non-empty absolute path", toolErrorText(t, result))
}

// TestValidateSaveToFileArgTypes_SaveFormatWithoutSaveToFile_Rejected pins
// that save_format supplied without a (non-empty) save_to_file is rejected
// rather than silently ignored.
func TestValidateSaveToFileArgTypes_SaveFormatWithoutSaveToFile_Rejected(t *testing.T) {
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"save_format": "json"}
	result := validateSaveToFileArgTypes(request)
	require.NotNil(t, result)
	assert.Equal(t, "save_to_file: save_format/save_overwrite require save_to_file", toolErrorText(t, result))
}

// TestValidateSaveToFileArgTypes_SaveOverwriteWithoutSaveToFile_Rejected
// mirrors the save_format case above for save_overwrite.
func TestValidateSaveToFileArgTypes_SaveOverwriteWithoutSaveToFile_Rejected(t *testing.T) {
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"save_overwrite": true}
	result := validateSaveToFileArgTypes(request)
	require.NotNil(t, result)
	assert.Equal(t, "save_to_file: save_format/save_overwrite require save_to_file", toolErrorText(t, result))
}

// TestValidateSaveToFileArgTypes_SaveToFileEmptyWithSaveFormat_EmptyPathErrorWins
// checks precedence when both are wrong: the empty-path error fires before
// the "requires save_to_file" error, since save_to_file is technically
// present (just invalid) rather than absent.
func TestValidateSaveToFileArgTypes_SaveToFileEmptyWithSaveFormat_EmptyPathErrorWins(t *testing.T) {
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"save_to_file": "",
		"save_format":  "json",
	}
	result := validateSaveToFileArgTypes(request)
	require.NotNil(t, result)
	assert.Equal(t, "save_to_file: must be a non-empty absolute path", toolErrorText(t, result))
}

// TestHandleCallToolVariant_SaveToFileInvalidFormatEnum_RejectedBeforeUpstream
// drives the real handler (not just the pure helper above) to prove the
// enum check runs BEFORE upstream dispatch, exactly like the existing
// TestHandleCallToolVariant_SaveToFileWrongType_RejectedBeforeUpstream test
// below does for a type mismatch — the mock proxy has no real upstream
// servers configured, so any error text other than the save_format one
// would mean the (possibly destructive) upstream call was attempted first.
func TestHandleCallToolVariant_SaveToFileInvalidFormatEnum_RejectedBeforeUpstream(t *testing.T) {
	mockProxy := &MCPProxyServer{
		upstreamManager: upstream.NewManager(zap.NewNop(), config.DefaultConfig(), nil, secret.NewResolver(), nil),
		logger:          zap.NewNop(),
		config:          &config.Config{},
	}
	ctx := context.Background()

	request := mcp.CallToolRequest{}
	request.Params.Name = contracts.ToolVariantDestructive
	request.Params.Arguments = map[string]any{
		"name":         "non-existent-server:some_tool",
		"args":         map[string]any{},
		"save_to_file": "/tmp/should-not-be-written.txt",
		"save_format":  "xml",
	}

	result, err := mockProxy.handleCallToolVariant(ctx, request, contracts.ToolVariantDestructive)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	text := toolErrorText(t, result)
	assert.Contains(t, text, `invalid save_format "xml"`)
	_, statErr := os.Stat("/tmp/should-not-be-written.txt")
	assert.True(t, os.IsNotExist(statErr), "upstream must never have been dispatched, so nothing was ever written")
}

// TestHandleCallToolVariant_SaveToFileWrongType_RejectedBeforeUpstream drives
// the real handler (not just the pure helper above) to prove the type check
// actually runs, and runs BEFORE the upstream dispatch — the mock proxy below
// has no real upstream servers configured at all, so any error text other
// than "save_to_file: parameter ... must be a ..." would mean the request
// either silently ignored the bad argument and fell through to (failed)
// upstream dispatch, or failed for an unrelated reason. Mirrors the
// no-live-upstream-needed mock pattern already used by
// TestHandleCallToolVariantAcceptsArgsObject in mcp_call_tool_args_test.go.
func TestHandleCallToolVariant_SaveToFileWrongType_RejectedBeforeUpstream(t *testing.T) {
	mockProxy := &MCPProxyServer{
		upstreamManager: upstream.NewManager(zap.NewNop(), config.DefaultConfig(), nil, secret.NewResolver(), nil),
		logger:          zap.NewNop(),
		config:          &config.Config{},
	}
	ctx := context.Background()

	request := mcp.CallToolRequest{}
	request.Params.Name = contracts.ToolVariantRead
	request.Params.Arguments = map[string]any{
		"name":         "non-existent-server:some_tool",
		"args":         map[string]any{},
		"save_to_file": 12345, // wrong type: must be a string
	}

	result, err := mockProxy.handleCallToolVariant(ctx, request, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError)
	assert.Contains(t, toolErrorText(t, result), "save_to_file: parameter save_to_file must be a string")
}

// --- (i) recountSaveOrTruncateTokenMetrics: this fix pass's own new token-
// metrics-correction logic, factored out of handleCallToolVariant's post-
// forward block into a
// pure function so it can be pinned directly (see mcp.go's call site for
// where this replaces the formerly-inlined block; route (b) from the fix
// pass brief, chosen because exercising this through handleCallToolVariant
// itself would need a full live-upstream test harness that does not exist
// in this package today — see the final report for what that leaves
// untested at the handleCallToolVariant integration level: the live-config
// hot-reload read and the RecordToolCall fall-through wiring around this
// call site are structurally reviewed but not pinned by a new test here). ---

// fakeTokenizer is a minimal tokens.Tokenizer for pinning
// recountSaveOrTruncateTokenMetrics without pulling in the real
// tiktoken-backed DefaultTokenizer (which needs a network-fetched encoding
// cache, unavailable in this offline sandbox — see the pre-existing
// internal/server/tokens package failures noted in the workstate).
type fakeTokenizer struct {
	tokens int
	err    error
}

func (f fakeTokenizer) CountTokens(string) (int, error)                    { return f.tokens, f.err }
func (f fakeTokenizer) CountTokensForModel(string, string) (int, error)    { return f.tokens, f.err }
func (f fakeTokenizer) CountTokensForEncoding(string, string) (int, error) { return f.tokens, f.err }
func (f fakeTokenizer) CountTokensInJSON(interface{}) (int, error)         { return f.tokens, f.err }
func (f fakeTokenizer) CountTokensInJSONForModel(interface{}, string) (int, error) {
	return f.tokens, f.err
}

func TestRecountSaveOrTruncateTokenMetrics_NilMetrics_NoPanic(t *testing.T) {
	mutated := recountSaveOrTruncateTokenMetrics(nil, true, true, "x", fakeTokenizer{tokens: 5})
	assert.False(t, mutated)
}

func TestRecountSaveOrTruncateTokenMetrics_NeitherFlag_OnlySetsSavedToFileFalse(t *testing.T) {
	tm := &storage.TokenMetrics{InputTokens: 10, OutputTokens: 9000, TotalTokens: 9010}
	mutated := recountSaveOrTruncateTokenMetrics(tm, false, false, "unused", fakeTokenizer{tokens: 5})
	assert.False(t, mutated)
	assert.False(t, tm.SavedToFile)
	// Untouched — this function must not correct the plain-truncation-free,
	// plain-save-free case at all.
	assert.Equal(t, 9000, tm.OutputTokens)
}

func TestRecountSaveOrTruncateTokenMetrics_SavedToFile_TokenizerSucceeds(t *testing.T) {
	tm := &storage.TokenMetrics{InputTokens: 10, OutputTokens: 90000, TotalTokens: 90010, Model: "gpt-4"}
	mutated := recountSaveOrTruncateTokenMetrics(tm, false, true, "the short envelope", fakeTokenizer{tokens: 7})
	require.True(t, mutated)
	assert.True(t, tm.SavedToFile)
	assert.False(t, tm.WasTruncated)
	assert.Equal(t, 7, tm.OutputTokens, "must be recounted from the envelope text, not left at the full upstream-body count")
	assert.Equal(t, 17, tm.TotalTokens)
}

func TestRecountSaveOrTruncateTokenMetrics_SavedToFile_NilTokenizer_ZerosOutputTokens(t *testing.T) {
	tm := &storage.TokenMetrics{InputTokens: 10, OutputTokens: 90000, TotalTokens: 90010}
	mutated := recountSaveOrTruncateTokenMetrics(tm, false, true, "the short envelope", nil)
	require.True(t, mutated)
	assert.True(t, tm.SavedToFile)
	assert.Equal(t, 0, tm.OutputTokens, "no tokenizer available — must still correct away from the full upstream-body count, not leave it")
	assert.Equal(t, tm.InputTokens, tm.TotalTokens)
}

func TestRecountSaveOrTruncateTokenMetrics_SavedToFile_TokenizerErrors_ZerosOutputTokens(t *testing.T) {
	tm := &storage.TokenMetrics{InputTokens: 10, OutputTokens: 90000, TotalTokens: 90010}
	mutated := recountSaveOrTruncateTokenMetrics(tm, false, true, "x", fakeTokenizer{err: errors.New("boom")})
	require.True(t, mutated)
	assert.Equal(t, 0, tm.OutputTokens)
	assert.Equal(t, tm.InputTokens, tm.TotalTokens)
}

func TestRecountSaveOrTruncateTokenMetrics_TruncatedWithoutSave_TokenizerSucceeds(t *testing.T) {
	tm := &storage.TokenMetrics{InputTokens: 10, OutputTokens: 90000, TotalTokens: 90010, Model: "gpt-4"}
	mutated := recountSaveOrTruncateTokenMetrics(tm, true, false, "truncated text", fakeTokenizer{tokens: 3})
	require.True(t, mutated)
	assert.False(t, tm.SavedToFile)
	assert.True(t, tm.WasTruncated)
	assert.Equal(t, 3, tm.OutputTokens)
	assert.Equal(t, 13, tm.TotalTokens)
}

func TestRecountSaveOrTruncateTokenMetrics_TruncatedWithoutSave_NilTokenizer_LeavesCountUnchanged(t *testing.T) {
	// Pre-existing, already-shipped behavior this fix pass does not change:
	// a truncated (but not saved) response with no tokenizer available keeps
	// its original full-body count rather than being zeroed — only the
	// save_to_file case gets the zero-fallback correction.
	tm := &storage.TokenMetrics{InputTokens: 10, OutputTokens: 90000, TotalTokens: 90010}
	mutated := recountSaveOrTruncateTokenMetrics(tm, true, false, "truncated text", nil)
	assert.False(t, mutated)
	assert.Equal(t, 90000, tm.OutputTokens)
	assert.Equal(t, 90010, tm.TotalTokens)
}

// --- test helpers ---

func decodeEnvelope(t *testing.T, forwarded *mcp.CallToolResult) saveToFileEnvelope {
	t.Helper()
	require.Len(t, forwarded.Content, 1)
	tc, ok := forwarded.Content[0].(mcp.TextContent)
	require.True(t, ok, "envelope must be a single TextContent block")
	var env saveToFileEnvelope
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &env))
	return env
}

func toolErrorText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, result.Content, 1)
	tc, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}
