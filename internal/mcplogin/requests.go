package mcplogin

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// Lifetimes of the in-memory login state.
const (
	// RequestLifetime is how long an authorization request lives.
	RequestLifetime = 10 * time.Minute
	// CodeLifetime is how long an authorization code is usable.
	CodeLifetime = 60 * time.Second
	// TombstoneLifetime is how long a consumed code is remembered so that a
	// second presentation can be told from an unknown code.
	TombstoneLifetime = 10 * time.Minute
)

// Errors returned by Store.
var (
	// ErrTooManyPending means MaxPendingRequests requests are already pending.
	ErrTooManyPending = errors.New("mcplogin: too many pending authorization requests")
	// ErrRequestNotFound means the request does not exist or has expired.
	ErrRequestNotFound = errors.New("mcplogin: authorization request not found")
	// ErrAlreadyDecided means the request left the pending state already.
	ErrAlreadyDecided = errors.New("mcplogin: authorization request already decided")
	// ErrCodeUnknown means the code is unknown, expired, cancelled or burned.
	ErrCodeUnknown = errors.New("mcplogin: authorization code unknown")
	// ErrCodeReused means a consumed code was presented again inside the
	// tombstone window. Consume returns the grant id it produced.
	ErrCodeReused = errors.New("mcplogin: authorization code already used")
)

// Status is the state of an authorization request.
type Status string

// Request states. A request is decided once: pending moves to approved or
// denied, and an approved request can still become cancelled until its code is
// consumed. Consumed is the final state of a request whose code was exchanged.
const (
	// StatusPending awaits the administrator's decision.
	StatusPending Status = "pending"
	// StatusApproved has a code issued and not yet exchanged.
	StatusApproved Status = "approved"
	// StatusDenied was refused by the administrator.
	StatusDenied Status = "denied"
	// StatusExpired ran out of time.
	StatusExpired Status = "expired"
	// StatusCancelled was withdrawn (client revoked or forgotten, feature off).
	StatusCancelled Status = "cancelled"
	// StatusConsumed had its code exchanged.
	StatusConsumed Status = "consumed"
)

// Decision is what the administrator chose when approving a request.
type Decision struct {
	// ClientID is the client hub id the login is for.
	ClientID string
	// NewClient is true when approval creates the client.
	NewClient bool
	// DisplayName is the display name of a new client.
	DisplayName string
	// Profile is the profile the client is bound to.
	Profile string
	// Mode is the binding mode.
	Mode string
	// BindingRevision is the binding revision the consent page displayed.
	BindingRevision string
	// ReplaceGrantID is the earlier login to revoke on success, if any.
	ReplaceGrantID string
	// ActorKind and ActorName identify the approving administrator.
	ActorKind string
	ActorName string
}

