package agentruntime

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"controlplane/internal/agentidentity"
	"controlplane/internal/agentmeter"
	"controlplane/internal/agentrelay"
)

func runtimeRelayAuthority(t *testing.T) (*x509.CertPool, func(string, bool) tls.Certificate) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalPKCS8PrivateKey(key)
	capem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	issuer, err := agentidentity.NewIssuer(capem, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(capem)
	return roots, func(node string, server bool) tls.Certificate {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, private)
		if err != nil {
			t.Fatal(err)
		}
		request := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})
		var issued agentidentity.IssuedCertificate
		if server {
			issued, err = issuer.IssueRelayServerCertificate(request, node, "127.0.0.1", now)
		} else {
			issued, err = issuer.IssueClientCertificate(request, node, now)
		}
		if err != nil {
			t.Fatal(err)
		}
		kd, _ := x509.MarshalPKCS8PrivateKey(private)
		pair, err := tls.X509KeyPair(issued.CertificatePEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
		if err != nil {
			t.Fatal(err)
		}
		return pair
	}
}
func runtimeFingerprint(pair tls.Certificate) string {
	sum := sha256.Sum256(pair.Certificate[0])
	return hex.EncodeToString(sum[:])
}

type relayCountingMeter struct{ opens, upload, download atomic.Int64 }

func (m *relayCountingMeter) Open(context.Context, string, string, uint64) (MeteredConnection, error) {
	m.opens.Add(1)
	return m, nil
}
func (m *relayCountingMeter) Copy(w io.Writer, r io.Reader, d agentmeter.Direction) (int64, error) {
	b := make([]byte, 1024)
	var total int64
	for {
		n, e := r.Read(b)
		if n > 0 {
			written, we := w.Write(b[:n])
			total += int64(written)
			if d == agentmeter.Upload {
				m.upload.Add(int64(written))
			} else {
				m.download.Add(int64(written))
			}
			if we != nil {
				return total, we
			}
		}
		if e != nil {
			return total, e
		}
	}
}
func (*relayCountingMeter) WritePacket(io.Writer, []byte, agentmeter.Direction) (int, error) {
	return 0, errors.New("unexpected UDP")
}
func (*relayCountingMeter) Close() error { return nil }

func TestThreeRuntimeProxyRelaysAndMetersOnlyIngress(t *testing.T) {
	roots, issue := runtimeRelayAuthority(t)
	ids := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"}
	clients, servers := make([]tls.Certificate, 3), make([]tls.Certificate, 3)
	ports := []int{unusedTCPPort(t), unusedTCPPort(t), unusedTCPPort(t)}
	for i := range ids {
		clients[i] = issue(ids[i], false)
		servers[i] = issue(ids[i], true)
	}
	meters := []*relayCountingMeter{{}, {}, {}}
	runtimes := make([]*Runtime, 3)
	var forbiddenDials atomic.Int64
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	proxyTLS, userTLS := proxyTestTLS(t)
	for i := range ids {
		i := i
		st, err := agentrelay.ServerTLSConfig(func() (*tls.Certificate, error) { return &servers[i], nil }, roots, func(string, string) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		opts := Options{BindHost: "127.0.0.1", RelayPort: ports[i], RelayTLSConfig: st, ProxyTLSConfig: proxyTLS, RequireMetering: true, Meter: meters[i], RelayClientTLS: func(_ context.Context, next RelayNextHop) (*tls.Config, error) {
			return agentrelay.ClientTLSConfig(func() (*tls.Certificate, error) { return &clients[i], nil }, roots, "127.0.0.1", next.NodeID, next.Fingerprints)
		}, Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}, DialTCP: func(ctx context.Context, address string) (net.Conn, error) {
			if i != 2 {
				forbiddenDials.Add(1)
				return nil, errors.New("ingress or middle attempted direct dial")
			}
			if address != "8.8.8.8:443" {
				return nil, errors.New("unexpected target")
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", echo.Addr().String())
		}, RelayDial: func(ctx context.Context, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		}}
		runtimes[i] = New(opts)
		defer runtimes[i].Close()
	}
	const line = "44444444-4444-4444-8444-444444444444"
	configs := make([]RelayConfig, 3)
	for i := range ids {
		configs[i] = RelayConfig{LineID: line, Generation: 1, Role: []RelayRole{RelayIngress, RelayMiddle, RelayEgress}[i]}
		if i > 0 {
			configs[i].PreviousNodeID = ids[i-1]
			configs[i].PreviousSecret = bytes.Repeat([]byte{byte(i)}, 32)
			configs[i].PreviousFingerprints = []string{runtimeFingerprint(clients[i-1])}
		}
		if i < 2 {
			configs[i].Next = &RelayNextHop{NodeID: ids[i+1], Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[i+1])), Port: ports[i+1], Secret: bytes.Repeat([]byte{byte(i + 1)}, 32), Fingerprints: []string{runtimeFingerprint(servers[i+1])}}
		}
	}
	proxy := testProxyConfig()
	proxy.LineID = line
	proxy.IngressPort = unusedTCPPort(t)
	proxy.RelayGeneration = 1
	for i := 2; i >= 0; i-- {
		snap := Snapshot{Revision: 1, RelayConfig: []RelayConfig{configs[i]}}
		if i == 0 {
			snap.ProxyConfig = []ProxyAccess{proxy}
		}
		if err := runtimes[i].Apply(context.Background(), snap); err != nil {
			t.Fatal(err)
		}
	}
	c, err := tls.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxy.IngressPort)), userTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("three runtime payload")
	if _, err := c.Write(append(trojanRequest(proxy.CredentialHash, "example.org", 443, 1), payload...)); err != nil {
		t.Fatal(err)
	}
	result := make([]byte, len(payload))
	if _, err := io.ReadFull(c, result); err != nil || !bytes.Equal(result, payload) {
		t.Fatalf("three-runtime payload mismatch: %v", err)
	}
	if forbiddenDials.Load() != 0 || meters[0].opens.Load() != 1 || meters[1].opens.Load() != 0 || meters[2].opens.Load() != 0 {
		t.Fatal("traffic bypassed relay or metered more than once")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && (meters[0].upload.Load() != int64(len(payload)) || meters[0].download.Load() != int64(len(payload))) {
		time.Sleep(time.Millisecond)
	}
	if meters[0].upload.Load() != int64(len(payload)) || meters[0].download.Load() != int64(len(payload)) {
		t.Fatal("relay handshake bytes were included in ingress payload accounting")
	}
	if err := runtimes[1].Apply(context.Background(), Snapshot{Revision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("revoked middle route remained open")
	}
}

func TestRelayProxySnapshotCannotSilentlyDialDirect(t *testing.T) {
	proxy := testProxyConfig()
	proxy.RelayGeneration = 2
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}}); err == nil {
		t.Fatal("multi-hop proxy accepted without ingress route")
	}
	route := relayTestConfig(RelayIngress)
	route.LineID = proxy.LineID
	if _, err := ValidateSnapshot(Snapshot{Revision: 1, ProxyConfig: []ProxyAccess{proxy}, RelayConfig: []RelayConfig{route}}); err == nil {
		t.Fatal("multi-hop proxy accepted stale generation")
	}
}
