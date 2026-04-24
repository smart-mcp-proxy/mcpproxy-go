package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"syscall"
	"testing"
)

func TestClassify_Nil(t *testing.T) {
	if got := Classify(nil, ClassifierHints{}); got != "" {
		t.Errorf("Classify(nil) = %q, want empty", got)
	}
}

func TestClassify_STDIO_SpawnENOENT(t *testing.T) {
	// os/exec wraps ENOENT into *exec.Error when the binary is missing.
	wrapped := &exec.Error{
		Name: "/nonexistent/does-not-exist",
		Err:  syscall.ENOENT,
	}
	got := Classify(wrapped, ClassifierHints{Transport: "stdio"})
	if got != STDIOSpawnENOENT {
		t.Errorf("Classify(enoent) = %q, want %q", got, STDIOSpawnENOENT)
	}
}

func TestClassify_STDIO_SpawnEACCES(t *testing.T) {
	wrapped := &exec.Error{
		Name: "/tmp/not-executable",
		Err:  syscall.EACCES,
	}
	got := Classify(wrapped, ClassifierHints{Transport: "stdio"})
	if got != STDIOSpawnEACCES {
		t.Errorf("Classify(eacces) = %q, want %q", got, STDIOSpawnEACCES)
	}
}

func TestClassify_STDIO_HandshakeTimeout(t *testing.T) {
	err := fmt.Errorf("handshake: %w", context.DeadlineExceeded)
	got := Classify(err, ClassifierHints{Transport: "stdio"})
	if got != STDIOHandshakeTimeout {
		t.Errorf("Classify(timeout) = %q, want %q", got, STDIOHandshakeTimeout)
	}
}

func TestClassify_HTTP_DNSFailed(t *testing.T) {
	err := &net.DNSError{Name: "nope.invalid", Err: "no such host"}
	got := Classify(err, ClassifierHints{Transport: "http"})
	if got != HTTPDNSFailed {
		t.Errorf("Classify(dns) = %q, want %q", got, HTTPDNSFailed)
	}
}

func TestClassify_HTTP_TLSFailed(t *testing.T) {
	err := errors.New("x509: certificate signed by unknown authority")
	got := Classify(err, ClassifierHints{Transport: "http"})
	if got != HTTPTLSFailed {
		t.Errorf("Classify(tls) = %q, want %q", got, HTTPTLSFailed)
	}
}

func TestClassify_HTTP_ConnRefused(t *testing.T) {
	err := fmt.Errorf("dial: %w", syscall.ECONNREFUSED)
	got := Classify(err, ClassifierHints{Transport: "http"})
	if got != HTTPConnRefuse {
		t.Errorf("Classify(connrefuse) = %q, want %q", got, HTTPConnRefuse)
	}
}

func TestClassify_NetworkOffline(t *testing.T) {
	err := &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}
	got := Classify(err, ClassifierHints{})
	if got != NetworkOffline {
		t.Errorf("Classify(netunreach) = %q, want %q", got, NetworkOffline)
	}
}

func TestClassify_Fallback(t *testing.T) {
	err := errors.New("something we don't know about")
	got := Classify(err, ClassifierHints{})
	if got != UnknownUnclassified {
		t.Errorf("Classify(unknown) = %q, want %q", got, UnknownUnclassified)
	}
}

func TestDiagnoseHTTPStatus(t *testing.T) {
	cases := map[int]Code{
		200: "",
		401: HTTPUnauth,
		403: HTTPForbidden,
		404: HTTPNotFound,
		500: HTTPServerErr,
		502: HTTPServerErr,
		599: HTTPServerErr,
	}
	for status, want := range cases {
		got := DiagnoseHTTPStatus(status)
		if got != want {
			t.Errorf("DiagnoseHTTPStatus(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestFixerRegistry(t *testing.T) {
	Register("test_fixer", func(_ context.Context, req FixRequest) (FixResult, error) {
		return FixResult{Outcome: OutcomeSuccess, Preview: "preview for " + req.ServerID}, nil
	})
	res, err := InvokeFixer(context.Background(), "test_fixer", FixRequest{ServerID: "s1", Mode: ModeDryRun})
	if err != nil {
		t.Fatalf("InvokeFixer returned error: %v", err)
	}
	if res.Outcome != OutcomeSuccess {
		t.Errorf("outcome = %q, want success", res.Outcome)
	}
	if res.Preview == "" {
		t.Errorf("preview is empty")
	}

	// Unknown fixer returns ErrUnknownFixer with blocked outcome.
	res, err = InvokeFixer(context.Background(), "does-not-exist", FixRequest{})
	if !errors.Is(err, ErrUnknownFixer) {
		t.Errorf("expected ErrUnknownFixer, got %v", err)
	}
	if res.Outcome != OutcomeBlocked {
		t.Errorf("outcome = %q, want blocked", res.Outcome)
	}
}
