package storage

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 132 (browser login for MCP clients), storage layer.
//
// HARD RULE (FR-002, research R10): nothing here creates a bucket or a key
// unless a browser-login write actually happens. Every bucket below is LAZY:
// it is created with CreateBucketIfNotExists on a write path, never from
// initBuckets, and every reader treats a nil bucket as empty. A database that
// never saw a browser login therefore stays byte-for-byte what it was.
const (
	// OAuthRegistrationsBucket holds dynamic client registrations, key client_id.
	OAuthRegistrationsBucket = "oauth_registrations"
	// OAuthGrantsBucket holds browser-login grants, key grant_id.
	OAuthGrantsBucket = "oauth_grants"
	// OAuthGrantAccessIndexBucket maps access-token hash -> grant id. It is a
	// sibling bucket (not a nested one) so ForEach over OAuthGrantsBucket only
	// ever sees grant rows.
	OAuthGrantAccessIndexBucket = "oauth_grant_access_index"
	// OAuthClientBindingsBucket holds the binding copy of a client that has at
	// least one grant record, key client id.
	OAuthClientBindingsBucket = "oauth_client_bindings"
	// OAuthLoginEverUsedKey is the MetaBucket marker holding the RFC 3339 time
	// of the first stored grant. Never deleted.
	OAuthLoginEverUsedKey = "oauth_login_ever_used"
)

// Registration states (data-model.md). A denial is not a state.
const (
	OAuthRegistrationPending    = "pending"
	OAuthRegistrationAuthorized = "authorized"
	OAuthRegistrationApproved   = "approved"
)

// Grant revoke reasons.
const (
	OAuthRevokeAdmin         = "admin"
	OAuthRevokeClientRevoked = "client_revoked"
	OAuthRevokeReplaced      = "replaced"
	OAuthRevokeReplay        = "replay"
)

// Bounds from the data model.
const (
	// MaxUnapprovedOAuthRegistrations caps pending + authorized rows together.
	MaxUnapprovedOAuthRegistrations = 50
	// OAuthPendingRegistrationTTL is how long a pending row lives from created_at.
	OAuthPendingRegistrationTTL = 15 * time.Minute
	// OAuthAuthorizedRegistrationTTL is how long an authorized row lives from authorized_at.
	OAuthAuthorizedRegistrationTTL = 24 * time.Hour
	// OAuthDeadGrantRetention is how long a revoked or lapsed grant is kept for display.
	OAuthDeadGrantRetention = 30 * 24 * time.Hour
)

// OAuthBindingAbsent is the revision of a client that has neither an API-key
// record nor a browser-login binding.
const OAuthBindingAbsent = "absent"

// Sentinel errors of the browser-login storage surface.
var (
	// ErrOAuthRegistrationGone: the registration is not stored (evicted, swept
	// or deleted with its last grant); it is never recreated from a code.
	ErrOAuthRegistrationGone = errors.New("oauth registration not found")
	// ErrOAuthClientExists: a decision that creates a new client found the id
	// already held by an API-key record or a browser-login binding.
	ErrOAuthClientExists = errors.New("client already exists")
	// ErrOAuthBindingChanged: the client's binding revision is no longer the
	// one the decision was made against.
	ErrOAuthBindingChanged = errors.New("client binding changed since it was reviewed")
	// ErrOAuthGrantNotFound: no such grant.
	ErrOAuthGrantNotFound = errors.New("oauth grant not found")
)

// OAuthActor names who approved a login.
type OAuthActor struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// OAuthRegistration is a dynamic client registration (data-model.md).
type OAuthRegistration struct {
	ClientID                string     `json:"client_id"`
	ReportedName            string     `json:"reported_name,omitempty"`
	RedirectURIs            []string   `json:"redirect_uris"`
	SoftwareID              string     `json:"software_id,omitempty"`
	SoftwareVersion         string     `json:"software_version,omitempty"`
	TokenEndpointAuthMethod string     `json:"token_endpoint_auth_method"`
	GrantTypes              []string   `json:"grant_types"`
	State                   string     `json:"state"`
	CreatedAt               time.Time  `json:"created_at"`
	AuthorizedAt            *time.Time `json:"authorized_at,omitempty"`
}

