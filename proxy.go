package main

import (
	"errors"
	"io"
	"net"
	"os"
)

func proxyConn(conn net.Conn) error {
	defer conn.Close()

	copyErr := make(chan error, 2)

	go func() {
		_, err := io.Copy(conn, os.Stdin)
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			// Half-close after stdin reaches EOF so the remote side can see end of
			// input while we still keep reading its stdout/stderr stream.
			_ = tcpConn.CloseWrite()
		}
		copyErr <- err
	}()

	go func() {
		_, err := io.Copy(os.Stdout, conn)
		copyErr <- err
	}()

	var firstErr error
	for i := 0; i < 2; i++ {
		err := <-copyErr
		if err != nil && !errors.Is(err, net.ErrClosed) && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

func closeUnusedConnections(states map[string]probeResult, selected string) {
	for key, state := range states {
		if key == selected {
			continue
		}
		closeConn(state.conn)
	}
}

func closeAllConnections(states map[string]probeResult) {
	for _, state := range states {
		closeConn(state.conn)
	}
}

func closeConn(conn net.Conn) {
	if conn != nil {
		_ = conn.Close()
	}
}
