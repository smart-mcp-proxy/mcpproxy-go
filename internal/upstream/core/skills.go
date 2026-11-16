package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/client"
	mcpclienttransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	mptransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// skillsRequestSeq numbers our raw skills requests. The ids are strings: the
// mcp-go client numbers its own requests with integers, and stdio and SSE
// match responses by id, so an integer id of ours could collide with one.
var skillsRequestSeq atomic.Uint64

func nextSkillsRequestID() mcp.RequestId {
	return mcp.NewRequestId(fmt.Sprintf("mcpproxy-skills-%d", skillsRequestSeq.Add(1)))
}

// maxSkillsListBytes is skills.MaxListBytes; a variable so a test can lower it.
var maxSkillsListBytes int64 = skills.MaxListBytes

// trimSkillListing cuts a listing to at most limit items in total, dropping
// undecodable entries before decoded ones.
func trimSkillListing(entries []skills.Entry, entryErrs []skills.EntryError, limit int) ([]skills.Entry, []skills.EntryError) {
	if len(entries) > limit {
		entries = entries[:limit]
	}
	if room := limit - len(entries); len(entryErrs) > room {
		entryErrs = entryErrs[:room]
	}
	return entries, entryErrs
}

// SetExposeSkills pushes the per-server expose_skills value to a running
// connection, like SetExposePrompts. nil means "not set" (exposed).
func (c *Client) SetExposeSkills(expose *bool) {
	c.exposeSkills.Store(expose)
}

// SupportsSkills reports whether the upstream announced the skills extension.
// The extension requires the resources capability too, so one without the
// other is not enough.
func (c *Client) SupportsSkills() bool {
	c.mu.RLock()
	info := c.serverInfo
	c.mu.RUnlock()
	if info == nil || info.Capabilities.Resources == nil {
		return false
	}
	_, ok := info.Capabilities.Extensions[skills.ExtensionKey]
	return ok
}

// skillsGuard returns the client and transport type for a skills request, or
// an error when not connected.
func (c *Client) skillsGuard() (cl *client.Client, transportType string, err error) {
	c.mu.RLock()
	cl = c.client
	serverInfo := c.serverInfo
	transportType = c.transportType
	c.mu.RUnlock()
	if !c.IsConnected() || cl == nil {
		return nil, "", fmt.Errorf("client not connected")
	}
	if serverInfo == nil {
		return nil, "", fmt.Errorf("server info not available")
	}
	return cl, transportType, nil
}

// skillsRPC sends one raw JSON-RPC request and returns its result. A JSON-RPC
// error becomes an error.
//
// maxBytes bounds the response before it is buffered on the streamable-HTTP
// transport only, where each request has its own response body. On stdio and
// SSE the response arrives on the stream every message shares and is not
// bounded per request: that is the exposure any tool result from the same
// server already has, and callers still check lengths after the fact.
func (c *Client) skillsRPC(ctx context.Context, method string, params map[string]any, maxBytes int64) (json.RawMessage, error) {
	cl, _, err := c.skillsGuard()
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 {
		ctx = mptransport.WithResponseLimit(ctx, maxBytes)
	}
	resp, err := cl.GetTransport().SendRequest(ctx, mcpclienttransport.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      nextSkillsRequestID(),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", method, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s failed: %s (code %d)", method, resp.Error.Message, resp.Error.Code)
	}
	return resp.Result, nil
}

// ListSkills pages skills/list. It returns nothing, and sends nothing, when the
// server does not announce skills or expose_skills is false. A bound that cuts
// the listing short comes back as a *skills.TruncatedError next to the partial
// entries; it is never swallowed.
func (c *Client) ListSkills(ctx context.Context) (entries []skills.Entry, entryErrs []skills.EntryError, err error) {
	if _, _, gerr := c.skillsGuard(); gerr != nil {
		return nil, nil, gerr
	}
	// Not asking is reported as such, never as an empty listing: the caller
	// unlists what an empty listing no longer contains.
	if !c.SupportsSkills() {
		return nil, nil, skills.ErrNotServed
	}
	if v := c.exposeSkills.Load(); v != nil && !*v {
		return nil, nil, skills.ErrNotServed
	}

	c.mu.RLock()
	transportType := c.transportType
	c.mu.RUnlock()
	if transportType == "sse" {
		c.sseRequestMu.Lock()
		defer c.sseRequestMu.Unlock()
	}

	release, err := c.beginRequest(ctx, "skills/list")
	if err != nil {
		return nil, nil, err
	}
	defer release()

	cursor := ""
	seen := 0
	var listBytes int64
	for page := 1; ; page++ {
		if cerr := ctx.Err(); cerr != nil {
			return nil, nil, fmt.Errorf("skills/list failed: %w", cerr)
		}
		if !skills.ShouldContinue(ctx) {
			return nil, nil, skills.ErrStopped
		}
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, rerr := c.skillsRPC(ctx, "skills/list", params, skills.MaxListResponseBytes)
		if rerr != nil {
			return nil, nil, rerr
		}
		// All pages together have a budget too: what a listing says ends up
		// in records, so its total size is what bounds them.
		if listBytes += int64(len(raw)); listBytes > maxSkillsListBytes {
			c.logger.Warn("upstream skills listing reached its byte budget; stopping",
				zap.String("server", c.config.Name), zap.Int64("budget", maxSkillsListBytes))
			return entries, entryErrs, &skills.TruncatedError{Bound: skills.BoundResponseBytes, Limit: maxSkillsListBytes}
		}
		decoded, derr := skills.DecodeListResult(raw)
		if derr != nil {
			return nil, nil, derr
		}
		// Index is the position in the whole listing, not in its page.
		for _, ee := range decoded.Errors {
			ee.Index += seen
			entryErrs = append(entryErrs, ee)
		}
		seen += len(decoded.Entries) + len(decoded.Errors)
		entries = append(entries, decoded.Entries...)

		// The cap counts every listed item, decoded or not: a malformed entry
		// with an address still becomes a record downstream.
		consumed := len(entries) + len(entryErrs)
		if consumed > skills.MaxSkillsPerServer || (consumed == skills.MaxSkillsPerServer && decoded.NextCursor != "") {
			entries, entryErrs = trimSkillListing(entries, entryErrs, skills.MaxSkillsPerServer)
			c.logger.Warn("upstream skills listing reached entry cap; stopping",
				zap.String("server", c.config.Name), zap.Int("cap", skills.MaxSkillsPerServer))
			return entries, entryErrs, &skills.TruncatedError{Bound: skills.BoundEntries, Limit: skills.MaxSkillsPerServer}
		}
		if decoded.NextCursor == "" {
			return entries, entryErrs, nil
		}
		if page >= skills.MaxListPages {
			c.logger.Warn("upstream skills listing reached page cap; stopping",
				zap.String("server", c.config.Name), zap.Int("cap", skills.MaxListPages))
			return entries, entryErrs, &skills.TruncatedError{Bound: skills.BoundPages, Limit: skills.MaxListPages}
		}
		cursor = decoded.NextCursor
	}
}