// OAuthGrant is one browser login (data-model.md).
type OAuthGrant struct {
	GrantID            string     `json:"grant_id"`
	RegistrationID     string     `json:"registration_id"`
	Client             string     `json:"client"`
	RedirectURI        string     `json:"redirect_uri"`
	Resource           string     `json:"resource"`
	Issuer             string     `json:"issuer"`
	RefreshHash        string     `json:"refresh_hash"`
	RefreshPrefix      string     `json:"refresh_prefix"`
	AccessHash         string     `json:"access_hash"`
	AccessExpiresAt    time.Time  `json:"access_expires_at"`
	RefreshExpiresAt   time.Time  `json:"refresh_expires_at"`
	ApprovedAt         time.Time  `json:"approved_at"`
	ApprovedBy         OAuthActor `json:"approved_by"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	ObservedClientName string     `json:"observed_client_name,omitempty"`
	ObservedAt         *time.Time `json:"observed_at,omitempty"`
	DisplayPrefix      string     `json:"display_prefix"`
	Revoked            bool       `json:"revoked"`
	RevokedAt          *time.Time `json:"revoked_at,omitempty"`
	RevokeReason       string     `json:"revoke_reason,omitempty"`
	UserID             string     `json:"user_id,omitempty"`
}

// OAuthClientBinding is the binding copy kept for a client with browser logins.
type OAuthClientBinding struct {
	ProfilePin  string    `json:"profile_pin"`
	ProfileMode string    `json:"profile_mode"`
	DisplayName string    `json:"display_name,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Refresh rotation outcomes.
type RefreshOutcome int

const (
	// RefreshInvalid: unknown, revoked or lapsed grant. Nothing was written.
	RefreshInvalid RefreshOutcome = iota
	// RefreshRotated: the presented secret was current; the grant rotated.
	RefreshRotated
	// RefreshReplay: the presented secret was an old one; the grant is revoked.
	RefreshReplay
)

// --- small bucket helpers (all nil-tolerant on the read side) ---

func oauthGetJSON(tx *bbolt.Tx, bucket, key string, out any) (bool, error) {
	b := tx.Bucket([]byte(bucket))
	if b == nil {
		return false, nil
	}
	v := b.Get([]byte(key))
	if v == nil {
		return false, nil
	}
	if err := json.Unmarshal(v, out); err != nil {
		return false, fmt.Errorf("failed to unmarshal %s/%s: %w", bucket, key, err)
	}
	return true, nil
}

func oauthPutJSON(tx *bbolt.Tx, bucket, key string, v any) error {
	b, err := tx.CreateBucketIfNotExists([]byte(bucket))
	if err != nil {
		return fmt.Errorf("failed to create %s bucket: %w", bucket, err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal %s/%s: %w", bucket, key, err)
	}
	return b.Put([]byte(key), data)
}

func oauthForEach[T any](tx *bbolt.Tx, bucket string, fn func(key string, v T) error) error {
	b := tx.Bucket([]byte(bucket))
	if b == nil {
		return nil
	}
	return b.ForEach(func(k, raw []byte) error {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil // skip unparseable rows, like the agent token scans
		}
		return fn(string(k), v)
	})
}

func getOAuthBindingTx(tx *bbolt.Tx, clientID string) (*OAuthClientBinding, error) {
	var b OAuthClientBinding
	ok, err := oauthGetJSON(tx, OAuthClientBindingsBucket, clientID, &b)
	if err != nil || !ok {
		return nil, err
	}
	return &b, nil
}

func getOAuthGrantTx(tx *bbolt.Tx, id string) (*OAuthGrant, error) {
	var g OAuthGrant
	ok, err := oauthGetJSON(tx, OAuthGrantsBucket, id, &g)
	if err != nil || !ok {
		return nil, err
	}
	return &g, nil
}

func putOAuthGrantTx(tx *bbolt.Tx, g *OAuthGrant) error {
	return oauthPutJSON(tx, OAuthGrantsBucket, g.GrantID, g)
}

func deleteAccessIndexTx(tx *bbolt.Tx, accessHash string) error {
	if accessHash == "" {
		return nil
	}
	b := tx.Bucket([]byte(OAuthGrantAccessIndexBucket))
	if b == nil {
		return nil
	}
	return b.Delete([]byte(accessHash))
}

func putAccessIndexTx(tx *bbolt.Tx, accessHash, grantID string) error {
	if accessHash == "" {
		return nil
	}
	b, err := tx.CreateBucketIfNotExists([]byte(OAuthGrantAccessIndexBucket))
	if err != nil {
		return fmt.Errorf("failed to create %s bucket: %w", OAuthGrantAccessIndexBucket, err)
	}
	return b.Put([]byte(accessHash), []byte(grantID))
}

// revokeGrantTx marks g revoked (no-op when already revoked) and drops its
// access index entry. Reports whether it changed anything.
func revokeGrantTx(tx *bbolt.Tx, g *OAuthGrant, reason string, now time.Time) (bool, error) {
	if g.Revoked {
		return false, nil
	}
	at := now.UTC()
	g.Revoked = true
	g.RevokedAt = &at
	g.RevokeReason = reason
	if err := deleteAccessIndexTx(tx, g.AccessHash); err != nil {
		return false, err
	}
	if err := putOAuthGrantTx(tx, g); err != nil {
		return false, err
	}
	return true, nil
}

// --- registrations ---

// CreateOAuthRegistration stores a new pending registration. At most
// MaxUnapprovedOAuthRegistrations rows may be pending or authorized together;
// when the cap is exceeded the OLDEST such row is dropped in the same
// transaction (approved rows are never evicted).
func (m *Manager) CreateOAuthRegistration(reg OAuthRegistration) error {
	if reg.ClientID == "" {
		return fmt.Errorf("registration needs a client id")
	}
	reg.State = OAuthRegistrationPending
	reg.AuthorizedAt = nil
	if reg.CreatedAt.IsZero() {
		reg.CreatedAt = time.Now()
	}
	reg.CreatedAt = reg.CreatedAt.UTC()

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		if err := oauthPutJSON(tx, OAuthRegistrationsBucket, reg.ClientID, &reg); err != nil {
			return err
		}
		type row struct {
			id string
			at time.Time
		}
		var open []row
		if err := oauthForEach(tx, OAuthRegistrationsBucket, func(key string, r OAuthRegistration) error {
			if r.State != OAuthRegistrationApproved && key != reg.ClientID {
				open = append(open, row{key, r.CreatedAt})
			}
			return nil
		}); err != nil {
			return err
		}
		// The new row counts toward the cap.
		excess := len(open) + 1 - MaxUnapprovedOAuthRegistrations
		if excess <= 0 {
			return nil
		}
		sort.Slice(open, func(i, j int) bool {
			if open[i].at.Equal(open[j].at) {
				return open[i].id < open[j].id
			}
			return open[i].at.Before(open[j].at)
		})
		b := tx.Bucket([]byte(OAuthRegistrationsBucket))
		for _, r := range open[:excess] {
			if err := b.Delete([]byte(r.id)); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetOAuthRegistration returns the registration, or ErrOAuthRegistrationGone.
func (m *Manager) GetOAuthRegistration(id string) (*OAuthRegistration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var reg OAuthRegistration
	var found bool
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		var err error
		found, err = oauthGetJSON(tx, OAuthRegistrationsBucket, id, &reg)
		return err
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrOAuthRegistrationGone
	}
	return &reg, nil
}

// MarkOAuthRegistrationAuthorized moves pending -> authorized and stamps
// authorized_at. An authorized row keeps its first stamp; an approved row is
// left alone.
func (m *Manager) MarkOAuthRegistrationAuthorized(id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		var reg OAuthRegistration
		ok, err := oauthGetJSON(tx, OAuthRegistrationsBucket, id, &reg)
		if err != nil {
			return err
		}
		if !ok {
			return ErrOAuthRegistrationGone
		}
		if reg.State != OAuthRegistrationPending {
			return nil
		}
		t := at.UTC()
		reg.State = OAuthRegistrationAuthorized
		reg.AuthorizedAt = &t
		return oauthPutJSON(tx, OAuthRegistrationsBucket, id, &reg)
	})
}

