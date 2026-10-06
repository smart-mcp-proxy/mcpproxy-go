package headerfwd

import (
	"context"
	"strings"

	"github.com/mark3labs/mcp-go/client/transport"
)

// Policy is the live per-server forwarding policy (FR-010).
type Policy struct {
	Enabled   bool
	Allow     []string
	Static    map[string]string
	Transport string // resolved: "http" or "streamable-http" forward; anything else does not
}

func httpTransport(t string) bool {
	switch strings.ToLower(t) {
	case "http", "streamable-http":
		return true
	}
	return false
}

func staticCollision(static map[string]string, name string) bool {
	for k := range static {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// Outbound computes the per-server outbound set from the edge snapshot (FR-010).
func Outbound(s Snapshot, p Policy) Snapshot {
	if !p.Enabled || !httpTransport(p.Transport) || s.IsEmpty() {
		return Snapshot{}
	}
	out := map[string]string{}
	total := 0
	for _, n := range p.Allow {
		cn := canon(n)
		if _, dup := out[cn]; dup || nameProblem(n) != "" || staticCollision(p.Static, cn) {
			continue
		}
		v, ok := s.h[cn]
		if !ok || v == "" || len(v) > MaxValueBytes || total+len(v) > MaxTotalBytes || !validValue(v) {
			continue
		}
		total += len(v)
		out[cn] = v
	}
	return newSnapshot(out)
}

// HeaderFunc returns the transport header func. It reads key B only and
// re-applies the deny list and static-name collision (defense in depth). It
// returns a fresh map on every call, or nil when there is nothing to forward.
func HeaderFunc(static map[string]string) transport.HTTPHeaderFunc {
	return func(ctx context.Context) map[string]string {
		s, ok := OutboundFrom(ctx)
		if !ok || s.IsEmpty() {
			return nil
		}
		out := make(map[string]string, len(s.h))
		for n, v := range s.h {
			if Denied(n) || staticCollision(static, n) {
				continue
			}
			out[n] = v
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
}
