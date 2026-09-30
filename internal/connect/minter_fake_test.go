package connect

import (
	"fmt"
	"sync"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// fakeMinter is a CredentialMinter for connect's own unit tests: it records
// every call and hands out deterministic, valid-format `mcp_cli_` secrets.
type fakeMinter struct {
	mu       sync.Mutex
	n        int
	issueErr error
	rotating bool

	intents []CredentialIntent
	issued  []*IssuedCredential
	commits int
	aborts  int

	// classify maps a secret to the state Classify reports; held is the set
	// of secrets HeldByRecord accepts.
	classify map[string]profile.CredentialState
	held     map[string]bool

	forgot     string
	forgotFor  string
	forgetErr  error
	forgetArgs []string
}

func newFakeMinter() *fakeMinter {
	return &fakeMinter{classify: map[string]profile.CredentialState{}, held: map[string]bool{}}
}

// secretN is the n-th (1-based) secret the fake issues.
func fakeSecret(n int) string { return fmt.Sprintf("%s%064x", auth.ClientTokenPrefixStr, n) }

func (m *fakeMinter) Issue(clientID string, intent CredentialIntent) (*IssuedCredential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.issueErr != nil {
		return nil, m.issueErr
	}
	m.n++
	prof := deref(intent.Profile)
	mode := "switchable"
	if prof != "" {
		mode = "locked"
	}
	if intent.Mode != nil {
		mode = *intent.Mode
	}
	is := &IssuedCredential{
		Secret: fakeSecret(m.n), TokenName: "client-" + clientID, Profile: prof, Mode: mode, Rotating: m.rotating,
	}
	m.intents = append(m.intents, intent)
	m.issued = append(m.issued, is)
	m.held[is.Secret] = true
	m.classify[is.Secret] = profile.CredentialStateClient
	return is, nil
}

func (m *fakeMinter) Commit(string, CredentialIntent, *IssuedCredential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commits++
	return nil
}

func (m *fakeMinter) Abort(_ string, _ CredentialIntent, is *IssuedCredential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aborts++
	delete(m.held, is.Secret)
	return nil
}

func (m *fakeMinter) Classify(_ string, secret string) profile.CredentialState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.classify[secret]; ok {
		return st
	}
	return profile.CredentialStateRevoked
}

func (m *fakeMinter) HeldByRecord(_ string, secret string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.held[secret]
}

func (m *fakeMinter) ForgetUnheld(clientID, restored string, _ CredentialIntent) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.forgetArgs = append(m.forgetArgs, clientID+"|"+restored)
	if m.forgetErr != nil {
		return "", m.forgetErr
	}
	if restored != "" && m.held[restored] {
		return "", nil
	}
	return m.forgot, nil
}

// lastSecret is the most recently issued secret ("" when none).
func (m *fakeMinter) lastSecret() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.issued) == 0 {
		return ""
	}
	return m.issued[len(m.issued)-1].Secret
}

// withFakeMinter installs a fresh fake on svc and returns it.
func withFakeMinter(svc *Service) *fakeMinter {
	m := newFakeMinter()
	svc.WithCredentialMinter(m)
	return m
}