// SweepOAuthRegistrations deletes pending rows older than 15 minutes (from
// created_at) and authorized rows older than 24 hours (from authorized_at).
// Approved rows are never swept. It returns how many rows it deleted.
func (m *Manager) SweepOAuthRegistrations(now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := 0
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(OAuthRegistrationsBucket))
		if b == nil {
			return nil
		}
		var drop []string
		if err := oauthForEach(tx, OAuthRegistrationsBucket, func(key string, r OAuthRegistration) error {
			switch r.State {
			case OAuthRegistrationPending:
				if now.Sub(r.CreatedAt) > OAuthPendingRegistrationTTL {
					drop = append(drop, key)
				}
			case OAuthRegistrationAuthorized:
				at := r.CreatedAt
				if r.AuthorizedAt != nil {
					at = *r.AuthorizedAt
				}
				if now.Sub(at) > OAuthAuthorizedRegistrationTTL {
					drop = append(drop, key)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		for _, k := range drop {
			if err := b.Delete([]byte(k)); err != nil {
				return err
			}
		}
		deleted = len(drop)
		return nil
	})
	return deleted, err
}

// ListOAuthRegistrations returns every stored registration.
func (m *Manager) ListOAuthRegistrations() ([]OAuthRegistration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []OAuthRegistration
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		return oauthForEach(tx, OAuthRegistrationsBucket, func(_ string, r OAuthRegistration) error {
			out = append(out, r)
			return nil
		})
	})
	return out, err
}

