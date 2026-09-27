package agentrelay

import (
	"crypto/tls"
	"io"
	"net"
	"time"
)

func closeImmediately(conn net.Conn) {
	if encrypted, ok := conn.(*tls.Conn); ok {
		// A revoked route must not wait for TLS close_notify to be read.
		_ = encrypted.NetConn().Close()
	}
	_ = conn.SetDeadline(time.Now())
	_ = conn.Close()
}

func finishWrite(conn net.Conn) {
	if half, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
	} else {
		closeImmediately(conn)
	}
}

func copyStreams(left, right net.Conn) {
	done := make(chan error, 2)
	copyOne := func(dst, src net.Conn) {
		_, err := io.Copy(dst, src)
		finishWrite(dst)
		done <- err
	}
	go copyOne(left, right)
	go copyOne(right, left)
	if err := <-done; err != nil {
		closeImmediately(left)
		closeImmediately(right)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		closeImmediately(left)
		closeImmediately(right)
		<-done
	}
}
