package oauth

import (
	"context"
	"net/http"
	"time"
)

// Env is the environment an upstream instance runs its OAuth work in (Spec
// 116 PR4, FR-024, FR-024a). Transport is the instance's gated transport:
// every OAuth preflight, resource auto-detection, metadata discovery, cache
// fetch, dynamic client registration, code exchange and refresh the instance
// starts goes through it, so none of them reaches the network once the
// instance is retired. Ctx bounds the resource auto-detection's rate-limit
// waits, as the caller's context did before.
//
// The zero Env is the process default (http.DefaultTransport, a background
// context): the exported helpers that run outside an upstream instance (the
// doctor, the REST diagnostics, the CLI) keep using it.
type Env struct {
	Ctx       context.Context
	Transport http.RoundTripper
}

func defaultEnv() Env { return Env{} }

func (e Env) ctx() context.Context {
	if e.Ctx == nil {
		return context.Background()
	}
	return e.Ctx
}

// transport is the round tripper requests run on. The process default is the
// one allowlisted reference to http.DefaultTransport in this package
// (scripts/ungated-http-allowlist.txt).
func (e Env) transport() http.RoundTripper {
	if e.Transport == nil {
		return http.DefaultTransport
	}
	return e.Transport
}

// client returns an *http.Client on the environment's transport.
func (e Env) client(timeout time.Duration) *http.Client {
	return &http.Client{Transport: e.transport(), Timeout: timeout}
}