// --- binding revision ---

// clientBindingRevisionTx is the revision of the client's current binding:
// the first 8 bytes of sha256(client|pin|mode), hex. The API-key record wins
// over the browser-login entry; a client with neither is OAuthBindingAbsent.
func clientBindingRevisionTx(tx *bbolt.Tx, clientID string) (string, error) {
	pin, mode, exists, err := currentClientBindingTx(tx, clientID)
	if err != nil {
		return "", err
	}
	if !exists {
		return OAuthBindingAbsent, nil
	}
	return bindingRevision(clientID, pin, mode), nil
}

// currentClientBindingTx reads a client's binding: from the API-key (kind
// client) record when there is one, else from the browser-login entry.
func currentClientBindingTx(tx *bbolt.Tx, clientID string) (pin, mode string, exists bool, err error) {
	_, tok, err := findClientTokenRecordLocked(tx, clientID)
	if err != nil {
		return "", "", false, err
	}
	if tok != nil && tok.Kind == auth.KindClient {
		return tok.ProfilePin, tok.ProfileMode, true, nil
	}
	b, err := getOAuthBindingTx(tx, clientID)
	if err != nil {
		return "", "", false, err
	}
	if b != nil {
		return b.ProfilePin, b.ProfileMode, true, nil
	}
	return "", "", false, nil
}

// ClientBindingRevision returns the binding revision the consent page shows
// and sends back; see clientBindingRevisionTx.
func (m *Manager) ClientBindingRevision(clientID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var rev string
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		var err error
		rev, err = clientBindingRevisionTx(tx, clientID)
		return err
	})
	return rev, err
}

// --- grants ---

// CommitLoginInput is everything the token exchange hands to CommitOAuthLogin.
type CommitLoginInput struct {
	// Grant is the prepared grant; Client and RegistrationID must be set.
	Grant OAuthGrant
	// Client is the Clients hub client id (must equal Grant.Client).
	Client string
	// NewClient: the decision creates a client; the id must be absent.
	NewClient bool
	// Pin, Mode, DisplayName are the binding of a NEW client. An existing
	// API-key client's entry is always copied from its current record.
	Pin, Mode, DisplayName string
	// ExpectedBindingRevision is the revision the consent page displayed.
	ExpectedBindingRevision string
	// ReplaceGrantID, when set, names a login of the same client to revoke
	// with reason "replaced".
	ReplaceGrantID string
	// Now stamps the binding entry and the ever-used marker.
	Now time.Time
}

