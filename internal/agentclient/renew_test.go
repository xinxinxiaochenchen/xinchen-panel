package agentclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentproto"
	"github.com/coder/websocket"
)

func TestAgentClientSendsCertificateUpdateOnExistingStream(t *testing.T) {
	pair := testClientCertificate(t)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]})
	updates := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, _, err := conn.Read(readCtx); err != nil {
			t.Error(err)
			return
		} // hello
		_, raw, err := conn.Read(readCtx)
		if err != nil {
			t.Error(err)
			return
		}
		message, err := agentproto.Decode(raw)
		if err != nil || message.Type != agentproto.TypeCertificateUpdate {
			t.Errorf("update message: %+v %v", message, err)
			return
		}
		update, err := agentproto.DecodeCertificateUpdate(message.Payload)
		if err != nil {
			t.Error(err)
			return
		}
		updates <- update.CertificatePEM
		wrongPayload, _ := json.Marshal(agentproto.CertificateUpdateAck{Fingerprint: strings.Repeat("0", 64)})
		wrongAck, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e427", NodeID: testNodeID,
			Type: agentproto.TypeCertificateUpdateAck, SentAt: time.Now(), Payload: wrongPayload})
		if err := conn.Write(readCtx, websocket.MessageText, wrongAck); err != nil {
			t.Error(err)
			return
		}
		sum := sha256.Sum256(pair.Certificate[0])
		ackPayload, _ := json.Marshal(agentproto.CertificateUpdateAck{Fingerprint: hex.EncodeToString(sum[:])})
		ack, _ := agentproto.Encode(agentproto.Envelope{ProtocolVersion: 1, MessageID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e425", NodeID: testNodeID,
			Type: agentproto.TypeCertificateUpdateAck, SentAt: time.Now(), Payload: ackPayload})
		if err := conn.Write(readCtx, websocket.MessageText, ack); err != nil {
			t.Error(err)
			return
		}
		if err := conn.Write(readCtx, websocket.MessageText, ack); err != nil {
			t.Error(err)
			return
		}
		<-readCtx.Done()
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client, err := New(Config{URL: "wss" + strings.TrimPrefix(server.URL, "https") + "/api/v1/agent/stream",
		NodeID: testNodeID, Version: "v1", RootCAs: roots, Certificate: pair}, func() Runtime { return &fakeRuntime{} }, &fakeStateStore{})
	if err != nil {
		t.Fatal(err)
	}
	client.certificateUpdates <- certPEM
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_ = client.RunOnce(ctx)
	if time.Since(started) < 250*time.Millisecond {
		t.Fatal("duplicate certificate confirmation ended the control stream")
	}
	select {
	case got := <-updates:
		if got != string(certPEM) {
			t.Fatal("wrong certificate update")
		}
	default:
		t.Fatal("certificate update not sent")
	}
}

func TestAgentClientReloadsCertificateAfterRenewal(t *testing.T) {
	issuer, caPEM := testEnrollmentIssuer(t)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, private)
	if err != nil {
		t.Fatal(err)
	}
	csr := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	first, err := issuer.IssueClientCertificate(csr, testNodeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := issuer.IssueClientCertificate(csr, testNodeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key")
	if err := os.WriteFile(certPath, first.CertificatePEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{URL: "wss://127.0.0.1:18443/api/v1/agent/stream", NodeID: testNodeID,
		Version: "v1", RootCAs: roots, Certificate: pair, CertFile: certPath, KeyFile: keyPath},
		func() Runtime { return &fakeRuntime{} }, &fakeStateStore{})
	if err != nil {
		t.Fatal(err)
	}
	transport := client.httpClient.Transport.(*http.Transport)
	loaded, err := transport.TLSClientConfig.GetClientCertificate(nil)
	if err != nil || string(loaded.Certificate[0]) != string(pair.Certificate[0]) {
		t.Fatalf("initial certificate: %v", err)
	}
	if err := SaveRenewedCertificate(certPath, keyPath, second.CertificatePEM); err != nil {
		t.Fatal(err)
	}
	loaded, err = transport.TLSClientConfig.GetClientCertificate(nil)
	if err != nil || string(loaded.Certificate[0]) == string(pair.Certificate[0]) {
		t.Fatalf("renewed certificate: %v", err)
	}
}

func TestSaveRenewedCertificateReplacesOnlyCertificate(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key")
	pair := testClientCertificate(t)
	keyBytes, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]})
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SaveRenewedCertificate(certPath, keyPath, []byte("invalid")); err == nil {
		t.Fatal("invalid certificate accepted")
	}
	stored, _ := os.ReadFile(certPath)
	if string(stored) != string(certPEM) {
		t.Fatal("failed renewal replaced certificate")
	}
	if err := SaveRenewedCertificate(certPath, keyPath, certPEM); err != nil {
		t.Fatal(err)
	}
	storedKey, _ := os.ReadFile(keyPath)
	if string(storedKey) != string(keyPEM) {
		t.Fatal("renewal changed private key")
	}
	info, err := os.Stat(certPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("certificate permissions: %v %v", info, err)
	}
}

func TestRenewCertificateUsesCurrentKeyAndValidatesResponse(t *testing.T) {
	issuer, caPEM := testEnrollmentIssuer(t)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, private)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := issuer.IssueClientCertificate(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), testNodeID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(initial.CertificatePEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
			t.Error("renewal lacks client certificate")
			return
		}
		var input struct {
			CSRPEM  string `json:"csr_pem"`
			Version string `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if input.Version != "v1" {
			t.Errorf("version %s", input.Version)
		}
		issued, err := issuer.IssueClientCertificate([]byte(input.CSRPEM), testNodeID, time.Now())
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := tls.X509KeyPair(issued.CertificatePEM, keyPEM); err != nil {
			t.Error("CSR key changed")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"node_id": testNodeID, "certificate_pem": string(issued.CertificatePEM), "ca_certificate_pem": string(caPEM), "expires_at": issued.ExpiresAt})
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	roots.AppendCertsFromPEM(caPEM)
	renewed, err := RenewCertificate(context.Background(), server.URL+"/api/v1/agent/renew", roots, pair, keyPEM, "v1", testNodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(renewed.CertPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	if !renewed.ExpiresAt.After(time.Now().Add(23 * time.Hour)) {
		t.Fatalf("renewed expiry: %s", renewed.ExpiresAt)
	}
	if _, err := RenewCertificate(context.Background(), "http"+strings.TrimPrefix(server.URL, "https")+"/api/v1/agent/renew", roots, pair, keyPEM, "v1", testNodeID); err == nil {
		t.Fatal("plaintext renewal accepted")
	}
}
