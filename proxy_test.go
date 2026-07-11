package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProxyStreamsReturnsWhenRemoteClosesWhileStdinIsOpen(t *testing.T) {
	proxySide, remoteSide := net.Pipe()
	stdinReader, stdinWriter := io.Pipe()
	t.Cleanup(func() {
		_ = stdinWriter.Close()
		_ = remoteSide.Close()
	})

	done := make(chan error, 1)
	go func() {
		done <- proxyStreams(proxySide, stdinReader, io.Discard)
	}()

	if err := remoteSide.Close(); err != nil {
		t.Fatalf("close remote side: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("proxyStreams returned error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("proxyStreams did not return after the remote closed")
	}
}

func TestProxyStreamsHalfClosesAfterStdinEOF(t *testing.T) {
	conn := newHalfCloseTestConn("response")
	var stdout bytes.Buffer

	if err := proxyStreams(conn, strings.NewReader("request"), &stdout); err != nil {
		t.Fatalf("proxyStreams returned error: %v", err)
	}

	if got := conn.written.String(); got != "request" {
		t.Fatalf("unexpected remote input: %q", got)
	}
	if got := stdout.String(); got != "response" {
		t.Fatalf("unexpected proxy output: %q", got)
	}
	if !conn.writeClosed {
		t.Fatal("expected proxyStreams to half-close the remote write side")
	}
}

func TestProxyStreamsReturnsRemoteReadError(t *testing.T) {
	wantErr := errors.New("remote read failed")
	conn := &errorReadTestConn{readErr: wantErr}
	stdinReader, stdinWriter := io.Pipe()
	t.Cleanup(func() { _ = stdinWriter.Close() })

	err := proxyStreams(conn, stdinReader, io.Discard)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

type halfCloseTestConn struct {
	mu          sync.Mutex
	written     bytes.Buffer
	response    []byte
	readOffset  int
	writeClosed bool
	closed      bool
	ready       chan struct{}
	readyOnce   sync.Once
}

func newHalfCloseTestConn(response string) *halfCloseTestConn {
	return &halfCloseTestConn{response: []byte(response), ready: make(chan struct{})}
}

func (c *halfCloseTestConn) Read(p []byte) (int, error) {
	<-c.ready
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readOffset == len(c.response) {
		return 0, io.EOF
	}
	n := copy(p, c.response[c.readOffset:])
	c.readOffset += n
	return n, nil
}

func (c *halfCloseTestConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.written.Write(p)
}

func (c *halfCloseTestConn) CloseWrite() error {
	c.mu.Lock()
	c.writeClosed = true
	c.mu.Unlock()
	c.readyOnce.Do(func() { close(c.ready) })
	return nil
}

func (c *halfCloseTestConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.readyOnce.Do(func() { close(c.ready) })
	return nil
}

func (c *halfCloseTestConn) LocalAddr() net.Addr              { return fakeAddr("local") }
func (c *halfCloseTestConn) RemoteAddr() net.Addr             { return fakeAddr("remote") }
func (c *halfCloseTestConn) SetDeadline(time.Time) error      { return nil }
func (c *halfCloseTestConn) SetReadDeadline(time.Time) error  { return nil }
func (c *halfCloseTestConn) SetWriteDeadline(time.Time) error { return nil }

type errorReadTestConn struct {
	readErr error
	closed  chan struct{}
	once    sync.Once
}

func (c *errorReadTestConn) Read([]byte) (int, error) { return 0, c.readErr }
func (c *errorReadTestConn) Write(p []byte) (int, error) {
	return len(p), nil
}
func (c *errorReadTestConn) Close() error {
	c.once.Do(func() {
		if c.closed != nil {
			close(c.closed)
		}
	})
	return nil
}
func (c *errorReadTestConn) LocalAddr() net.Addr              { return fakeAddr("local") }
func (c *errorReadTestConn) RemoteAddr() net.Addr             { return fakeAddr("remote") }
func (c *errorReadTestConn) SetDeadline(time.Time) error      { return nil }
func (c *errorReadTestConn) SetReadDeadline(time.Time) error  { return nil }
func (c *errorReadTestConn) SetWriteDeadline(time.Time) error { return nil }
