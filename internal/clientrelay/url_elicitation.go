package clientrelay

import (
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// urlElicitationTTL bounds how long a URL-mode elicitation waits for its
// completion notification; an abandoned browser flow sends none.
const urlElicitationTTL = time.Hour

// URLElicitations remembers which client session each URL-mode elicitation of
// one upstream went to, so the upstream's notifications/elicitation/complete
// reaches that client and no other. The zero value is ready to use.
type URLElicitations struct {
	mu       sync.Mutex
	sessions map[string]urlElicitation
}

type urlElicitation struct {
	session mcpserver.ClientSession
	expires time.Time
}

// add records that elicitation id went to s, until the returned func or its
// completion removes it. A nil session or an empty id records nothing.
func (u *URLElicitations) add(id string, s mcpserver.ClientSession) (forget func()) {
	if id == "" || s == nil {
		return func() {}
	}
	now := time.Now()
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.sessions == nil {
		u.sessions = make(map[string]urlElicitation)
	}
	for k, e := range u.sessions {
		if now.After(e.expires) {
			delete(u.sessions, k)
		}
	}
	u.sessions[id] = urlElicitation{session: s, expires: now.Add(urlElicitationTTL)}
	return func() {
		u.mu.Lock()
		if e, ok := u.sessions[id]; ok && e.session == s {
			delete(u.sessions, id)
		}
		u.mu.Unlock()
	}
}

// Complete forwards an upstream's notifications/elicitation/complete to the
// session its elicitation went to, without blocking. It reports whether it
// did; a completion for an unknown or expired id is dropped, as a client
// would ignore it.
func (u *URLElicitations) Complete(n mcp.JSONRPCNotification) bool {
	id, _ := n.Params.AdditionalFields["elicitationId"].(string)
	u.mu.Lock()
	e, ok := u.sessions[id]
	delete(u.sessions, id)
	u.mu.Unlock()
	if !ok || time.Now().After(e.expires) {
		return false
	}
	select {
	case e.session.NotificationChannel() <- n:
		return true
	default:
		return false
	}
}