// CommitOAuthLogin stores a browser login in ONE transaction (research R15):
// it re-checks the registration, the new-client id and the binding revision,
// creates the OAuth-side binding when the client has none, promotes the
// registration to approved, stores the grant, revokes the replaced login and
// writes the ever-used marker. Any failed check leaves the database untouched.
func (m *Manager) CommitOAuthLogin(in CommitLoginInput) (OAuthGrant, error) {
	g := in.Grant
	if in.Client == "" {
		in.Client = g.Client
	}
	if g.Client == "" {
		g.Client = in.Client
	}
	if g.GrantID == "" || g.RegistrationID == "" || !auth.ValidClientID(in.Client) || g.Client != in.Client {
		return OAuthGrant{}, fmt.Errorf("invalid login: grant id, registration id and a matching client id are required")
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	var stored OAuthGrant
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		// (1) the registration must still exist.
		var reg OAuthRegistration
		ok, err := oauthGetJSON(tx, OAuthRegistrationsBucket, g.RegistrationID, &reg)
		if err != nil {
			return err
		}
		if !ok {
			return ErrOAuthRegistrationGone
		}

		// (2) a decision that creates a client needs the id absent everywhere.
		_, anyRecord, err := findClientTokenRecordLocked(tx, in.Client)
		if err != nil {
			return err
		}
		existingBinding, err := getOAuthBindingTx(tx, in.Client)
		if err != nil {
			return err
		}
		if in.NewClient && (anyRecord != nil || existingBinding != nil) {
			return ErrOAuthClientExists
		}

		// (3) the binding the administrator reviewed is still the binding.
		rev, err := clientBindingRevisionTx(tx, in.Client)
		if err != nil {
			return err
		}
		if rev != in.ExpectedBindingRevision {
			return ErrOAuthBindingChanged
		}
		if !in.NewClient && rev == OAuthBindingAbsent {
			// An existing-client decision whose client vanished: there is no
			// binding to copy and none to create from input.
			return ErrOAuthBindingChanged
		}

		// (4) create the OAuth-side binding when the client has none.
		if existingBinding == nil {
			nb := OAuthClientBinding{UpdatedAt: now}
			if in.NewClient {
				if in.Mode != auth.ProfileModeLocked && in.Mode != auth.ProfileModeSwitchable {
					return fmt.Errorf("invalid profile mode %q", in.Mode)
				}
				if in.Mode == auth.ProfileModeLocked && in.Pin == "" {
					return fmt.Errorf("a locked client binding requires a non-empty profile")
				}
				if utf8.RuneCountInString(in.DisplayName) > auth.MaxClientDisplayName {
					return fmt.Errorf("client display name is too long")
				}
				nb.ProfilePin, nb.ProfileMode, nb.DisplayName = in.Pin, in.Mode, in.DisplayName
			} else {
				pin, mode, exists, err := currentClientBindingTx(tx, in.Client)
				if err != nil {
					return err
				}
				if !exists {
					return ErrOAuthBindingChanged
				}
				nb.ProfilePin, nb.ProfileMode = pin, mode
				if anyRecord != nil {
					nb.DisplayName = anyRecord.DisplayName
				}
			}
			if err := oauthPutJSON(tx, OAuthClientBindingsBucket, in.Client, &nb); err != nil {
				return err
			}
		}

		// (5) the registration becomes approved.
		reg.State = OAuthRegistrationApproved
		if err := oauthPutJSON(tx, OAuthRegistrationsBucket, reg.ClientID, &reg); err != nil {
			return err
		}

		// (6) the grant.
		if prev, err := getOAuthGrantTx(tx, g.GrantID); err != nil {
			return err
		} else if prev != nil {
			return fmt.Errorf("grant %s already exists", g.GrantID)
		}
		g.Revoked, g.RevokedAt, g.RevokeReason = false, nil, ""
		if err := putOAuthGrantTx(tx, &g); err != nil {
			return err
		}
		if err := putAccessIndexTx(tx, g.AccessHash, g.GrantID); err != nil {
			return err
		}

		// (7) the replaced login, only when it belongs to the same client.
		if in.ReplaceGrantID != "" && in.ReplaceGrantID != g.GrantID {
			old, err := getOAuthGrantTx(tx, in.ReplaceGrantID)
			if err != nil {
				return err
			}
			if old != nil && old.Client == in.Client {
				if _, err := revokeGrantTx(tx, old, OAuthRevokeReplaced, now); err != nil {
					return err
				}
			}
		}

		// (8) the ever-used marker, kept once.
		meta, err := tx.CreateBucketIfNotExists([]byte(MetaBucket))
		if err != nil {
			return err
		}
		if meta.Get([]byte(OAuthLoginEverUsedKey)) == nil {
			if err := meta.Put([]byte(OAuthLoginEverUsedKey), []byte(now.Format(time.RFC3339))); err != nil {
				return err
			}
		}
		stored = g
		return nil
	})
	if err != nil {
		return OAuthGrant{}, err
	}
	return stored, nil
}

// GetOAuthGrant returns the grant, or ErrOAuthGrantNotFound.
func (m *Manager) GetOAuthGrant(id string) (*OAuthGrant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var g *OAuthGrant
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		var err error
		g, err = getOAuthGrantTx(tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, ErrOAuthGrantNotFound
	}
	return g, nil
}

