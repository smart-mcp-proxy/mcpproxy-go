package audit

// line.go builds the three Spec 107 audit line kinds (authz, tool_call,
// auth_event) against contracts/audit-line.schema.json. Every constructor
// takes a typed input struct with no field through which raw arguments, a
// response fragment or error text could arrive (FR-015's structural
// guarantee); the redaction this file DOES perform is the per-field masking
// of caller/operator-controlled strings (contracts/audit-line-events.md
// "Redaction (FR-015)").

import (
	"encoding/json"
	"fmt"
	"time"
)

// tsLayout is the fixed nine-fractional-digit UTC layout the schema
// requires (RFC3339Nano trims trailing zeros, which this must not do).
const tsLayout = "2006-01-02T15:04:05.000000000Z"

// Line is a single, already-validated audit line.
type Line struct {
	fields map[string]interface{}
}

// JSON serialises the line. encoding/json sorts map[string]interface{} keys,
// so repeated calls on the same Line are byte-stable.
func (l Line) JSON() ([]byte, error) {
	return json.Marshal(l.fields)
}

func newBase(event string, ts time.Time, requestID, origin, source string, caller Caller) map[string]interface{} {
	f := map[string]interface{}{
		"schema_version": 1,
		"ts":             ts.UTC().Format(tsLayout),
		"event":          event,
		"request_id":     requestID,
		"origin":         origin,
		"source":         source,
		"caller":         buildCaller(caller),
	}
	return f
}

func buildCaller(c Caller) map[string]interface{} {
	m := map[string]interface{}{"kind": c.Kind}
	if c.UserID != "" {
		m["user_id"] = c.UserID
	}
	if c.UserEmail != "" {
		m["user_email"] = c.UserEmail
	}
	if c.EmailHash != "" {
		m["email_hash"] = c.EmailHash
	}
	if c.Role != "" {
		m["role"] = c.Role
	}
	if c.Provider != "" {
		m["provider"] = c.Provider
	}
	if c.TokenName != "" {
		m["token_name"] = maskCredential(c.TokenName)
	}
	if c.TokenPrefix != "" {
		m["token_prefix"] = c.TokenPrefix
	}
	if c.ProfilePin != "" {
		m["profile_pin"] = maskCredential(c.ProfilePin)
	}
	return m
}

func setClient(f map[string]interface{}, name, version, ip string) {
	c := map[string]interface{}{}
	if name != "" {
		c["name"] = maskCredential(name)
	}
	if version != "" {
		c["version"] = version
	}
	if ip != "" {
		c["ip"] = ip
	}
	if len(c) > 0 {
		f["client"] = c
	}
}

func setOptString(f map[string]interface{}, key, val string) {
	if val != "" {
		f[key] = val
	}
}

// ---------------------------------------------------------------------------
// authz
// ---------------------------------------------------------------------------

// AuthzInput builds one `authz` line: exactly one per pre-dispatch decision.
type AuthzInput struct {
	Ts        time.Time
	Attempt   Attempt
	Caller    Caller
	Decision  string // allow|deny
	Reason    string // "none" iff allow; else a pre-dispatch gate reason
	Disclosed *bool  // required iff Decision == deny
}

// NewAuthz builds and structurally validates an `authz` line.
func NewAuthz(in AuthzInput) (Line, error) {
	switch in.Decision {
	case "allow":
		if in.Reason != "none" {
			return Line{}, fmt.Errorf("audit.NewAuthz: decision:allow requires reason:none, got %q", in.Reason)
		}
	case "deny":
		if in.Reason == "none" || in.Reason == "" {
			return Line{}, fmt.Errorf("audit.NewAuthz: decision:deny requires a non-none reason")
		}
		if in.Disclosed == nil {
			return Line{}, fmt.Errorf("audit.NewAuthz: decision:deny requires Disclosed")
		}
	default:
		return Line{}, fmt.Errorf("audit.NewAuthz: invalid decision %q", in.Decision)
	}

	a := in.Attempt
	f := newBase("authz", in.Ts, a.RequestID, a.Origin, a.Source, in.Caller)
	f["surface"] = a.Surface
	f["server"] = maskCredential(a.Server)
	f["tool"] = maskCredential(a.Tool)
	f["operation"] = a.Operation
	f["decision"] = in.Decision
	f["reason"] = in.Reason
	f["args_sha256"] = a.ArgsSHA256
	f["args_bytes"] = a.ArgsBytes
	if in.Disclosed != nil {
		f["disclosed"] = *in.Disclosed
	}
	setOptString(f, "transport_request_id", a.TransportRequestID)
	setOptString(f, "parent_id", a.ParentID)
	setOptString(f, "session_id", a.SessionID)
	setOptString(f, "work_session_id", a.WorkSessionID)
	setOptString(f, "profile", maskCredential(a.Profile))
	setClient(f, a.ClientName, a.ClientVersion, a.ClientIP)

	return Line{fields: f}, nil
}

