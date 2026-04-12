//go:build unix

package main

import (
	"net"
	"testing"
)

func TestPassConnRejectsNonTCPConnOnUnix(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	err := passConn(left)
	if err == nil {
		t.Fatal("expected fdpass error for non-TCP connection, got nil")
	}
}