// ListOAuthGrants returns every stored grant.
func (m *Manager) ListOAuthGrants() ([]OAuthGrant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []OAuthGrant
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		return oauthForEach(tx, OAuthGrantsBucket, func(_ string, g OAuthGrant) error {
			out = append(out, g)
			return nil
		})
	})
	return out, err
}

// ListOAuthGrantsForClient returns the grants of one client, newest approval first.
func (m *Manager) ListOAuthGrantsForClient(clientID string) ([]OAuthGrant, error) {
	all, err := m.ListOAuthGrants()
	if err != nil {
		return nil, err
	}
	var out []OAuthGrant
	for _, g := range all {
		if g.Client == clientID {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ApprovedAt.After(out[j].ApprovedAt) })
	return out, nil
}

// GetOAuthGrantByAccessHash resolves an access-token hash to its grant through
// the index, re-checking that the grant still carries that hash. A missing or
// stale entry is ErrOAuthGrantNotFound. Revocation and expiry are the
// caller's decision (it needs the grant to report them).
func (m *Manager) GetOAuthGrantByAccessHash(hash string) (*OAuthGrant, error) {
	if hash == "" {
		return nil, ErrOAuthGrantNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var g *OAuthGrant
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		idx := tx.Bucket([]byte(OAuthGrantAccessIndexBucket))
		if idx == nil {
			return nil
		}
		id := idx.Get([]byte(hash))
		if id == nil {
			return nil
		}
		cand, err := getOAuthGrantTx(tx, string(id))
		if err != nil || cand == nil {
			return err
		}
		if subtle.ConstantTimeCompare([]byte(cand.AccessHash), []byte(hash)) == 1 {
			g = cand
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, ErrOAuthGrantNotFound
	}
	return g, nil
}

// RotateOAuthRefresh is the refresh-token exchange, in one transaction. The
// caller has already verified the token's MAC; storage sees hashes only.
//
//   - unknown, revoked or lapsed grant: RefreshInvalid, nothing written, the
//     lifetime is not extended;
//   - presented hash equals the current one (constant time): rotate;
//   - anything else: the grant is revoked with reason "replay".
func (m *Manager) RotateOAuthRefresh(grantID, presentedSecretHash, newRefreshHash, newRefreshPrefix, newAccessHash string, now time.Time, accessTTL, refreshTTL time.Duration) (RefreshOutcome, OAuthGrant, error) {
	now = now.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()

	// Refuse an unknown, revoked or lapsed grant in a read-only transaction:
	// a refused refresh must not commit anything (and never extends a lifetime).
	live := false
	if err := m.db.db.View(func(tx *bbolt.Tx) error {
		g, err := getOAuthGrantTx(tx, grantID)
		live = err == nil && g != nil && !g.Revoked && g.RefreshExpiresAt.After(now)
		return err
	}); err != nil {
		return RefreshInvalid, OAuthGrant{}, err
	}
	if !live {
		return RefreshInvalid, OAuthGrant{}, nil
	}

	outcome := RefreshInvalid
	var result OAuthGrant
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		g, err := getOAuthGrantTx(tx, grantID)
		if err != nil {
			return err
		}
		if g == nil || g.Revoked || !g.RefreshExpiresAt.After(now) {
			return nil
		}
		if presentedSecretHash != "" && subtle.ConstantTimeCompare([]byte(presentedSecretHash), []byte(g.RefreshHash)) == 1 {
			if err := deleteAccessIndexTx(tx, g.AccessHash); err != nil {
				return err
			}
			g.RefreshHash = newRefreshHash
			g.RefreshPrefix = newRefreshPrefix
			g.AccessHash = newAccessHash
			g.AccessExpiresAt = now.Add(accessTTL)
			g.RefreshExpiresAt = now.Add(refreshTTL)
			if err := putOAuthGrantTx(tx, g); err != nil {
				return err
			}
			if err := putAccessIndexTx(tx, g.AccessHash, g.GrantID); err != nil {
				return err
			}
			outcome, result = RefreshRotated, *g
			return nil
		}
		if _, err := revokeGrantTx(tx, g, OAuthRevokeReplay, now); err != nil {
			return err
		}
		outcome, result = RefreshReplay, *g
		return nil
	})
	if err != nil {
		return RefreshInvalid, OAuthGrant{}, err
	}
	return outcome, result, nil
}

