//go:build unix

package main

import (
	"errors"
	"net"
	"os"
	"syscall"
)

func passConn(conn net.Conn) error {
	return passConnTo(conn, os.Stdout.Fd())
}

func passConnTo(conn net.Conn, controlFD uintptr) error {
	if conn == nil {
		return errors.New("no connected socket to pass")
	}

	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return errors.New("fdpass requires a TCP connection")
	}

	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return err
	}

	var sendErr error
	controlErr := rawConn.Control(func(fd uintptr) {
		// ProxyUseFdpass expects the proxy command to return a connected socket
		// over stdout using SCM_RIGHTS. After that, ssh owns the connection and
		// this helper can exit.
		rights := syscall.UnixRights(int(fd))
		sendErr = syscall.Sendmsg(int(controlFD), []byte{0}, rights, nil, 0)
	})
	closeConn(conn)
	if controlErr != nil {
		return controlErr
	}
	return sendErr
}
