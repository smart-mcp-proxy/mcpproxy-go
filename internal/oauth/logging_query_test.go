package oauth

import (
	"net/url"
	"strings"
	"testing"
)

// SEC-01. LogSafeQueryString renders a BARE query string (an http.Request's
// RawQuery) for a log sink. It must apply the SAME rule LogSafeURL applies to a
// whole URL - feeding the bare string to url.Parse reads it as a path, so the
// name rule would never fire.
func TestLogSafeQueryString(t *testing.T) {
	const secret = "SUPERSECRETKEY0123456789abcdef01"

	cases := []struct {
		name        string
		rawQuery    string
		mustSurvive []string
		wantExact   string
	}{
		{name: "empty", rawQuery: "", wantExact: ""},
		{
			name:        "apikey masked, siblings verbatim",
			rawQuery:    "apikey=" + secret + "&foo=bar&page=2",
			mustSurvive: []string{"apikey=", "foo=bar", "page=2"},
		},
		{name: "api_key", rawQuery: "api_key=" + secret, mustSurvive: []string{"api_key="}},
		{name: "token", rawQuery: "token=" + secret, mustSurvive: []string{"token="}},
		{name: "key", rawQuery: "key=" + secret, mustSurvive: []string{"key="}},
		{name: "secret", rawQuery: "secret=" + secret, mustSurvive: []string{"secret="}},
		{
			name:        "unparseable escape does not defeat the rule",
			rawQuery:    "%zz&apikey=" + secret,
			mustSurvive: []string{"apikey="},
		},
		{
			name:      "no sensitive parameter is returned byte-for-byte",
			rawQuery:  "foo=bar&page=2&filter=a%20b",
			wantExact: "foo=bar&page=2&filter=a%20b",
		},
		{
			name:      "reference values are labels, not secrets",
			rawQuery:  "apikey=${env:MCPPROXY_API_KEY}&foo=bar",
			wantExact: "apikey=${env:MCPPROXY_API_KEY}&foo=bar",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LogSafeQueryString(tc.rawQuery)
			if strings.Contains(got, secret) {
				t.Fatalf("credential survived redaction: %q", got)
			}
			if tc.wantExact != "" || tc.rawQuery == "" {
				if got != tc.wantExact {
					t.Fatalf("want %q, got %q", tc.wantExact, got)
				}
			}
			for _, want := range tc.mustSurvive {
				if !strings.Contains(got, want) {
					t.Fatalf("expected %q to survive in %q", want, got)
				}
			}
			if strings.HasPrefix(got, "?") {
				t.Fatalf("the wrapping %q must not leak into the field: %q", "?", got)
			}
		})
	}
}

