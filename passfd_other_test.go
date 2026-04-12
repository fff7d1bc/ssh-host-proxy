//go:build !unix

package main

import "testing"

func TestPassConnUnsupportedOnNonUnix(t *testing.T) {
	conn := &fakeConn{}

	err := passConn(conn)
	if err == nil {
		t.Fatal("expected unsupported fdpass error, got nil")
	}

	if conn.closed.Load() == 0 {
		t.Fatal("expected non-unix passConn to close the provided connection")
	}
}
