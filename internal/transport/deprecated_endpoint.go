package transport

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// Text forms of an HTTP 410 / deprecation answer. "410" and "gone" must stand
// alone: a bare substring match also hit every error naming an address such
// as 127.0.0.1:41039 (GH #1929 CI: a reconnect after a network outage failed
// on a relay port containing "410" and was parked as "endpoint deprecated").
var (
	responseStatusRe = regexp.MustCompile(`(?i)\b(?:status(?: code)?|http(?:/\d(?:\.\d)?)?)[: ]+([1-5][0-9]{2})\b`)
	goneRe           = regexp.MustCompile(`(?i)\b410 gone\b`)
)

var deprecationPhrases = []string{
	"deprecated",                     // common migration message, and our own "endpoint deprecated"
	"removed",                        // endpoint removed
	"no longer supported",            // common deprecation message
	"use the http transport",         // Sentry-specific migration hint
	"sse transport has been removed", // Sentry-specific error
}

// LooksLikeDeprecatedEndpoint reports whether err says the upstream endpoint
// was deprecated or removed (HTTP 410 Gone), so reconnecting cannot help.
// A socket-level failure (refused, reset, unreachable, DNS, timeout) never
// is, whatever address its text names.
func LooksLikeDeprecatedEndpoint(err error) bool {
	if err == nil {
		return false
	}
	var deprecated *ErrEndpointDeprecated
	if errors.As(err, &deprecated) {
		return true
	}
	var response *HTTPError
	if errors.As(err, &response) && response.StatusCode != 0 {
		return response.StatusCode == 410
	}
	// net/http reports a failed request (socket, TLS, redirect) as a
	// *url.Error and never a response status; its text quotes the request
	// URL, whose path may say "deprecated" or "removed".
	var opErr *net.OpError
	var dnsErr *net.DNSError
	var urlErr *url.Error
	if errors.As(err, &opErr) || errors.As(err, &dnsErr) || errors.As(err, &urlErr) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if match := responseStatusRe.FindStringSubmatch(msg); match != nil {
		return match[1] == "410"
	}
	if goneRe.MatchString(msg) {
		return true
	}
	for _, p := range deprecationPhrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
