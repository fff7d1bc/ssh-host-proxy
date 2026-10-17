//go:build unix

package main

import (
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
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

func TestPassConnTransfersLiveTCPConnection(t *testing.T) {
	conn, peer := tcpTestPair(t)
	control, receiver := fdpassControlPair(t)
	if err := passConnTo(conn, control.Fd()); err != nil {
		t.Fatalf("pass connection: %v", err)
	}
	assertTCPClosed(t, conn)

	payload := make([]byte, 2)
	oob := make([]byte, syscall.CmsgSpace(4))
	n, oobn, flags, _, err := receiver.ReadMsgUnix(payload, oob)
	if err != nil {
		t.Fatalf("receive descriptor: %v", err)
	}
	messages, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		t.Fatal(err)
	}
	var files []*os.File
	for _, message := range messages {
		fds, err := syscall.ParseUnixRights(&message)
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range fds {
			file := os.NewFile(uintptr(fd), "received-tcp")
			t.Cleanup(func() { file.Close() })
			files = append(files, file)
		}
	}
	if flags&syscall.MSG_CTRUNC != 0 || n != 1 || payload[0] != 0 || len(messages) != 1 || len(files) != 1 {
		t.Fatalf("unexpected fdpass message: payload=%v, flags=%d, messages=%d, fds=%d", payload[:n], flags, len(messages), len(files))
	}
	received, err := net.FileConn(files[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { received.Close() })
	if err := received.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// The transferred descriptor must remain usable after the sender has
	// closed its original connection, in both directions of the SSH stream.
	for _, direction := range []struct {
		from, to net.Conn
		message  string
	}{
		{received, peer, "request"},
		{peer, received, "response"},
	} {
		if _, err := io.WriteString(direction.from, direction.message); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(direction.message))
		if _, err := io.ReadFull(direction.to, got); err != nil {
			t.Fatal(err)
		}
		if string(got) != direction.message {
			t.Fatalf("received %q, want %q", got, direction.message)
		}
	}
}

func TestPassConnClosesTCPConnectionAfterSendFailure(t *testing.T) {
	conn, _ := tcpTestPair(t)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	// A pipe is a valid descriptor but cannot carry SCM_RIGHTS.
	if err := passConnTo(conn, writer.Fd()); err == nil {
		t.Fatal("expected descriptor-passing error on a pipe")
	}
	assertTCPClosed(t, conn)
}

func assertTCPClosed(t *testing.T, conn *net.TCPConn) {
	t.Helper()
	if _, err := conn.Write([]byte("closed")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("expected original connection to be closed, got %v", err)
	}
}

func tcpTestPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialTimeout("tcp4", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	server, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	for _, conn := range []net.Conn{client, server} {
		if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	return client.(*net.TCPConn), server
}

func fdpassControlPair(t *testing.T) (*os.File, *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	sender := os.NewFile(uintptr(fds[0]), "fdpass-sender")
	reader := os.NewFile(uintptr(fds[1]), "fdpass-receiver")
	t.Cleanup(func() { sender.Close(); reader.Close() })
	conn, err := net.FileConn(reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return sender, conn.(*net.UnixConn)
}
