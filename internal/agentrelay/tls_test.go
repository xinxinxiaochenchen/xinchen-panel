package agentrelay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"controlplane/internal/agentidentity"
)

const tlsIngressID = "11111111-1111-4111-8111-111111111111"
const tlsEgressID = "22222222-2222-4222-8222-222222222222"

func relayTLSFixture(t *testing.T) (*x509.CertPool, tls.Certificate, tls.Certificate) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Relay test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, public, private)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(private)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	issuer, err := agentidentity.NewIssuer(caPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	issue := func(nodeID string, server bool) tls.Certificate {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
		if err != nil {
			t.Fatal(err)
		}
		request := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})
		var issued agentidentity.IssuedCertificate
		if server {
			issued, err = issuer.IssueRelayServerCertificate(request, nodeID, "relay.example.com", now)
		} else {
			issued, err = issuer.IssueClientCertificate(request, nodeID, now)
		}
		if err != nil {
			t.Fatal(err)
		}
		keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
		pair, err := tls.X509KeyPair(issued.CertificatePEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
		if err != nil {
			t.Fatal(err)
		}
		return pair
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	return roots, issue(tlsIngressID, false), issue(tlsEgressID, true)
}

func certFingerprint(cert tls.Certificate) string {
	sum := sha256.Sum256(cert.Certificate[0])
	return hex.EncodeToString(sum[:])
}

func tlsHandshake(clientConfig, serverConfig *tls.Config) (error, error) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := tls.Client(left, clientConfig)
	server := tls.Server(right, serverConfig)
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.HandshakeContext(ctx) }()
	clientErr := client.HandshakeContext(ctx)
	return clientErr, <-serverResult
}

func TestRelayMutualTLSBindsBothNodeIdentities(t *testing.T) {
	roots, client, server := relayTLSFixture(t)
	serverConfig, err := ServerTLSConfig(func() (*tls.Certificate, error) { return &server, nil }, roots,
		func(nodeID, fingerprint string) bool {
			return nodeID == tlsIngressID && fingerprint == certFingerprint(client)
		})
	if err != nil {
		t.Fatal(err)
	}
	clientConfig, err := ClientTLSConfig(func() (*tls.Certificate, error) { return &client, nil }, roots,
		"relay.example.com", tlsEgressID, []string{certFingerprint(server)})
	if err != nil {
		t.Fatal(err)
	}
	if clientErr, serverErr := tlsHandshake(clientConfig, serverConfig); clientErr != nil || serverErr != nil {
		t.Fatalf("mTLS failed: client %v, server %v", clientErr, serverErr)
	}
}

func TestRelayMutualTLSRejectsWrongPeerAndAddress(t *testing.T) {
	roots, client, server := relayTLSFixture(t)
	getClient := func() (*tls.Certificate, error) { return &client, nil }
	getServer := func() (*tls.Certificate, error) { return &server, nil }
	serverConfig, _ := ServerTLSConfig(getServer, roots, func(nodeID, _ string) bool { return nodeID == tlsIngressID })
	for name, peer := range map[string]struct{ host, nodeID, fingerprint string }{
		"wrong node":        {"relay.example.com", tlsIngressID, certFingerprint(server)},
		"wrong SAN":         {"other.example.com", tlsEgressID, certFingerprint(server)},
		"wrong fingerprint": {"relay.example.com", tlsEgressID, certFingerprint(client)},
	} {
		t.Run(name, func(t *testing.T) {
			config, err := ClientTLSConfig(getClient, roots, peer.host, peer.nodeID, []string{peer.fingerprint})
			if err != nil {
				t.Fatal(err)
			}
			if clientErr, _ := tlsHandshake(config, serverConfig); clientErr == nil {
				t.Fatal("wrong peer accepted")
			}
		})
	}
	clientConfig, _ := ClientTLSConfig(getClient, roots, "relay.example.com", tlsEgressID, []string{certFingerprint(server)})
	denied, _ := ServerTLSConfig(getServer, roots, func(string, string) bool { return false })
	if _, serverErr := tlsHandshake(clientConfig, denied); serverErr == nil {
		t.Fatal("unapproved source accepted")
	}
}

func TestRelayTLSConfigOwnsFingerprintSnapshot(t *testing.T) {
	roots, client, server := relayTLSFixture(t)
	fingerprints := []string{certFingerprint(server)}
	clientConfig, err := ClientTLSConfig(func() (*tls.Certificate, error) { return &client, nil }, roots,
		"relay.example.com", tlsEgressID, fingerprints)
	if err != nil {
		t.Fatal(err)
	}
	fingerprints[0] = certFingerprint(client)
	serverConfig, err := ServerTLSConfig(func() (*tls.Certificate, error) { return &server, nil }, roots, func(string, string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if clientErr, serverErr := tlsHandshake(clientConfig, serverConfig); clientErr != nil || serverErr != nil {
		t.Fatalf("caller mutated TLS authorization snapshot: %v / %v", clientErr, serverErr)
	}
}
