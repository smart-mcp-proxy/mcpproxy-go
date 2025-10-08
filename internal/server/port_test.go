package server

import (
	"net"
	"testing"
)

func TestFindAvailableListenAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open temporary listener: %v", err)
	}
	defer ln.Close()

	base := ln.Addr().String()

	candidate, err := findAvailableListenAddress(base, 5)
	if err != nil {
		t.Fatalf("findAvailableListenAddress returned error: %v", err)
	}

	if candidate == base {
		t.Fatalf("expected alternate address different from base; got %s", candidate)
	}

	ln2, err := net.Listen("tcp", candidate)
	if err != nil {
		t.Fatalf("candidate address is not bindable: %v", err)
	}
	_ = ln2.Close()
}

func TestSplitListenAddressValidation(t *testing.T) {
	if _, _, err := splitListenAddress(""); err == nil {
		t.Fatalf("expected error for empty listen address")
	}

	if _, _, err := splitListenAddress("8080"); err == nil {
		t.Fatalf("expected error for missing host separator")
	}

	host, port, err := splitListenAddress("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "127.0.0.1" || port != 8080 {
		t.Fatalf("unexpected split result host=%s port=%d", host, port)
	}
}

func TestPortInUseDetection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open listener: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	_, err = net.Listen("tcp", addr)
	if err == nil {
		t.Fatalf("expected port to be in use when double binding")
	}

	if !isAddrInUseError(err) {
		t.Fatalf("expected isAddrInUseError to detect address in use error, got: %v (type: %T)", err, err)
	}
}
