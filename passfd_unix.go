//go:build unix

package main

import (
	"errors"
	"net"
	"os"
	"syscall"
)

func passConn(conn net.Conn) error {
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
		rights := syscall.UnixRights(int(fd))
		sendErr = syscall.Sendmsg(int(os.Stdout.Fd()), []byte{0}, rights, nil, 0)
	})
	closeConn(conn)
	if controlErr != nil {
		return controlErr
	}
	return sendErr
}