// SEC-01, codex round 1 finding 1. A Referer is entirely client-controlled, so
// it does not have to be a parseable URL. LogSafeURL masks nothing in a URL
// url.Parse rejects - RedactURLQueryParamsWith falls back to the regex
// RedactURL, which has no rule for `apikey`, and the generated key is 64 hex
// characters, which the value-shaped detector does not recognise on its own. A
// single bad escape anywhere in the path was enough to put the root credential
// back into http.log verbatim.
func TestLogSafeRequestURL_MalformedURLStillMasksTheCredential(t *testing.T) {
	// The real shape: what config.generateAPIKey() produces.
	const adminKey = "6930184070f362383e7cddbd1184b3b8ed66f84717eedd8906ab8743dfa746cd"

	cases := []struct {
		name   string
		rawURL string
	}{
		{name: "bad escape in path", rawURL: "http://127.0.0.1:8080/%zz?apikey=" + adminKey},
		{name: "truncated escape", rawURL: "http://127.0.0.1:8080/ui%/?apikey=" + adminKey},
		{name: "missing scheme", rawURL: ":::/bad?apikey=" + adminKey},
		{name: "control byte in path", rawURL: "http://127.0.0.1:8080/\x7f?apikey=" + adminKey},
		{name: "well-formed still works", rawURL: "http://127.0.0.1:8080/ui/?apikey=" + adminKey},
		{name: "query only", rawURL: "?apikey=" + adminKey},
		// codex round 2 finding 2: an HTAB is legal inside an HTTP header
		// value and makes url.Parse fail, so every parse-dependent path fell
		// back to a regex with no `apikey` rule.
		{name: "HTAB in a sibling parameter", rawURL: "http://127.0.0.1:8080/ui/?note=\t&apikey=" + adminKey},
		{name: "HTAB and a bad escape", rawURL: "http://127.0.0.1:8080/%zz?note=\t&apikey=" + adminKey},
		// codex round 2 finding 1: the fragment never saw the name rule.
		{name: "credential in the fragment", rawURL: "http://127.0.0.1:8080/ui/#?apikey=" + adminKey},
		{name: "hash routing", rawURL: "http://127.0.0.1:8080/ui/#/servers?apikey=" + adminKey},
		{name: "fragment with no question mark", rawURL: "http://127.0.0.1:8080/ui/#apikey=" + adminKey},
		{name: "query and fragment both", rawURL: "http://127.0.0.1:8080/ui/?apikey=" + adminKey + "#token=" + adminKey},
		// codex round 4: the part of a fragment BEFORE its own '?' bypassed
		// the query name rule, and the free-form scrubber never
		// percent-decodes a parameter name.
		{name: "credential before the fragment's own query", rawURL: "http://127.0.0.1:8080/ui/#apikey=" + adminKey + "?route=x"},
		{name: "percent-encoded parameter name in the fragment", rawURL: "http://127.0.0.1:8080/ui/#api%6bey=" + adminKey + "?route=x"},
		{name: "percent-encoded parameter name in the query", rawURL: "http://127.0.0.1:8080/ui/?api%6bey=" + adminKey},
		// Same class, in a PATH segment of a URL that parses perfectly well -
		// which is why this renderer decomposes by hand instead of trusting
		// LogSafeURL when url.Parse happens to succeed.
		{name: "credential in the path", rawURL: "http://127.0.0.1:8080/ui/apikey=" + adminKey},
		{name: "percent-encoded parameter name in the path", rawURL: "http://127.0.0.1:8080/ui/api%6bey=" + adminKey},
		{name: "userinfo of a well-formed URL", rawURL: "http://user:" + adminKey + "@127.0.0.1:8080/ui/"},
		// codex round 5: the same, behind a path prefix - the name has to be
		// judged PER SEGMENT or `/servers/api%6bey` normalises to nothing.
		{name: "encoded name behind a path prefix", rawURL: "http://127.0.0.1:8080/ui/#/servers/api%6bey=" + adminKey + "?route=x"},
		{name: "encoded name behind a mangled path prefix", rawURL: "http://127.0.0.1:8080/ui/%zz/api%6bey=" + adminKey},
		{name: "encoded separator in the name", rawURL: "http://127.0.0.1:8080/a/b/c/API%2DKEY=" + adminKey},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LogSafeRequestURL(tc.rawURL)
			if strings.Contains(got, adminKey) {
				t.Fatalf("admin API key survived into the log field: %q", got)
			}
		})
	}
}

// The base half of an unparseable URL keeps the regex fallback's
// `user:pass@host` rule: a basic-auth password has no value shape a detector
// can recognise, so dropping RedactURL there would publish it.
func TestLogSafeRequestURL_MalformedURLStillMasksUserinfo(t *testing.T) {
	const pw = "hunter2hunter2hunter2"

	for _, rawURL := range []string{
		"https://admin:" + pw + "@127.0.0.1:8080/%zz?apikey=deadbeefdeadbeef",
		// codex round 2 finding 2: the first '?' falls BETWEEN the password
		// and its '@', so splitting before scrubbing left the password in a
		// substring the userinfo rule could no longer match.
		"https://admin:" + pw + "?x@127.0.0.1:8080/%zz",
		"https://admin:" + pw + "?x@127.0.0.1:8080/%zz?apikey=deadbeefdeadbeef",
		// codex round 3 finding 3: the same cut, made by the OTHER delimiter.
		"https://admin:" + pw + "#x@127.0.0.1:8080/%zz",
		"https://admin:" + pw + "#x@127.0.0.1:8080/%zz?apikey=deadbeefdeadbeef",
	} {
		if got := LogSafeRequestURL(rawURL); strings.Contains(got, pw) {
			t.Fatalf("basic-auth password survived into the log field: %q", got)
		}
	}
}

