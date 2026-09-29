package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// notifyClientConnected best-effort relays a successful `mcpproxy connect`
// write to a running daemon so storage.OnboardingState.ClientConnectedAt
// (Spec 109-b FR-042) reflects CLI connects the same way the Web UI and
// macOS tray already do through POST /api/v1/connect/{client}.
//
// `mcpproxy connect` writes the client's config file directly, without going
// through that endpoint, on purpose — the command must keep working when no
// daemon is running at all. That means it never reaches
// httpapi.recordClientConnected, so before this the CLI path was a silent
// gap: ClientConnectedAt only ever reflected REST/tray connects (review
// round 6 finding). This closes it by relaying the event to a daemon IF one
// happens to be reachable, via the existing onboarding/mark endpoint.
//
// It is intentionally fire-and-forget: the connect write has already
// succeeded and been reported to the user by the time this runs, so no
// daemon running, a stale socket, or any request failure here is swallowed
// rather than surfaced — this is bookkeeping, never a reason to fail or warn
// about the connect command itself. It also does not fall back to opening
// the on-disk database directly when no daemon is reachable: config.db is
// exclusively locked while a daemon does run, so a fallback open could only
// ever help in the "no daemon" case, where the very state it would update
// has no live reader anyway until the daemon next starts and recomputes it.
func notifyClientConnected(cfg *config.Config, clientID string) {
	notifyClientsConnected(cfg, []string{clientID})
}

// notifyClientDisconnected mirrors a successful local CLI disconnect into a
// running daemon's presence record. It never makes disconnect depend on the
// daemon being available.
func notifyClientDisconnected(cfg *config.Config, clientID string) {
	notifyClientConnectionChange(cfg, "disconnected_client_id", clientID)
}

func notifyClientConnectionChange(cfg *config.Config, field, clientID string) {
	notifyClientConnectionsChanged(cfg, field, []string{clientID})
}

// notifyClientsConnected is the batch form used by `connect --all`. It
// resolves the daemon (socket stat + status probe) once and relays every
// client concurrently under a single overall deadline.
func notifyClientsConnected(cfg *config.Config, clientIDs []string) {
	notifyClientConnectionsChanged(cfg, "connected_client_id", clientIDs)
}

func notifyClientConnectionsChanged(cfg *config.Config, field string, clientIDs []string) {
	if len(clientIDs) == 0 {
		return
	}
	client, ok := newDaemonClient(cfg, nil)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), daemonProbeTimeout)
	defer cancel()

	var wg sync.WaitGroup
	for _, id := range clientIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			body, err := json.Marshal(map[string]string{field: id})
			if err != nil {
				return
			}
			resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/onboarding/mark", body)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
		}(id)
	}
	wg.Wait()
}