// RevokeOAuthGrant revokes one grant. It reports whether the grant changed
// (an already revoked grant is not an error and keeps its first reason).
func (m *Manager) RevokeOAuthGrant(id, reason string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := false
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		g, err := getOAuthGrantTx(tx, id)
		if err != nil {
			return err
		}
		if g == nil {
			return ErrOAuthGrantNotFound
		}
		changed, err = revokeGrantTx(tx, g, reason, now)
		return err
	})
	return changed, err
}

// TouchOAuthGrant stamps last_used_at and, when non-empty, the observed client
// name. Best effort, off the request path, like UpdateAgentTokenLastUsedByHash.
func (m *Manager) TouchOAuthGrant(id string, lastUsedAt time.Time, observedName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db.db.Update(func(tx *bbolt.Tx) error {
		g, err := getOAuthGrantTx(tx, id)
		if err != nil {
			return err
		}
		if g == nil {
			return ErrOAuthGrantNotFound
		}
		t := lastUsedAt.UTC()
		g.LastUsedAt = &t
		if observedName != "" {
			g.ObservedClientName = observedName
			g.ObservedAt = &t
		}
		return putOAuthGrantTx(tx, g)
	})
}

// SweepOAuthGrants deletes grants that were revoked, or whose refresh token
// lapsed, more than 30 days ago. A registration whose last grant record goes
// is deleted with it, and so is the binding entry of a client whose last grant
// record goes. It returns the number of grants deleted.
func (m *Manager) SweepOAuthGrants(now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := 0
	err := m.db.db.Update(func(tx *bbolt.Tx) error {
		gb := tx.Bucket([]byte(OAuthGrantsBucket))
		if gb == nil {
			return nil
		}
		var dead []OAuthGrant
		survivingReg := map[string]bool{}
		survivingClient := map[string]bool{}
		if err := oauthForEach(tx, OAuthGrantsBucket, func(_ string, g OAuthGrant) error {
			var since time.Time
			if g.Revoked {
				if g.RevokedAt != nil {
					since = *g.RevokedAt
				} else {
					since = g.ApprovedAt
				}
			} else if !g.RefreshExpiresAt.After(now) {
				since = g.RefreshExpiresAt
			}
			if !since.IsZero() && now.Sub(since) > OAuthDeadGrantRetention {
				dead = append(dead, g)
				return nil
			}
			survivingReg[g.RegistrationID] = true
			survivingClient[g.Client] = true
			return nil
		}); err != nil {
			return err
		}
		for _, g := range dead {
			if err := gb.Delete([]byte(g.GrantID)); err != nil {
				return err
			}
			if err := deleteAccessIndexTx(tx, g.AccessHash); err != nil {
				return err
			}
			if !survivingReg[g.RegistrationID] {
				if rb := tx.Bucket([]byte(OAuthRegistrationsBucket)); rb != nil {
					if err := rb.Delete([]byte(g.RegistrationID)); err != nil {
						return err
					}
				}
			}
			if !survivingClient[g.Client] {
				if bb := tx.Bucket([]byte(OAuthClientBindingsBucket)); bb != nil {
					if err := bb.Delete([]byte(g.Client)); err != nil {
						return err
					}
				}
			}
		}
		deleted = len(dead)
		return nil
	})
	return deleted, err
}

// OAuthLoginEverUsed reports whether a browser login was ever stored.
func (m *Manager) OAuthLoginEverUsed() (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	used := false
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		meta := tx.Bucket([]byte(MetaBucket))
		used = meta != nil && meta.Get([]byte(OAuthLoginEverUsedKey)) != nil
		return nil
	})
	return used, err
}

// GetOAuthClientBinding returns a client's browser-login binding entry, or nil
// when it has none.
func (m *Manager) GetOAuthClientBinding(clientID string) (*OAuthClientBinding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var b *OAuthClientBinding
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		var err error
		b, err = getOAuthBindingTx(tx, clientID)
		return err
	})
	return b, err
}

// ListOAuthClientBindings returns every browser-login binding entry by client id.
func (m *Manager) ListOAuthClientBindings() (map[string]OAuthClientBinding, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]OAuthClientBinding{}
	err := m.db.db.View(func(tx *bbolt.Tx) error {
		return oauthForEach(tx, OAuthClientBindingsBucket, func(k string, b OAuthClientBinding) error {
			out[k] = b
			return nil
		})
	})
	return out, err
}