// The non-credential parts of a referer are the reason the field exists; a URL
// with nothing sensitive in it must come back byte-for-byte.
//
// codex round 6 finding 3: this table used to contain no path with an '=', no
// percent-encoded segment and no config reference, so it did not notice that
// ScrubUpstreamText's unanchored `<name>=<value>` regex was rewriting
// `/monkey=banana` to `/monkey=***REDACTED***`.
func TestLogSafeRequestURL_PreservesBenignURLs(t *testing.T) {
	for _, rawURL := range []string{
		"http://127.0.0.1:8080/ui/",
		"http://127.0.0.1:8080/ui/?page=2&filter=a%20b",
		"http://127.0.0.1:8080/ui/#/servers/everything",
		"http://127.0.0.1:8080/ui/?page=2#/servers/everything",
		// A path segment that merely CONTAINS a marker word.
		"http://127.0.0.1:8080/monkey=banana",
		"http://127.0.0.1:8080/api/v1/tokens/list",
		"http://127.0.0.1:8080/donkey=hotay/passwords=plural",
		// Percent-encoded but benign.
		"http://127.0.0.1:8080/a%20b/c%2Fd?page=2",
		// References are labels, not secrets, in every position.
		"http://127.0.0.1:8080/ui/?url=${env:MCPPROXY_URL}&page=2",
		"http://127.0.0.1:8080/ui/?apikey=${keyring:mcpproxy}",
	} {
		if got := LogSafeRequestURL(rawURL); got != rawURL {
			t.Fatalf("benign referer was rewritten:\n want %q\n  got %q", rawURL, got)
		}
	}
}

// The same guarantee for the `path` field, which is the most-read field in
// http.log and must stay readable.
func TestLogSafeRequestPath(t *testing.T) {
	const adminKey = "6930184070f362383e7cddbd1184b3b8ed66f84717eedd8906ab8743dfa746cd"

	for _, path := range []string{
		"/",
		"/api/v1/servers",
		"/api/v1/servers/everything/tools",
		"/ui/",
		"/monkey=banana",
		"/api/v1/tokens",
		"/events",
	} {
		if got := LogSafeRequestPath(path); got != path {
			t.Fatalf("benign path was rewritten:\n want %q\n  got %q", path, got)
		}
	}

	for _, path := range []string{
		"/ui/apikey=" + adminKey,
		"/ui/api%6bey=" + adminKey,
		"/a/b/token=" + adminKey,
	} {
		if got := LogSafeRequestPath(path); strings.Contains(got, adminKey) {
			t.Fatalf("credential survived in the path field: %q", got)
		}
	}
}

// codex round 7: dropping ScrubUpstreamText from the component rule must not
// drop the two of its rules that no name rule and no value detector can
// replace. r.URL.Path arrives percent-DECODED, so a path really can carry a
// `Bearer <token>` with a space in it, and an embedded URL really can carry
// `user:pass@`.
func TestLogSafeRequestPath_KeepsTheBearerAndUserinfoRules(t *testing.T) {
	const pw = "hunter2"

	for _, path := range []string{
		"/api/v1/servers/Bearer " + pw,
		"/proxy/https://alice:" + pw + "@host/mcp",
	} {
		got := LogSafeRequestPath(path)
		if strings.Contains(got, pw) {
			t.Fatalf("credential survived in the path field: %q", got)
		}
	}
}