// ---------------------------------------------------------------------------
// tool_call
// ---------------------------------------------------------------------------

// ToolCallInput builds one `tool_call` line: exactly one per `authz allow`,
// written at completion.
type ToolCallInput struct {
	Ts            time.Time
	Attempt       Attempt
	Caller        Caller
	Outcome       string // success|error|blocked|rejected
	Reason        string // required iff blocked/rejected; forbidden otherwise
	ErrorClass    string // required iff error; forbidden otherwise
	DurationMs    int
	RequestBytes  *int
	ResponseBytes *int
}

// NewToolCall builds and structurally validates a `tool_call` line.
func NewToolCall(in ToolCallInput) (Line, error) {
	switch in.Outcome {
	case "success":
		if in.Reason != "" {
			return Line{}, fmt.Errorf("audit.NewToolCall: reason is forbidden on outcome:success")
		}
		if in.ErrorClass != "" {
			return Line{}, fmt.Errorf("audit.NewToolCall: error_class is forbidden on outcome:success")
		}
	case "error":
		if in.Reason != "" {
			return Line{}, fmt.Errorf("audit.NewToolCall: reason is forbidden on outcome:error")
		}
		if in.ErrorClass == "" {
			return Line{}, fmt.Errorf("audit.NewToolCall: outcome:error requires error_class")
		}
	case "blocked", "rejected":
		if in.Reason == "" {
			return Line{}, fmt.Errorf("audit.NewToolCall: outcome:%s requires reason", in.Outcome)
		}
		if in.ErrorClass != "" {
			return Line{}, fmt.Errorf("audit.NewToolCall: error_class is forbidden on outcome:%s", in.Outcome)
		}
	default:
		return Line{}, fmt.Errorf("audit.NewToolCall: invalid outcome %q", in.Outcome)
	}

	a := in.Attempt
	f := newBase("tool_call", in.Ts, a.RequestID, a.Origin, a.Source, in.Caller)
	f["surface"] = a.Surface
	f["server"] = maskCredential(a.Server)
	f["tool"] = maskCredential(a.Tool)
	f["operation"] = a.Operation
	f["outcome"] = in.Outcome
	f["duration_ms"] = in.DurationMs
	f["args_sha256"] = a.ArgsSHA256
	f["args_bytes"] = a.ArgsBytes
	setOptString(f, "reason", in.Reason)
	setOptString(f, "error_class", in.ErrorClass)
	setOptString(f, "transport_request_id", a.TransportRequestID)
	setOptString(f, "parent_id", a.ParentID)
	setOptString(f, "session_id", a.SessionID)
	setOptString(f, "work_session_id", a.WorkSessionID)
	if in.RequestBytes != nil {
		f["request_bytes"] = *in.RequestBytes
	}
	if in.ResponseBytes != nil {
		f["response_bytes"] = *in.ResponseBytes
	}
	setClient(f, a.ClientName, a.ClientVersion, a.ClientIP)

	return Line{fields: f}, nil
}

// ---------------------------------------------------------------------------
// auth_event
// ---------------------------------------------------------------------------

// AuthEventInput builds one `auth_event` line: one per terminal login
// attempt the proxy observes, one per logout.
type AuthEventInput struct {
	Ts        time.Time
	RequestID string
	Origin    string
	Source    string
	Surface   string // login|logout
	Reason    string
	Caller    Caller
	ClientIP  string
	Flags     []string
}

// NewAuthEvent builds and structurally validates an `auth_event` line.
func NewAuthEvent(in AuthEventInput) (Line, error) {
	if in.Surface == "logout" && in.Reason != "logout" {
		return Line{}, fmt.Errorf("audit.NewAuthEvent: surface:logout requires reason:logout, got %q", in.Reason)
	}
	if in.Reason == "logout" && in.Surface != "logout" {
		return Line{}, fmt.Errorf("audit.NewAuthEvent: reason:logout requires surface:logout, got %q", in.Surface)
	}

	f := newBase("auth_event", in.Ts, in.RequestID, in.Origin, in.Source, in.Caller)
	f["surface"] = in.Surface
	f["reason"] = in.Reason
	if len(in.Flags) > 0 {
		flags := make([]interface{}, len(in.Flags))
		for i, fl := range in.Flags {
			flags[i] = fl
		}
		f["flags"] = flags
	}
	setClient(f, "", "", in.ClientIP)

	return Line{fields: f}, nil
}
