//go:build !unix

package main

import (
	"errors"
	"net"
)

func passConn(conn net.Conn) error {
	closeConn(conn)
	return errors.New("--fdpass is only supported on Unix-like systems")
}