func TestLogSafeRequestURL_EmptyAndQueryless(t *testing.T) {
	if got := LogSafeRequestURL(""); got != "" {
		t.Fatalf("empty referer must stay empty, got %q", got)
	}
	// Malformed, query-less and credential-free: the path survives (that is
	// the diagnostic) and nothing panics.
	if got := LogSafeRequestURL("http://127.0.0.1:8080/%zz"); !strings.Contains(got, "127.0.0.1:8080") {
		t.Fatalf("the host must survive a malformed referer, got %q", got)
	}
}

// The boundary of the NAME rule, pinned so it is a decision and not a surprise.
//
// The rule needs a name it can decode and normalise, sitting alone in front of
// an '='. Three shapes have no such name, so only the value-shaped detector
// sees them - and a 64-character hex admin key has no vendor shape for it:
//
//   - a component with no '=' at all (`?<KEY>`);
//   - a component whose '=' is itself percent-encoded (`api%6bey%3d<KEY>`);
//   - a component whose path delimiter is percent-encoded, which makes the
//     name something other than a parameter name (`/ui%2Fapi%6bey=<KEY>`).
//
// Left that way deliberately (codex round 6 finding 1, declined). mcpproxy only
// ever reads the credential from a NAMED `apikey` query parameter - see
// httpapi.Server's auth resolution - and nothing in this codebase emits any of
// these shapes, so none of them is a channel the proxy's OWN key travels on;
// reaching them means a client deliberately encoding its own credential into a
// path. The real gap underneath is that the value detector does not recognise a
// bare hex string, which is repo-wide and not something to change here on a
// guess.
func TestLogSafeQueryString_DocumentedNameRuleBoundary(t *testing.T) {
	const adminKey = "6930184070f362383e7cddbd1184b3b8ed66f84717eedd8906ab8743dfa746cd"

	for _, in := range []string{
		adminKey,
		"api%6bey%3d" + adminKey,
	} {
		if got := LogSafeQueryString(in); got != in {
			t.Fatalf("documented boundary changed - revisit the comment above:\n in %q\nout %q", in, got)
		}
	}
	const encodedDelimiter = "http://host/ui%2Fapi%6bey=" + adminKey
	if got := LogSafeRequestURL(encodedDelimiter); got != encodedDelimiter {
		t.Fatalf("documented boundary changed - revisit the comment above: %q", got)
	}
}

// A literal '#' in RawQuery is an ordinary value byte to url.ParseQuery (Go does
// not split a fragment off a request target), and the parameters after it are
// still parameters. Pin both: the credential after the '#' is masked, and the
// benign parameter's decoded value is unchanged.
func TestLogSafeQueryString_HashIsAValueByteNotAFragment(t *testing.T) {
	const secret = "SUPERSECRETKEY0123456789abcdef01"

	got := LogSafeQueryString("note=a#b&apikey=" + secret)
	if strings.Contains(got, secret) {
		t.Fatalf("a credential after a literal '#' escaped the name rule: %q", got)
	}
	values, err := url.ParseQuery(got)
	if err != nil {
		t.Fatalf("output must still be a parseable query string: %v", err)
	}
	if values.Get("note") != "a#b" {
		t.Fatalf("the benign parameter's decoded value must be unchanged, got %q", values.Get("note"))
	}
}

// The value-shaped detector has to run too: a credential under a parameter name
// no matcher enumerates is exactly what the name rule cannot see. This is the
// property LogSafeURL was introduced for (issue #1158) and the reason this
// helper delegates to it instead of growing a second rule.
func TestLogSafeQueryString_RunsValueShapedDetector(t *testing.T) {
	const ghp = "ghp_1234567890abcdefghijABCDEFGHIJ123456"
	got := LogSafeQueryString("opaque=" + ghp)
	if strings.Contains(got, ghp) {
		t.Fatalf("vendor-shaped credential under an opaque name survived: %q", got)
	}
}