// Request is one authorization request, kept in memory only.
type Request struct {
	ID             string
	RegistrationID string
	RedirectURI    string
	State          string
	CodeChallenge  string
	Resource       string
	Issuer         string
	// PathClient is the client id from the resource path, empty for the
	// generic address.
	PathClient string
	Status     Status
	Decision   Decision
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

type codeEntry struct {
	requestID  string
	expiresAt  time.Time
	consumed   bool
	consumedAt time.Time
	grantID    string // empty when the commit failed
}

// Store holds authorization requests and their codes. It is safe for
// concurrent use; one mutex guards everything and nothing blocks under it
// except the commit callback of Consume, which is its single-use boundary.
type Store struct {
	key   []byte
	now   func() time.Time
	mu    sync.Mutex
	reqs  map[string]*Request
	codes map[string]*codeEntry // keyed by HashSecret(kind code)
}

// NewStore returns an empty Store. key hashes codes; now is the clock (nil
// means time.Now).
func NewStore(key []byte, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{key: key, now: now, reqs: map[string]*Request{}, codes: map[string]*codeEntry{}}
}

// live returns the request with lazy expiry applied. Caller holds s.mu.
func (s *Store) live(id string) *Request {
	r := s.reqs[id]
	if r == nil {
		return nil
	}
	if (r.Status == StatusPending || r.Status == StatusApproved) && !s.now().Before(r.ExpiresAt) {
		r.Status = StatusExpired
	}
	if r.Status == StatusExpired {
		return nil
	}
	return r
}

func (s *Store) pendingCountLocked() int {
	n := 0
	for id := range s.reqs {
		if r := s.live(id); r != nil && r.Status == StatusPending {
			n++
		}
	}
	return n
}

// Create stores a new pending request built from req (ID, Status, CreatedAt
// and ExpiresAt are assigned here). It fails with ErrTooManyPending at the cap.
func (s *Store) Create(req Request) (Request, error) {
	id, err := randomBase62(22)
	if err != nil {
		return Request{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	if s.pendingCountLocked() >= MaxPendingRequests {
		return Request{}, ErrTooManyPending
	}
	now := s.now()
	req.ID = id
	req.Status = StatusPending
	req.Decision = Decision{}
	req.CreatedAt = now
	req.ExpiresAt = now.Add(RequestLifetime)
	s.reqs[id] = &req
	return req, nil
}

// Get returns a copy of the request. An expired request reads as not found.
func (s *Store) Get(id string) (Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.live(id)
	if r == nil {
		return Request{}, false
	}
	return *r, true
}

// ListPending returns the pending requests, oldest first.
func (s *Store) ListPending() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Request
	for id := range s.reqs {
		if r := s.live(id); r != nil && r.Status == StatusPending {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Approve moves a pending request to approved, records the decision and
// returns the single-use code (valid CodeLifetime). A request that is no
// longer pending gives ErrAlreadyDecided.
func (s *Store) Approve(id string, d Decision) (string, error) {
	code, err := randomSecret()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	r := s.live(id)
	if r == nil {
		return "", ErrRequestNotFound
	}
	if r.Status != StatusPending {
		return "", ErrAlreadyDecided
	}
	r.Status = StatusApproved
	r.Decision = d
	s.codes[HashSecret(s.key, KindCode, code)] = &codeEntry{requestID: id, expiresAt: s.now().Add(CodeLifetime)}
	return code, nil
}

// Deny moves a pending request to denied.
func (s *Store) Deny(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.live(id)
	if r == nil {
		return ErrRequestNotFound
	}
	if r.Status != StatusPending {
		return ErrAlreadyDecided
	}
	r.Status = StatusDenied
	return nil
}

func (s *Store) cancelWhere(match func(*Request) bool) int {
	n := 0
	for id := range s.reqs {
		r := s.live(id)
		if r == nil || (r.Status != StatusPending && r.Status != StatusApproved) {
			continue
		}
		if match(r) {
			r.Status = StatusCancelled
			n++
		}
	}
	return n
}

// CancelForClient cancels the pending and approved-but-unconsumed requests
// whose path client or decided client is clientID. Their codes become unusable.
func (s *Store) CancelForClient(clientID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelWhere(func(r *Request) bool {
		return r.PathClient == clientID || (r.Status == StatusApproved && r.Decision.ClientID == clientID)
	})
}

// CancelAll cancels every pending and approved-but-unconsumed request.
func (s *Store) CancelAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelWhere(func(*Request) bool { return true })
}

// Consume exchanges a code. Under the store lock it checks that the code is
// known, unexpired, unconsumed and that its request is still approved, then
// calls commit with a copy of the request. On success the request is consumed
// and a tombstone {consumed_at, grant_id} is kept for TombstoneLifetime. If
// commit fails the code is burned too (single use) and the error is returned.
// Presenting a consumed code again inside the tombstone window returns
// ErrCodeReused together with the grant id it produced; afterwards the code is
// unknown. This holds for a code whose commit failed as well, with an empty
// grant id: callers must treat an empty grant id as "nothing to revoke".
func (s *Store) Consume(code string, commit func(Request) (grantID string, err error)) (Request, string, error) {
	h := HashSecret(s.key, KindCode, code)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	now := s.now()
	e := s.codes[h]
	if e == nil {
		return Request{}, "", ErrCodeUnknown
	}
	if e.consumed {
		if now.Before(e.consumedAt.Add(TombstoneLifetime)) {
			return Request{}, e.grantID, ErrCodeReused
		}
		delete(s.codes, h)
		return Request{}, "", ErrCodeUnknown
	}
	if !now.Before(e.expiresAt) {
		delete(s.codes, h)
		return Request{}, "", ErrCodeUnknown
	}
	r := s.live(e.requestID)
	if r == nil || r.Status != StatusApproved {
		delete(s.codes, h)
		return Request{}, "", ErrCodeUnknown
	}
	e.consumed = true
	e.consumedAt = now
	r.Status = StatusConsumed
	grantID, err := commit(*r)
	if err != nil {
		return Request{}, "", err
	}
	e.grantID = grantID
	return *r, grantID, nil
}

// Sweep removes expired requests and codes and tombstones past their window.
func (s *Store) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
}

// sweepLocked drops dead entries. Create, Approve and Consume call it, so the
// maps stay bounded by what is live (at most MaxPendingRequests pending plus
// ten minutes of decided state) without an external janitor. Caller holds s.mu.
func (s *Store) sweepLocked() {
	now := s.now()
	for id, r := range s.reqs {
		if !now.Before(r.ExpiresAt) {
			delete(s.reqs, id)
		}
	}
	for h, e := range s.codes {
		if e.consumed {
			if !now.Before(e.consumedAt.Add(TombstoneLifetime)) {
				delete(s.codes, h)
			}
		} else if !now.Before(e.expiresAt) {
			delete(s.codes, h)
		}
	}
}
