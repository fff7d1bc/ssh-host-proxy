package main

import (
	"errors"
	"io"
	"net"
	"os"
)

func proxyConn(conn net.Conn) error {
	return proxyStreams(conn, os.Stdin, os.Stdout)
}

type proxyCopyDirection uint8

const (
	copyToRemote proxyCopyDirection = iota
	copyFromRemote
)

type proxyCopyResult struct {
	direction proxyCopyDirection
	err       error
}

func proxyStreams(conn net.Conn, stdin io.Reader, stdout io.Writer) error {
	defer conn.Close()

	copyResults := make(chan proxyCopyResult, 2)

	go func() {
		_, err := io.Copy(conn, stdin)
		if err == nil {
			// Half-close after stdin reaches EOF so the remote side can see end of
			// input while we still keep reading its stdout/stderr stream.
			if closeWriter, ok := conn.(interface{ CloseWrite() error }); ok {
				err = closeWriter.CloseWrite()
			}
		}
		copyResults <- proxyCopyResult{direction: copyToRemote, err: err}
	}()

	go func() {
		_, err := io.Copy(stdout, conn)
		copyResults <- proxyCopyResult{direction: copyFromRemote, err: err}
	}()

	first := <-copyResults
	if first.direction == copyFromRemote || first.err != nil {
		// Once the remote stream ends there is nothing more to deliver to ssh.
		// Return without waiting for stdin, which may remain open and blocked.
		return normalizeProxyError(first.err)
	}

	// A clean stdin EOF has half-closed the TCP write side. Keep reading until
	// the remote finishes so responses are not truncated.
	return normalizeProxyError((<-copyResults).err)
}

func normalizeProxyError(err error) error {
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
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