// GetSkill reads one entry with skills/get.
func (c *Client) GetSkill(ctx context.Context, uri string) (skills.Entry, error) {
	if _, _, err := c.skillsGuard(); err != nil {
		return skills.Entry{}, err
	}
	if !c.SupportsSkills() {
		return skills.Entry{}, fmt.Errorf("server %s does not announce skills", c.config.Name)
	}
	if v := c.exposeSkills.Load(); v != nil && !*v {
		return skills.Entry{}, fmt.Errorf("skills not exposed for server %s", c.config.Name)
	}
	c.mu.RLock()
	transportType := c.transportType
	c.mu.RUnlock()
	if transportType == "sse" {
		c.sseRequestMu.Lock()
		defer c.sseRequestMu.Unlock()
	}
	release, err := c.beginRequest(ctx, "skills/get")
	if err != nil {
		return skills.Entry{}, err
	}
	defer release()

	raw, err := c.skillsRPC(ctx, "skills/get", map[string]any{"uri": uri}, skills.MaxListResponseBytes)
	if err != nil {
		return skills.Entry{}, err
	}
	return skills.DecodeGetResult(raw)
}

// classifySkillText returns the bytes of a text content item and whether they
// may be treated as text. Text that is not valid UTF-8 is binary.
func classifySkillText(s string) ([]byte, bool) {
	return []byte(s), utf8.ValidString(s)
}

// ReadSkillFile reads one listed file with resources/read. The caller verifies
// the digest (skills.VerifyFile); the length is checked here, before any
// hashing, so a response that disagrees with its listing is refused cheaply.
func (c *Client) ReadSkillFile(ctx context.Context, ref skills.FileRef) (content []byte, isText bool, mimeType string, err error) {
	cl, transportType, err := c.skillsGuard()
	if err != nil {
		return nil, false, "", err
	}
	if !c.SupportsSkills() {
		return nil, false, "", fmt.Errorf("server %s does not announce skills", c.config.Name)
	}
	if v := c.exposeSkills.Load(); v != nil && !*v {
		return nil, false, "", fmt.Errorf("skills not exposed for server %s", c.config.Name)
	}
	if transportType == "sse" {
		c.sseRequestMu.Lock()
		defer c.sseRequestMu.Unlock()
	}
	release, err := c.beginRequest(ctx, string(mcp.MethodResourcesRead))
	if err != nil {
		return nil, false, "", err
	}
	defer release()

	// Six bytes per source byte covers a fully \u-escaped JSON string, which
	// costs more than base64; 64 KiB covers the envelope.
	limited := mptransport.WithResponseLimit(ctx, 6*ref.Size+64<<10)
	req := mcp.ReadResourceRequest{}
	req.Params.URI = ref.URI
	res, err := cl.ReadResource(limited, req)
	if err != nil {
		return nil, false, "", fmt.Errorf("resources/read failed for %s: %w", ref.URI, err)
	}
	if res == nil || len(res.Contents) != 1 {
		n := 0
		if res != nil {
			n = len(res.Contents)
		}
		return nil, false, "", fmt.Errorf("resources/read for %s returned %d content items, want exactly 1", ref.URI, n)
	}

	switch item := res.Contents[0].(type) {
	case mcp.TextResourceContents:
		if item.URI != ref.URI {
			return nil, false, "", fmt.Errorf("resources/read for %s returned content for uri %s", ref.URI, item.URI)
		}
		content, isText = classifySkillText(item.Text)
		mimeType = item.MIMEType
	case mcp.BlobResourceContents:
		if item.URI != ref.URI {
			return nil, false, "", fmt.Errorf("resources/read for %s returned content for uri %s", ref.URI, item.URI)
		}
		content, err = base64.StdEncoding.DecodeString(item.Blob)
		if err != nil {
			return nil, false, "", fmt.Errorf("resources/read for %s: blob is not valid base64: %w", ref.URI, err)
		}
		isText = false
		mimeType = item.MIMEType
	default:
		return nil, false, "", fmt.Errorf("resources/read for %s returned an unsupported content type %T", ref.URI, res.Contents[0])
	}
	if int64(len(content)) != ref.Size {
		// A typed error, so the caller reports size_mismatch, not a read failure.
		return nil, false, "", &skills.UnusableError{Code: skills.ReasonSizeMismatch, Detail: fmt.Sprintf("resources/read for %s returned %d bytes, listed size is %d", ref.URI, len(content), ref.Size)}
	}
	return content, isText, mimeType, nil
}
