package agentrelay

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"time"
)

// NextHop is provided by an applied control-plane snapshot. The inbound OPEN
// frame cannot choose or override Address, TLSConfig or Secret.
type NextHop struct {
	Address   string
	TLSConfig *tls.Config
	Secret    []byte
}

func DialLine(ctx context.Context, next NextHop, request Open) (net.Conn, error) {
	return dialLine(ctx, next, request, nil)
}

func dialLine(ctx context.Context, next NextHop, request Open, dial func(context.Context, string) (net.Conn, error)) (net.Conn, error) {
	if next.TLSConfig == nil || next.Address == "" || len(next.Secret) != 32 ||
		next.TLSConfig.InsecureSkipVerify || next.TLSConfig.MinVersion < tls.VersionTLS13 ||
		next.TLSConfig.RootCAs == nil || next.TLSConfig.ServerName == "" || next.TLSConfig.VerifyConnection == nil {
		return nil, errors.New("invalid next relay configuration")
	}
	_, port, err := net.SplitHostPort(next.Address)
	if err != nil {
		return nil, errors.New("invalid next relay address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, errors.New("invalid next relay port")
	}
	request.Proof = ""
	request, err = SignOpen(request, next.Secret)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if dial == nil {
		transport := &net.Dialer{Timeout: 10 * time.Second}
		dial = func(ctx context.Context, address string) (net.Conn, error) {
			return transport.DialContext(ctx, "tcp", address)
		}
	}
	raw, err := dial(dialCtx, next.Address)
	if err != nil || raw == nil {
		return nil, errors.New("next relay is unavailable")
	}
	connection := tls.Client(raw, next.TLSConfig.Clone())
	fail := func() (net.Conn, error) {
		closeImmediately(connection)
		return nil, errors.New("next relay handshake failed")
	}
	stop := context.AfterFunc(dialCtx, func() { closeImmediately(connection) })
	defer stop()
	deadline, _ := dialCtx.Deadline()
	_ = connection.SetDeadline(deadline)
	if err := connection.HandshakeContext(dialCtx); err != nil {
		return fail()
	}
	if err := WriteOpen(connection, request); err != nil {
		return fail()
	}
	response, err := ReadOpenResponse(connection)
	if err != nil || response.Type != "open_ok" || response.ConnectionID != request.ConnectionID {
		return fail()
	}
	if !stop() || dialCtx.Err() != nil {
		return fail()
	}
	_ = connection.SetDeadline(time.Time{})
	return connection, nil
}
