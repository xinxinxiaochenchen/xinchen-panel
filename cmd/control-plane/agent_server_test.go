package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controlplane/internal/platform/config"
)

func TestServeAgentTLSStartsAndStopsWithContext(t *testing.T) {
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer fixture.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := serveAgentTLS(ctx, "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS13,
		Certificates: fixture.TLS.Certificates}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}), slog.New(slog.NewTextHandler(io.Discard, nil)), cancel)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(fixture.Certificate())
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}}
	response, err := client.Get("https://" + listener.Addr().String() + "/api/v1/agent/enroll")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("Agent TLS listener = %d", response.StatusCode)
	}
	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		requestCtx, requestCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "https://"+listener.Addr().String()+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		requestCancel()
		if err != nil {
			return
		}
		response.Body.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Agent TLS listener remained open after cancellation")
}

func TestStartConfiguredAgentServerSkipsUnsetListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := startConfiguredAgentServer(ctx, config.Config{}, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)), cancel)
	if err != nil || listener != nil {
		t.Fatalf("disabled Agent server = %v, %v", listener, err)
	}
}
