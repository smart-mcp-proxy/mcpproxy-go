//go:build !server

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
)

// ClientsListData is the `data` of GET /clients?profile=&client= as a generic
// JSON object ({clients, routing, warnings}). The MCP `profiles` tool's
// list_clients returns it without routing and the config paths (Spec 108-h, H3).
func (s *Server) ClientsListData(ctx context.Context, profileFilter, clientFilter string) (map[string]any, error) {
	resp, err := s.clientsListResponse(ctx, profileFilter, clientFilter)
	if err != nil {
		return nil, &requestError{http.StatusServiceUnavailable, err.Error()}
	}
	return toDataMap(resp)
}

// ClientBindingData is the `data` of PUT /clients/{client}/binding as a generic
// JSON object ({client, warnings}): the decorated client row after a change and
// the warnings that concern it (F17).
func (s *Server) ClientBindingData(ctx context.Context, clientID string) (map[string]any, error) {
	view, warnings := s.clientViewAndWarnings(ctx, clientID)
	if view == nil {
		return nil, &requestError{http.StatusInternalServerError, "failed to read the client after the change"}
	}
	return toDataMap(ClientBindingResponse{Client: *view, Warnings: warnings})
}

// toDataMap converts a typed response to the generic JSON object a REST client
// would decode from its `data`.
func toDataMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
