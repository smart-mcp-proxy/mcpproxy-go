package dialgate

import (
	"net"
	"testing"
)

// T4.7: the per-Write cost of the gate (one uncontended RLock and two atomic
// loads) against an ungated conn, 1 KiB writes. Compare
// BenchmarkGatedConnWrite with BenchmarkUngatedConnWrite (< 5% budget).

type discardConn struct{ net.Conn }

func (discardConn) Write(b []byte) (int, error) { return len(b), nil }
func (discardConn) Close() error                { return nil }

func BenchmarkUngatedConnWrite(b *testing.B) {
	c := net.Conn(discardConn{})
	buf := make([]byte, 1024)
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Write(buf)
	}
}

func BenchmarkGatedConnWrite(b *testing.B) {
	g := New()
	c := &gatedConn{Conn: discardConn{}, g: g}
	buf := make([]byte, 1024)
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Write(buf)
	}
}

// BenchmarkGatedConnWriteLoopback measures the gate against a real loopback
// socket write, the shape of an upstream request.
func BenchmarkGatedConnWriteLoopback(b *testing.B) {
	benchLoopback(b, true)
}

func BenchmarkUngatedConnWriteLoopback(b *testing.B) {
	benchLoopback(b, false)
}

func benchLoopback(b *testing.B, gated bool) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 64<<10)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	defer raw.Close()
	c := raw
	if gated {
		c = &gatedConn{Conn: raw, g: New()}
	}
	buf := make([]byte, 1024)
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Write(buf); err != nil {
			b.Fatal(err)
		}
	}
}
